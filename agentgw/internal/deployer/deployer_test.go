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

func TestPlanDeployStepsWithTokenIncludesConfigUpload(t *testing.T) {
	steps := deployer.PlanStepsWithToken("~/bin", []byte("fake"), "my-secret-token")
	foundConfig := false
	for _, s := range steps {
		if s.Kind == "upload" && strings.Contains(s.Path, "config.json") {
			foundConfig = true
			content := string(s.Data)
			if !strings.Contains(content, "\"token\": \"my-secret-token\"") {
				t.Fatalf("expected config to contain token, got %q", content)
			}
		}
	}
	if !foundConfig {
		t.Fatal("expected config.json upload step when token is provided")
	}
}

func TestPlanDeployStepsWithTokenKillBeforeUpload(t *testing.T) {
	steps := deployer.PlanStepsWithToken("~/bin", []byte("fake"), "my-secret-token")

	// Find indices of key steps
	var killIdx, binUploadIdx, mvIdx, chmodIdx, versionIdx, startIdx int
	haveKill, haveBinUpload, haveMv, haveChmod, haveVersion, haveStart := false, false, false, false, false, false
	for i, s := range steps {
		switch s.Kind {
		case "exec":
			if strings.Contains(s.Command, "pkill") {
				killIdx, haveKill = i, true
			}
			if strings.Contains(s.Command, "chmod +x") {
				chmodIdx, haveChmod = i, true
			}
			if strings.Contains(s.Command, "version") {
				versionIdx, haveVersion = i, true
			}
			if strings.Contains(s.Command, "start") && (strings.Contains(s.Command, "setsid") || strings.Contains(s.Command, "nohup")) {
				startIdx, haveStart = i, true
			}
			if strings.Contains(s.Command, "mv ") && strings.Contains(s.Command, "agentd.new") {
				mvIdx, haveMv = i, true
			}
		case "upload":
			if strings.Contains(s.Path, "agentd.new") {
				binUploadIdx, haveBinUpload = i, true
			}
		}
	}

	if !haveKill {
		t.Fatal("expected pkill step")
	}
	if !haveBinUpload {
		t.Fatal("expected binary upload step")
	}
	if !haveMv {
		t.Fatal("expected mv step for atomic replace")
	}
	if !haveChmod {
		t.Fatal("expected chmod step")
	}
	if !haveVersion {
		t.Fatal("expected version check step")
	}
	if !haveStart {
		t.Fatal("expected start step")
	}

	// kill must come before binary upload
	if killIdx >= binUploadIdx {
		t.Fatalf("pkill (idx %d) must come before binary upload (idx %d)", killIdx, binUploadIdx)
	}
	// binary upload must come before mv
	if binUploadIdx >= mvIdx {
		t.Fatalf("binary upload (idx %d) must come before mv (idx %d)", binUploadIdx, mvIdx)
	}
	// mv must come before chmod
	if mvIdx >= chmodIdx {
		t.Fatalf("mv (idx %d) must come before chmod (idx %d)", mvIdx, chmodIdx)
	}
	// chmod must come before version check
	if chmodIdx >= versionIdx {
		t.Fatalf("chmod (idx %d) must come before version (idx %d)", chmodIdx, versionIdx)
	}
	// version check must come before start
	if versionIdx >= startIdx {
		t.Fatalf("version (idx %d) must come before start (idx %d)", versionIdx, startIdx)
	}
}

func TestPlanDeployStepsWithTokenHasSleepAfterKill(t *testing.T) {
	steps := deployer.PlanStepsWithToken("~/bin", []byte("fake"), "my-secret-token")

	var killIdx, startIdx int
	haveKill, haveStart := false, false
	for i, s := range steps {
		if s.Kind != "exec" {
			continue
		}
		if strings.Contains(s.Command, "pkill") {
			killIdx, haveKill = i, true
		}
		if strings.Contains(s.Command, "start") && (strings.Contains(s.Command, "setsid") || strings.Contains(s.Command, "nohup")) {
			startIdx, haveStart = i, true
		}
	}
	if !haveKill {
		t.Fatal("expected pkill step")
	}
	if !haveStart {
		t.Fatal("expected start step")
	}

	// There must be a sleep step between kill and start
	haveSleep := false
	for i := killIdx + 1; i < startIdx; i++ {
		if steps[i].Kind == "exec" && strings.Contains(steps[i].Command, "sleep") {
			haveSleep = true
			break
		}
	}
	if !haveSleep {
		t.Fatal("expected a sleep step between pkill and start")
	}
}

func TestPlanDeployStepsWithTokenAtomicReplace(t *testing.T) {
	steps := deployer.PlanStepsWithToken("~/bin", []byte("fake"), "my-secret-token")

	var uploadIdx, mvIdx int
	haveUpload, haveMv := false, false
	for i, s := range steps {
		if s.Kind == "upload" && strings.Contains(s.Path, "agentd.new") {
			uploadIdx, haveUpload = i, true
		}
		if s.Kind == "exec" && strings.Contains(s.Command, "mv ") && strings.Contains(s.Command, "agentd.new") {
			mvIdx, haveMv = i, true
		}
	}
	if !haveUpload {
		t.Fatal("expected upload to agentd.new")
	}
	if !haveMv {
		t.Fatal("expected mv step to atomically replace agentd.new -> agentd")
	}
	if uploadIdx >= mvIdx {
		t.Fatalf("upload to agentd.new (idx %d) must come before mv (idx %d)", uploadIdx, mvIdx)
	}
}
