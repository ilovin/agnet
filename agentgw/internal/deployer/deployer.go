package deployer

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"path/filepath"

	gossh "golang.org/x/crypto/ssh"
)

// Step describes a single deploy action.
type Step struct {
	Kind    string // "mkdir", "upload", "exec"
	Command string // for exec/mkdir steps
	Path    string // for upload/mkdir steps
	Data    []byte // for upload steps
}

// HashBinary returns the SHA256 hex digest of data.
func HashBinary(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

// PlanSteps returns the deploy steps for uploading agentd to remoteDir.
func PlanSteps(remoteDir string, binaryData []byte) []Step {
	binPath := filepath.Join(remoteDir, "agentd")
	return []Step{
		{Kind: "mkdir", Path: remoteDir, Command: "mkdir -p " + remoteDir},
		{Kind: "upload", Path: binPath, Data: binaryData},
		{Kind: "exec", Command: "chmod +x " + binPath},
		{Kind: "exec", Command: binPath + " version || true"},
		{Kind: "exec", Command: "pkill -f 'agentd start' || true"},
		{Kind: "exec", Command: binPath + " start &"},
	}
}

// Deployer uploads agentd to a remote machine via SSH and starts it.
type Deployer struct {
	client *gossh.Client
}

func New(client *gossh.Client) *Deployer {
	return &Deployer{client: client}
}

// Deploy executes all deploy steps on the remote machine.
func (d *Deployer) Deploy(remoteDir string, binaryData []byte) error {
	steps := PlanSteps(remoteDir, binaryData)
	for _, step := range steps {
		if err := d.execStep(step); err != nil {
			return fmt.Errorf("step %s %q: %w", step.Kind, step.Command, err)
		}
	}
	return nil
}

// RemoteHash returns the SHA256 of the agentd binary on the remote host, or "" if not found.
func (d *Deployer) RemoteHash(remoteDir string) string {
	binPath := filepath.Join(remoteDir, "agentd")
	out, err := d.runCommand("sha256sum " + binPath + " 2>/dev/null | awk '{print $1}'")
	if err != nil || len(out) < 64 {
		return ""
	}
	return string(bytes.TrimSpace(out))
}

func (d *Deployer) execStep(step Step) error {
	switch step.Kind {
	case "mkdir", "exec":
		_, err := d.runCommand(step.Command)
		return err
	case "upload":
		return d.scpUpload(step.Path, step.Data)
	}
	return fmt.Errorf("unknown step kind: %s", step.Kind)
}

func (d *Deployer) runCommand(cmd string) ([]byte, error) {
	sess, err := d.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()
	return sess.Output(cmd)
}

// scpUpload uploads data to remotePath using SCP protocol.
func (d *Deployer) scpUpload(remotePath string, data []byte) error {
	sess, err := d.client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	filename := filepath.Base(remotePath)
	remoteDir := filepath.Dir(remotePath)

	stdin, err := sess.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	if err := sess.Start("scp -t " + remoteDir); err != nil {
		return fmt.Errorf("scp start: %w", err)
	}

	fmt.Fprintf(stdin, "C0755 %d %s\n", len(data), filename)
	io.Copy(stdin, bytes.NewReader(data))
	fmt.Fprint(stdin, "\x00")
	stdin.Close()

	return sess.Wait()
}
