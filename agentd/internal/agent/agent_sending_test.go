package agent_test

import (
	"sync"
	"testing"

	"github.com/phone-talk/agentd/internal/agent"
)

func TestAgent_BeginSendEndSendIsSending(t *testing.T) {
	ag := agent.NewTestAgent("a", "hermes")

	if ag.IsSending() {
		t.Fatalf("new agent should not be sending")
	}

	ag.BeginSend()
	if !ag.IsSending() {
		t.Fatalf("after BeginSend, IsSending should be true")
	}

	ag.EndSend()
	if ag.IsSending() {
		t.Fatalf("after EndSend, IsSending should be false")
	}
}

func TestAgent_SendingFlagIsConcurrencySafe(t *testing.T) {
	ag := agent.NewTestAgent("a", "hermes")

	var wg sync.WaitGroup
	const N = 100
	for i := 0; i < N; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ag.BeginSend()
			ag.EndSend()
		}()
		go func() {
			defer wg.Done()
			_ = ag.IsSending()
		}()
	}
	wg.Wait()
	// final state should be cleared (BeginSend always paired with EndSend above)
	if ag.IsSending() {
		t.Fatalf("expected sending=false after balanced begin/end, got true")
	}
}
