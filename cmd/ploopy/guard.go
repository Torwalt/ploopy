package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/harness/claude"
)

// guardCommand is documented for discoverability; it is dispatched before
// cobra runs so it needs no repository.
func guardCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "guard",
		Short:  "PreToolUse hook: refuse the git commands a session must not run",
		Hidden: true,
		RunE: func(*cobra.Command, []string) error {
			os.Exit(runGuard())
			return nil
		},
	}
}

// runGuard answers a PreToolUse hook. Exit 2 blocks the tool call and shows
// the reason to the session.
func runGuard() int {
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 0
	}
	denied, reason := claude.Guard(stdin)
	if !denied {
		return 0
	}
	fmt.Fprintln(os.Stderr, "ploopy refuses this: "+reason)
	return 2
}
