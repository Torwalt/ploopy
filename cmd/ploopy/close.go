package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/catalog"
	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

func newClose(e *env) *cobra.Command {
	var force, keep bool
	cmd := &cobra.Command{
		Use:   "close [PLAN]",
		Short: "Drop a plan without landing it, or close a review or fix plan",
		Long: "Delete a plan and its state file in one commit carrying the plan's\n" +
			"report. Nothing else that is staged goes into that commit.\n\n" +
			"A plan exists while the default branch holds it. Closing one drops it\n" +
			"there, in the main checkout, which must be on the default branch; its\n" +
			"branch and worktree are left as they are. `ploopy land` is how a\n" +
			"finished plan's work reaches the default branch, and it closes the plan\n" +
			"as it does.\n\n" +
			"A review or fix plan is closed on its branch, in the checkout that\n" +
			"holds it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return closePlan(cmd.Context(), e, args, force) },
	}
	cmd.Flags().BoolVar(&force, "force", false, "close a plan with units still open")
	cmd.Flags().BoolVar(&keep, "keep-worktree", false, "")
	_ = cmd.Flags().MarkDeprecated("keep-worktree", "close leaves every worktree alone")
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
	if review.Family(c.Plan.Path) == c.Plan.Path {
		return dropPlan(ctx, e, c)
	}

	if c, err = e.checkedOut(ctx, c); err != nil {
		return err
	}
	cfg, err := config.Load(c.Root)
	if err != nil {
		return err
	}
	text, err := closeCommit(ctx, c, cfg)
	if err != nil {
		return err
	}
	fmt.Printf("%s\n\nclosed %s in a commit on %s\n", text, c.Plan.Path, c.Branch)
	return nil
}

// dropPlan deletes a plan on the default branch, where it exists. Its report
// is read from the copy that got furthest, wherever that is.
func dropPlan(ctx context.Context, e *env, c catalog.Copy) error {
	r := e.repo()
	onto, err := r.DefaultBranch(ctx)
	if err != nil {
		return err
	}
	trees, err := r.Worktrees(ctx)
	if err != nil {
		return err
	}
	if len(trees) == 0 {
		return fmt.Errorf("git lists no checkout of this repository")
	}
	main := trees[0]
	if main.Branch != onto {
		return fmt.Errorf("%s is not on %s; a plan is closed there, so `git switch %s` first", main.Path, onto, onto)
	}
	cfg, err := config.Load(main.Path)
	if err != nil {
		return err
	}
	at := repo.New(main.Path, cfg.AuthorPaths)
	if !at.Committed(ctx, c.Plan.Path) {
		return fmt.Errorf("%s is not committed on %s; delete the file instead", c.Plan.Path, onto)
	}
	paths := []string{c.Plan.Path}
	if statePath := state.PathFor(c.Plan.Path); at.Committed(ctx, statePath) {
		paths = append(paths, statePath)
	}
	text := strings.TrimRight(reportOf(c), "\n")
	if err := at.RemoveAndCommit(ctx, paths, fmt.Sprintf(cfg.CloseCommit, c.Plan.Name())+"\n\n"+text); err != nil {
		return err
	}
	fmt.Printf("%s\n\nclosed %s in a commit on %s\n", text, c.Plan.Path, onto)
	if branch := c.Branch; branch != "" && branch != onto {
		fmt.Printf("%s is left as it is: `ploopy land %s` lands its work, `git branch -D %s` drops it\n", branch, branch, branch)
	}
	return nil
}

// closeCommit deletes a plan and its state file in one commit carrying its
// report, and returns the report.
func closeCommit(ctx context.Context, c catalog.Copy, cfg config.Config) (string, error) {
	r := repo.New(c.Root, cfg.AuthorPaths)
	paths := []string{c.Plan.Path}
	if statePath := state.PathFor(c.Plan.Path); r.Committed(ctx, statePath) {
		paths = append(paths, statePath)
	}
	// The report is read before the files it reads from go.
	text := strings.TrimRight(reportOf(c), "\n")
	if err := r.RemoveAndCommit(ctx, paths, fmt.Sprintf(cfg.CloseCommit, c.Plan.Name())+"\n\n"+text); err != nil {
		return "", err
	}
	return text, nil
}
