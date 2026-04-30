package scanner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileSystem abstracts filesystem operations for testability.
type FileSystem interface {
	ReadFile(name string) ([]byte, error)
	ReadDir(name string) ([]os.DirEntry, error)
	Readlink(name string) (string, error)
	Stat(name string) (os.FileInfo, error)
	UserHomeDir() (string, error)
	Glob(pattern string) ([]string, error)
	Exec(name string, arg ...string) ([]byte, error)
	Open(name string) (*os.File, error)
}

// RealFileSystem delegates to the real os package.
type RealFileSystem struct{}

func (RealFileSystem) ReadFile(name string) ([]byte, error)    { return os.ReadFile(name) }
func (RealFileSystem) ReadDir(name string) ([]os.DirEntry, error) { return os.ReadDir(name) }
func (RealFileSystem) Readlink(name string) (string, error)    { return os.Readlink(name) }
func (RealFileSystem) Stat(name string) (os.FileInfo, error)   { return os.Stat(name) }
func (RealFileSystem) UserHomeDir() (string, error)            { return os.UserHomeDir() }
func (RealFileSystem) Glob(pattern string) ([]string, error)   { return filepath.Glob(pattern) }
func (RealFileSystem) Exec(name string, arg ...string) ([]byte, error) {
	return exec.Command(name, arg...).Output()
}
func (RealFileSystem) Open(name string) (*os.File, error) { return os.Open(name) }

// memDirEntry implements os.DirEntry for MemFileSystem.
type memDirEntry struct {
	name  string
	isDir bool
	info  os.FileInfo
}

func (e memDirEntry) Name() string               { return e.name }
func (e memDirEntry) IsDir() bool                { return e.isDir }
func (e memDirEntry) Type() os.FileMode          { return e.info.Mode().Type() }
func (e memDirEntry) Info() (os.FileInfo, error) { return e.info, nil }

// memFileInfo implements os.FileInfo for MemFileSystem.
type memFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func (fi memFileInfo) Name() string       { return fi.name }
func (fi memFileInfo) Size() int64        { return fi.size }
func (fi memFileInfo) Mode() os.FileMode  { return fi.mode }
func (fi memFileInfo) ModTime() time.Time { return fi.modTime }
func (fi memFileInfo) IsDir() bool        { return fi.isDir }
func (fi memFileInfo) Sys() interface{}   { return nil }

// MemFileSystem is an in-memory filesystem for tests.
type MemFileSystem struct {
	mu      sync.RWMutex
	files   map[string][]byte
	dirs    map[string]bool
	links   map[string]string
	stats   map[string]memFileInfo
	home    string
	execs   map[string]func(name string, arg ...string) ([]byte, error)
}

// NewMemFileSystem creates a new in-memory filesystem.
func NewMemFileSystem() *MemFileSystem {
	return &MemFileSystem{
		files: make(map[string][]byte),
		dirs:  make(map[string]bool),
		links: make(map[string]string),
		stats: make(map[string]memFileInfo),
		execs: make(map[string]func(name string, arg ...string) ([]byte, error)),
	}
}

// SetHome sets the home directory returned by UserHomeDir.
func (m *MemFileSystem) SetHome(home string) { m.home = home }

// WriteFile creates or overwrites a file.
func (m *MemFileSystem) WriteFile(name string, data []byte, perm os.FileMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name = filepath.Clean(name)
	m.files[name] = data
	m.stats[name] = memFileInfo{name: filepath.Base(name), size: int64(len(data)), mode: perm, modTime: time.Now(), isDir: false}
	// Ensure parent dirs exist
	dir := filepath.Dir(name)
	for dir != "/" && dir != "." {
		m.dirs[dir] = true
		dir = filepath.Dir(dir)
	}
}

// MkdirAll creates a directory and all parents.
func (m *MemFileSystem) MkdirAll(path string, perm os.FileMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path = filepath.Clean(path)
	m.dirs[path] = true
	m.stats[path] = memFileInfo{name: filepath.Base(path), mode: perm | os.ModeDir, modTime: time.Now(), isDir: true}
	// Ensure parent dirs exist
	dir := filepath.Dir(path)
	for dir != "/" && dir != "." {
		m.dirs[dir] = true
		dir = filepath.Dir(dir)
	}
}

