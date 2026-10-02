package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/ui"
)

// handoff is a run moved off this checkout: the branch goes to a worktree of
// its own, and this checkout to the default branch.
type handoff struct {
	branch string
	onto   string
	path   string
}

// worktreePath is beside the repository, not inside it, so tools that walk
// the main checkout never find a second copy of it.
func worktreePath(root, branch string) string {
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+".worktrees", branch)
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
