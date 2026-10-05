package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/catalog"
	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/ui"
)

// showPrompt renders what a session would be given. It needs a harness only
// for the shape of the options, so the first one will do.
func showPrompt(cmd *cobra.Command, e *env, c catalog.Copy, u plan.Unit) error {
	if c.Root == "" {
		return fmt.Errorf("%s is only on branch %s; check it out to see a whole prompt", c.Plan.Path, c.Branch)
	}
	root := c.Root
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	skill, err := skillAt(root, cfg)
	if err != nil {
		return err
	}
	p := c.Plan
	runner, err := loop.New(root, loop.Options{
		PlanPath:    p.Path,
		Harness:     e.harnesses[0],
		Skill:       skill,
		BaseContext: cfg.Context,
		VerifyCmd:   cfg.Verify,
		TestCmd:     cfg.Test,
		AuthorPaths: cfg.AuthorPaths,
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
