package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

func newClose(e *env) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "close [PLAN]",
		Short: "Delete a finished plan and its state file, with its report in the commit",
		Long: "Delete a finished plan and its state file in one commit, on the branch\n" +
			"and in the checkout that hold them, with the plan's report as the\n" +
			"commit's body. Nothing else that is staged goes into that commit. The\n" +
			"checkout's .ploopy/ is left alone.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return closePlan(cmd.Context(), e, args, force) },
	}
	cmd.Flags().BoolVar(&force, "force", false, "close a plan with units still open")
	return cmd
}

func closePlan(ctx context.Context, e *env, args []string, force bool) error {
	c, err := e.planArg(ctx, args, "Close which plan?", false)
	if err != nil {
		return err
	}
	if c.Live != nil {
		return fmt.Errorf("%s is running in %s; stop it first with `ploopy adjust --stop`", c.Plan.Path, c.Live.Root)
	}
	if c.Root == "" {
		return fmt.Errorf("%s is only on branch %s; check it out to close it", c.Plan.Path, c.Branch)
	}
	if done, total := c.Done(); done < total && !force {
		if !ui.Interactive() {
			return fmt.Errorf("%s has %d of %d units still open; --force closes it anyway", c.Plan.Path, total-done, total)
		}
		picked, err := ui.Pick(fmt.Sprintf("%s has %d of %d units still open. Close it anyway?", c.Plan.Path, total-done, total),
			"force", []ui.Choice{{Label: "no, leave it open", Value: "no"}, {Label: "yes, close it", Value: "yes"}})
		if err != nil {
			return err
		}
		if picked != "yes" {
			fmt.Printf("%s stays open\n", c.Plan.Path)
			return nil
		}
	}

	cfg, err := config.Load(c.Root)
	if err != nil {
		return err
	}
	r := repo.New(c.Root, cfg.AuthorPaths)
	paths := []string{c.Plan.Path}
	if statePath := state.PathFor(c.Plan.Path); r.Committed(ctx, statePath) {
		paths = append(paths, statePath)
	}
	// The report is read before the files it reads from go.
	text := strings.TrimRight(reportOf(c), "\n")
	if err := r.RemoveAndCommit(ctx, paths, fmt.Sprintf(cfg.CloseCommit, c.Plan.Name())+"\n\n"+text); err != nil {
		return err
	}

	fmt.Printf("%s\n\nclosed %s in a commit on %s\n", text, c.Plan.Path, c.Branch)
	if !c.Here {
		fmt.Printf("that is %s; once %s is done with: git worktree remove %s\n", c.Where(e.root), c.Branch, c.Root)
	}
	return nil
}
