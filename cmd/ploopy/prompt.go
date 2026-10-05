package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/ui"
)

// showPrompt renders what a session would be given. It needs a harness only
// for the shape of the options, so the first one will do.
func showPrompt(cmd *cobra.Command, e *env, p *plan.Plan, u plan.Unit) error {
	skill, err := skillAt(e.root, e.cfg)
	if err != nil {
		return err
	}
	runner, err := loop.New(e.root, loop.Options{
		PlanPath:    p.Path,
		Harness:     e.harnesses[0],
		Skill:       skill,
		BaseContext: e.cfg.Context,
		VerifyCmd:   e.cfg.Verify,
		TestCmd:     e.cfg.Test,
		AuthorPaths: e.cfg.AuthorPaths,
	}, ui.NewReporter(cmd.OutOrStdout(), cmd.ErrOrStderr()))
	if err != nil {
		return err
	}
	prompt, err := runner.Prompt(p, u, "")
	if err != nil {
		return err
	}
	fmt.Print(prompt)
	return nil
}