// Symlink creates a symbolic link.
func (m *MemFileSystem) Symlink(oldname, newname string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	newname = filepath.Clean(newname)
	m.links[newname] = oldname
}

// SetExec registers a mock exec command.
func (m *MemFileSystem) SetExec(name string, fn func(name string, arg ...string) ([]byte, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.execs[name] = fn
}

// SetModTime sets the modification time of a file or directory.
func (m *MemFileSystem) SetModTime(name string, t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name = filepath.Clean(name)
	if stat, ok := m.stats[name]; ok {
		stat.modTime = t
		m.stats[name] = stat
	}
}

func (m *MemFileSystem) ReadFile(name string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	name = filepath.Clean(name)
	if data, ok := m.files[name]; ok {
		return data, nil
	}
	return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
}

func (m *MemFileSystem) ReadDir(name string) ([]os.DirEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	name = filepath.Clean(name)
	if !m.dirs[name] {
		// Check if it exists as a file
		if _, ok := m.files[name]; ok {
			return nil, &os.PathError{Op: "readdir", Path: name, Err: os.ErrInvalid}
		}
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
	}
	var entries []os.DirEntry
	seen := make(map[string]bool)
	for path := range m.dirs {
		if path == name {
			continue
		}
		dir := filepath.Dir(path)
		if dir == name {
			base := filepath.Base(path)
			if !seen[base] {
				seen[base] = true
				info := m.stats[path]
				entries = append(entries, memDirEntry{name: base, isDir: true, info: info})
			}
		}
	}
	for path := range m.files {
		dir := filepath.Dir(path)
		if dir == name {
			base := filepath.Base(path)
			if !seen[base] {
				seen[base] = true
				info := m.stats[path]
				entries = append(entries, memDirEntry{name: base, isDir: false, info: info})
			}
		}
	}
	for path, target := range m.links {
		dir := filepath.Dir(path)
		if dir == name {
			base := filepath.Base(path)
			if !seen[base] {
				seen[base] = true
				info := memFileInfo{name: base, mode: os.ModeSymlink, modTime: time.Now(), isDir: false}
				entries = append(entries, memDirEntry{name: base, isDir: false, info: info})
				_ = target
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
}

func (m *MemFileSystem) Readlink(name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	name = filepath.Clean(name)
	if target, ok := m.links[name]; ok {
		return target, nil
	}
	return "", &os.PathError{Op: "readlink", Path: name, Err: os.ErrNotExist}
}

func (m *MemFileSystem) Stat(name string) (os.FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	name = filepath.Clean(name)
	if stat, ok := m.stats[name]; ok {
		return stat, nil
	}
	if m.dirs[name] {
		if stat, ok := m.stats[name]; ok {
			return stat, nil
		}
		return memFileInfo{name: filepath.Base(name), mode: os.ModeDir | 0o755, modTime: time.Now(), isDir: true}, nil
	}
	return nil, &os.PathError{Op: "stat", Path: name, Err: os.ErrNotExist}
}

func (m *MemFileSystem) UserHomeDir() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.home != "" {
		return m.home, nil
	}
	return "/home/test", nil
}

func (m *MemFileSystem) Glob(pattern string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var matches []string
	for path := range m.files {
		if matched, _ := filepath.Match(pattern, path); matched {
			matches = append(matches, path)
		}
	}
	for path := range m.dirs {
		if matched, _ := filepath.Match(pattern, path); matched {
			matches = append(matches, path)
		}
	}
	for path := range m.links {
		if matched, _ := filepath.Match(pattern, path); matched {
			matches = append(matches, path)
		}
	}
	return matches, nil
}

func (m *MemFileSystem) Exec(name string, arg ...string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if fn, ok := m.execs[name]; ok {
		return fn(name, arg...)
	}
	return nil, fmt.Errorf("exec %s: command not found in memfs", name)
}

func (m *MemFileSystem) Open(name string) (*os.File, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	name = filepath.Clean(name)
	if data, ok := m.files[name]; ok {
		// Return a real temp file with the data so os.File methods work
		f, err := os.CreateTemp("", "memfs-*")
		if err != nil {
			return nil, err
		}
		f.Write(data)
		f.Seek(0, 0)
		return f, nil
	}
	return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
}
