package pty

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
)

// Process wraps a child process (PTY or pipe mode).
type Process struct {
	cmd    *exec.Cmd
	ptmx   *os.File      // PTY mode: the PTY master
	stdin  io.WriteCloser // Pipe mode: stdin pipe
	stdout io.ReadCloser  // Pipe mode: stdout pipe
	usePTY bool
}

// Spawn starts cmd with args in workDir, attached to a PTY. env is merged with os.Environ().
func Spawn(cmd string, args []string, workDir string, env []string) (*Process, error) {
	return SpawnWithMode(cmd, args, workDir, env, true)
}

// SpawnPipes starts cmd with args in workDir using pipes (non-PTY mode).
// This is useful for non-interactive mode where TUI should be suppressed.
func SpawnPipes(cmd string, args []string, workDir string, env []string) (*Process, error) {
	return SpawnWithMode(cmd, args, workDir, env, false)
}

// SpawnWithMode starts cmd with args in workDir, using either PTY or pipes.
func SpawnWithMode(cmd string, args []string, workDir string, env []string, usePTY bool) (*Process, error) {
	// Resolve command via user's home bin paths if not absolute
	if cmd != "" && cmd[0] != '/' {
		home, _ := os.UserHomeDir()
		extraPaths := []string{
			home + "/.local/bin",
			home + "/.cargo/bin",
			home + "/.opencode/bin",
			home + "/.npm-global/bin",
			"/usr/local/bin",
		}
		for _, dir := range extraPaths {
			full := dir + "/" + cmd
			if _, err := os.Stat(full); err == nil {
				cmd = full
				break
			}
		}
	}

	c := exec.Command(cmd, args...)
	c.Dir = workDir
	c.Env = append(os.Environ(), env...)

	if usePTY {
		ptmx, err := pty.Start(c)
		if err != nil {
			return nil, fmt.Errorf("pty.Start: %w", err)
		}
		return &Process{cmd: c, ptmx: ptmx, usePTY: true}, nil
	}

	// Pipe mode: use stdin/stdout pipes
	stdin, err := c.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// stderr goes to stdout in pipe mode to capture all output
	c.Stderr = c.Stdout

	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}
	return &Process{cmd: c, stdin: stdin, stdout: stdout, usePTY: false}, nil
}

// Read reads raw output bytes (from PTY or pipe).
func (p *Process) Read(buf []byte) (int, error) {
	if p.usePTY {
		return p.ptmx.Read(buf)
	}
	return p.stdout.Read(buf)
}

// Write sends input bytes (to PTY or pipe).
func (p *Process) Write(data []byte) (int, error) {
	if p.usePTY {
		return p.ptmx.Write(data)
	}
	return p.stdin.Write(data)
}

// CloseStdin closes the stdin pipe to signal EOF to the child process.
// This is useful for pipe mode where the child reads from stdin.
func (p *Process) CloseStdin() error {
	if !p.usePTY && p.stdin != nil {
		return p.stdin.Close()
	}
	return nil
}

// SetReadDeadline sets a deadline on the underlying file (PTY mode only).
func (p *Process) SetReadDeadline(t time.Time) {
	if p.usePTY {
		_ = p.ptmx.SetReadDeadline(t)
	}
}

// Kill sends SIGKILL to the child process and closes handles.
// For PTY: Process is killed first to avoid SIGHUP-induced zombie from closing ptmx early.
func (p *Process) Kill() error {
	var err error
	if p.cmd.Process != nil {
		err = p.cmd.Process.Kill()
	}
	if p.usePTY {
		_ = p.ptmx.Close()
	} else {
		_ = p.stdin.Close()
		_ = p.stdout.Close()
	}
	return err
}

// Wait waits for the child process to exit.
func (p *Process) Wait() error {
	return p.cmd.Wait()
}

// Pid returns the child process PID.
func (p *Process) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// UsePTY returns true if the process is using PTY mode (false for pipe mode).
func (p *Process) UsePTY() bool {
	return p.usePTY
}
