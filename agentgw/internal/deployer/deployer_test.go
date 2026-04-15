package deployer_test

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/phone-talk/agentgw/internal/deployer"
)

func TestHashBinary(t *testing.T) {
	content := []byte("fake binary content for testing")
	h := deployer.HashBinary(content)
	expected := fmt.Sprintf("%x", sha256.Sum256(content))
	if h != expected {
		t.Errorf("expected hash %q, got %q", expected, h)
	}
}

func TestPlanDeploySteps(t *testing.T) {
	content := []byte("fake agentd binary")
	steps := deployer.PlanSteps("~/.agentd", content)

	if len(steps) == 0 {
		t.Fatal("expected non-empty deploy steps")
	}
	found := map[string]bool{}
	for _, s := range steps {
		found[s.Kind] = true
	}
	if !found["mkdir"] {
		t.Error("expected mkdir step")
	}
	if !found["upload"] {
		t.Error("expected upload step")
	}
	if !found["exec"] {
		t.Error("expected exec step")
	}
}

func TestPlanDeployStepsStartsDetached(t *testing.T) {
	steps := deployer.PlanSteps("~/bin", []byte("fake"))
	if len(steps) == 0 {
		t.Fatal("expected deploy steps")
	}
	startCmd := steps[len(steps)-1].Command
	for _, want := range []string{
		"setsid",
		"nohup",
		"\"$HOME/bin/agentd\" start",
		">/tmp/agentd.log 2>&1 < /dev/null &",
	} {
		if !strings.Contains(startCmd, want) {
			t.Fatalf("expected start command to contain %q, got %q", want, startCmd)
		}
	}
}
