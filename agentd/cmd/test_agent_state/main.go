package main

import (
	"fmt"
	"log"
	"path/filepath"

	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/store"
)

func main() {
	home, _ := filepath.Abs("../../..")
	dbPath := filepath.Join(home, ".agentd", "data", "agents.db")
	s, err := store.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	mgr := agent.NewManager(s, filepath.Join(home, ".agentd", "data"))
	if err := mgr.LoadFromStore(); err != nil {
		log.Fatal(err)
	}

	for _, ag := range mgr.List() {
		state := mgr.DeriveAgentState(ag.ID)
		fmt.Printf("Agent=%s PID=%d Provider=%s AttachMode=%q TmuxTarget=%q SessionControl=%q\n",
			ag.ID[:8], ag.PID, ag.Provider, ag.AttachMode(), ag.TmuxTarget(), state.SessionControl)
	}

	// Also test scan
	procs, err := mgr.ScanExisting()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\n--- Scan Results ---")
	for _, p := range procs {
		if p.Provider == "claude" {
			fmt.Printf("PID=%d TmuxTarget=%q AttachMode=%q\n", p.PID, p.TmuxTarget, p.AttachMode())
		}
	}
}
