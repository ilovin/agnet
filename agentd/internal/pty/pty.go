package pty

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
)

// Process wraps a PTY-attached child process.
type Process struct {
	cmd  *exec.Cmd
	ptmx *os.File
}

// Spawn starts cmd with args in workDir, attached to a PTY. env is merged with os.Environ().
func Spawn(cmd string, args []string, workDir string, env []string) (*Process, error) {
	c := exec.Command(cmd, args...)
	c.Dir = workDir
	c.Env = append(os.Environ(), env...)

	ptmx, err := pty.Start(c)
	if err != nil {
		return nil, fmt.Errorf("pty.Start: %w", err)
	}
	return &Process{cmd: c, ptmx: ptmx}, nil
}

// Read reads raw PTY output bytes.
func (p *Process) Read(buf []byte) (int, error) {
	return p.ptmx.Read(buf)
}

// Write sends input bytes to the PTY (as if typed by the user).
func (p *Process) Write(data []byte) (int, error) {
	return p.ptmx.Write(data)
}

// SetReadDeadline sets a deadline on the underlying PTY file.
func (p *Process) SetReadDeadline(t time.Time) {
	_ = p.ptmx.SetReadDeadline(t)
}

// Kill sends SIGKILL to the child process and closes the PTY.
// Process is killed first to avoid SIGHUP-induced zombie from closing ptmx early.
func (p *Process) Kill() error {
	var err error
	if p.cmd.Process != nil {
		err = p.cmd.Process.Kill()
	}
	_ = p.ptmx.Close()
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
