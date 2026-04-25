package main

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/phone-talk/agentd/internal/scanner"
)

func main() {
	s := scanner.New()
	procs, err := s.Scan()
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range procs {
		if p.Provider == "claude" {
			fmt.Printf("PID=%d TTY=%q TmuxTarget=%q Session=%q AttachMode=%q\n",
				p.PID, p.Terminal, p.TmuxTarget, p.Session, p.AttachMode())
		}
	}

	data, _ := json.MarshalIndent(procs, "", "  ")
	fmt.Printf("\nAll processes:\n%s\n", data)
}
