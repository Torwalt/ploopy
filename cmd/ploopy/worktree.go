package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

// handoff is a run moved off this checkout: the branch goes to a worktree of
// its own, and this checkout to the default branch.
type handoff struct {
	branch string
	onto   string
	path   string
}

// worktreesDir is beside the repository, not inside it, so tools that walk
// the main checkout never find a second copy of it.
func worktreesDir(root string) string {
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+".worktrees")
}

func worktreePath(root, branch string) string {
	return filepath.Join(worktreesDir(root), branch)
}

// leaveWorktree removes ploopy's worktree once the plan is complete: the
// report is stamped and nothing in it is read again. An open plan keeps it,
// so the next run resumes there with its progress and stats log, and so does
// another open plan beside it, such as the fix plan a review just wrote.
func leaveWorktree(ctx context.Context, e *env, root, branch, planPath string, cfg config.Config, complete bool,
	runner *loop.Loop, report *ui.Reporter) {
	open := plansIn(root, cfg, planPath, true)
	switch {
	case ctx.Err() != nil:
		report.Say("the run was cancelled; the worktree stays at %s", root)
	case !complete:
		report.Say("the worktree stays at %s; the next run resumes there", root)
	case len(open) > 0:
		report.Say("the worktree stays at %s: %s is still open there", root, strings.Join(open, ", "))
	default:
		if err := e.repo().RemoveWorktree(ctx, root, worktreesDir(e.root)); err != nil {
			report.Fail("the worktree stays at %s: %v", root, err)
			runner.Note("worktree", "kept: "+err.Error())
			return
		}
		report.Say("removed the worktree at %s; `git switch %s` picks the branch up here", root, branch)
	}
}

// closeWorktree removes ploopy's worktree for a branch once the last plan in
// it is closed. A worktree the author made is theirs, and one that still holds
// a plan or a run stays.
func closeWorktree(ctx context.Context, e *env, root, branch string, cfg config.Config, keep bool) {
	switch {
	case root != worktreePath(e.root, branch):
		fmt.Printf("that is %s; once %s is done with: git worktree remove %s\n", root, branch, root)
		return
	case keep:
		fmt.Printf("the worktree stays at %s, as asked\n", root)
		return
	}
	if left := plansIn(root, cfg, "", false); len(left) > 0 {
		fmt.Printf("the worktree stays at %s: it still holds %s\n", root, strings.Join(left, ", "))
		return
	}
	if lives, _ := control.List(); len(lives) > 0 {
		for _, live := range lives {
			if live.Root == root {
				fmt.Printf("the worktree stays at %s: %s is running there\n", root, live.Plan)
				return
			}
		}
	}
	if err := e.repo().RemoveWorktree(ctx, root, worktreesDir(e.root)); err != nil {
		fmt.Printf("the worktree stays at %s: %v\n", root, err)
		return
	}
	fmt.Printf("removed the worktree at %s; `git switch %s` picks the branch up here\n", root, branch)
}

// plansIn lists the plans a checkout holds besides skip; with open, only
// those with units still open.
func plansIn(root string, cfg config.Config, skip string, open bool) []string {
	plans, err := plan.Discover(root, cfg.Plans)
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range plans {
		if p.Path == skip {
			continue
		}
		if open {
			s, err := state.Load(filepath.Join(root, state.PathFor(p.Path)))
			if err == nil && loop.Complete(p, s) {
				continue
			}
		}
		out = append(out, p.Path)
	}
	return out
}

// chooseWorktree takes the flag, or asks when the run can move. Only a branch
// other than the default, checked out in the main worktree, can be handed
// over; anywhere else the run stays here.
func chooseWorktree(ctx context.Context, e *env, f *runFlags) (*handoff, error) {
	r := repo.New(e.root, nil)
	branch, err := r.Branch(ctx)
	if err != nil {
		return nil, err
	}
	linked, err := r.Linked(ctx)
	if err != nil {
		return nil, err
	}
	onto, defaultErr := r.DefaultBranch(ctx)

	why := ""
	switch {
	case linked:
		why = "this checkout is already a worktree; run here"
	case branch == "":
		why = "a detached head has no branch to hand over"
	case defaultErr != nil:
		why = defaultErr.Error()
	case branch == onto:
		why = "this checkout is on " + onto + ", and only another branch can be handed over"
	}
	if why != "" {
		if f.worktree {
			return nil, fmt.Errorf("--worktree: %s", why)
		}
		return nil, nil
	}

	if !f.worktree {
		if !ui.Interactive() {
			return nil, nil
		}
		picked, err := ui.Pick("Where?", "worktree", []ui.Choice{
			{Label: "here, on " + branch, Value: "here"},
			{Label: "in a worktree for " + branch + "; this checkout moves to " + onto, Value: "worktree"},
		})
		if err != nil {
			return nil, err
		}
		if picked == "here" {
			return nil, nil
		}
	}
	return &handoff{branch: branch, onto: onto, path: worktreePath(e.root, branch)}, nil
}

// check refuses a hand-off that would leave work behind. A worktree gets only
// what is committed, the author's own paths included.
func (h *handoff) check(ctx context.Context, e *env, p *plan.Plan, allowDirty bool) error {
	if allowDirty {
		return errors.New("--allow-dirty hands this tree's changes to the session, and a worktree gets none of them")
	}
	r := repo.New(e.root, nil)
	dirt, err := r.DirtLines(ctx)
	if err != nil {
		return err
	}
	if len(dirt) > 0 {
		return fmt.Errorf("a worktree gets only what is committed; commit or stash these first:\n%s",
			strings.Join(dirt, "\n"))
	}
	if !r.Committed(ctx, p.Path) {
		return fmt.Errorf("%s is not committed on %s, so a worktree would not have it", p.Path, h.branch)
	}
	if _, err := os.Stat(h.path); err == nil {
		return fmt.Errorf("%s already exists; remove it, or run there", h.path)
	}
	return nil
}
