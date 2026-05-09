package main

import (
	"fmt"
	"os"

	"github.com/phone-talk/agentcli/internal/commands"
)

func main() {
	rootCmd := commands.NewRootCmd()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
