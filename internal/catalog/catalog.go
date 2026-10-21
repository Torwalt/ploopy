// Package catalog finds a repository's plans wherever their progress lives:
// this checkout, the repository's other worktrees, the branches not yet
// merged into the default one, and the runs going on.
//
// The default branch says which plans exist: a plan is open while its file is
// there, committed or still uncommitted in a checkout of that branch. A review
// or fix plan exists while the plan it descends from does. Progress lives on
// the branches, so the copy to show is the one that got furthest.
package catalog

import (
	"context"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
)

// Copy is one place a plan lives.
type Copy struct {
	Plan   *plan.Plan
	State  *state.State
	Branch string // empty on a detached head
	Root   string // the checkout holding it; empty for a branch read from git
	Here   bool   // the checkout ploopy was started in
	Live   *control.Live
}

// Done counts the units landed or skipped, of all of them.
func (c Copy) Done() (int, int) {
	done := 0
	for _, u := range c.Plan.Units {
		if status := c.State.Status(u.ID); status == state.Landed || status == state.Skipped {
			done++
		}
	}
	return done, len(c.Plan.Units)
}

// Where says where the copy lives, for a person standing in here.
func (c Copy) Where(here string) string {
	switch {
	case c.Here:
		return "here"
	case c.Root != "":
		if rel, err := filepath.Rel(here, c.Root); err == nil {
			return c.Branch + " in " + rel
		}
		return c.Branch + " in " + c.Root
	}
	return "branch " + c.Branch
}

// latest is when the copy last made progress.
func (c Copy) latest() time.Time {
	var newest time.Time
	for _, entry := range c.State.Units {
		if at, err := time.Parse(time.RFC3339, entry.Date); err == nil && at.After(newest) {
			newest = at
		}
	}
	return newest
}

// Entry is a plan and every place it lives.
type Entry struct {
	Best   Copy   // the copy to show and to run
	Others []Copy // the rest, in the order they were found
}

// Path is the plan's path within the repository.
func (e Entry) Path() string { return e.Best.Plan.Path }

// Find lists the plans under plansDir across the repository at root, in path
// order.
func Find(ctx context.Context, root, plansDir string) ([]Entry, error) {
	r := repo.New(root, nil)
	here := clean(root)
	onto, _ := r.DefaultBranch(ctx)

	trees, err := r.Worktrees(ctx)
	if err != nil {
		return nil, err
	}
	var copies []Copy
	checkedOut := map[string]bool{}
	for _, tree := range trees {
		checkedOut[tree.Branch] = true
		plans, err := plan.Discover(tree.Path, plansDir)
		if err != nil {
			continue // a worktree whose directory is gone
		}
		for _, p := range plans {
			s, err := state.Load(filepath.Join(tree.Path, state.PathFor(p.Path)))
			if err != nil {
				return nil, err
			}
			copies = append(copies, Copy{
				Plan: p, State: s, Branch: tree.Branch, Root: tree.Path, Here: clean(tree.Path) == here,
			})
		}
	}

	onBranches, err := branchCopies(ctx, r, plansDir, onto, checkedOut)
	if err != nil {
		return nil, err
	}
	copies = append(copies, onBranches...)
	markLive(copies)
	copies = withoutDetached(copies)
	if _, err := r.Resolve(ctx, "refs/heads/"+onto); onto != "" && err == nil {
		if copies, err = registered(ctx, r, plansDir, onto, copies); err != nil {
			return nil, err
		}
	}
	return group(copies, onto), nil
}

// withoutDetached leaves out the checkouts on no branch, which builds and
// bisects make at old commits: progress belongs to a branch. A run going on
// in one, or the checkout ploopy was started in, still counts.
func withoutDetached(copies []Copy) []Copy {
	var out []Copy
	for _, c := range copies {
		if c.Root != "" && c.Branch == "" && c.Live == nil && !c.Here {
			continue
		}
		out = append(out, c)
	}
	return out
}

// registered keeps the copies of the plans the default branch holds, and of
// the review and fix plans descending from them. A landed or dropped plan is
// gone from it, so whatever stale branch still holds a copy is left out. A run
// going on is never left out, and neither is a checkout on no branch that
// ploopy was started in.
func registered(ctx context.Context, r *repo.Repo, plansDir, onto string, copies []Copy) ([]Copy, error) {
	tip := "refs/heads/" + onto
	files, err := r.FilesAt(ctx, []string{tip}, plansDir, func(name string) bool { return strings.HasSuffix(name, ".md") })
	if err != nil {
		return nil, err
	}
	exists := map[string]bool{}
	for p := range files[tip] {
		exists[key(p)] = true
	}
	for _, c := range copies {
		if c.Root != "" && c.Branch == onto {
			exists[key(c.Plan.Path)] = true // written, not yet committed
		}
	}
	var out []Copy
	for _, c := range copies {
		if c.Live != nil || (c.Here && c.Branch == "") || exists[key(review.Family(c.Plan.Path))] {
			out = append(out, c)
		}
	}
	return out, nil
}

func key(p string) string { return path.Clean(filepath.ToSlash(p)) }

// branchCopies reads the plans of every branch not checked out anywhere and
// not yet merged into the default branch, and of the default branch itself
// when no checkout has it.
func branchCopies(ctx context.Context, r *repo.Repo, plansDir, into string, checkedOut map[string]bool) ([]Copy, error) {
	refs, err := r.Unmerged(ctx, into)
	if err != nil {
		return nil, err
	}
	if into != "" && !checkedOut[into] {
		if commit, err := r.Resolve(ctx, "refs/heads/"+into); err == nil {
			refs = append(refs, repo.Ref{Branch: into, Commit: commit})
		}
	}
	var commits []string
	var branches []repo.Ref
	for _, ref := range refs {
		if !checkedOut[ref.Branch] {
			commits = append(commits, ref.Commit)
			branches = append(branches, ref)
		}
	}
	keep := func(name string) bool { return strings.HasSuffix(name, ".md") }
	files, err := r.FilesAt(ctx, commits, plansDir, func(name string) bool {
		return keep(name) || strings.HasSuffix(name, ".state.json")
	})
	if err != nil {
		return nil, err
	}

	var copies []Copy
	for _, ref := range branches {
		tree := files[ref.Commit]
		var parsed []*plan.Plan
		for path, content := range tree {
			if keep(path) {
				parsed = append(parsed, plan.Parse(string(content), path))
			}
		}
		exists := func(path string) bool { _, ok := tree[filepath.ToSlash(path)]; return ok }
		for _, p := range plan.Runnable(parsed, exists) {
			s := &state.State{Path: state.PathFor(p.Path), Units: map[string]*state.Entry{}}
			if raw, ok := tree[filepath.ToSlash(state.PathFor(p.Path))]; ok {
				if s, err = state.Parse(raw, state.PathFor(p.Path)); err != nil {
					continue // a branch with a broken state file shows nothing rather than stopping status
				}
			}
			copies = append(copies, Copy{Plan: p, State: s, Branch: ref.Branch})
		}
	}
	return copies, nil
}

// markLive ties each run going on to the copy it runs.
func markLive(copies []Copy) {
	lives, err := control.List()
	if err != nil {
		return
	}
	for i := range lives {
		for j := range copies {
			if copies[j].Root != "" && clean(copies[j].Root) == clean(lives[i].Root) && copies[j].Plan.Path == lives[i].Plan {
				copies[j].Live = &lives[i]
			}
		}
	}
}

// group gathers the copies of each plan and picks the one to show: a run
// going on, else the one with the most units done, else the newest progress,
// else the default branch's, where a plan starts, else this checkout's.
func group(copies []Copy, onto string) []Entry {
	byPath := map[string]*Entry{}
	var order []string
	for _, c := range copies {
		entry, seen := byPath[c.Plan.Path]
		if !seen {
			byPath[c.Plan.Path] = &Entry{Best: c}
			order = append(order, c.Plan.Path)
			continue
		}
		if better(c, entry.Best, onto) {
			entry.Others = append(entry.Others, entry.Best)
			entry.Best = c
		} else {
			entry.Others = append(entry.Others, c)
		}
	}
	sortStrings(order)
	out := make([]Entry, 0, len(order))
	for _, path := range order {
		out = append(out, *byPath[path])
	}
	return out
}

func better(a, b Copy, onto string) bool {
	if (a.Live != nil) != (b.Live != nil) {
		return a.Live != nil
	}
	ad, _ := a.Done()
	bd, _ := b.Done()
	if ad != bd {
		return ad > bd
	}
	if at, bt := a.latest(), b.latest(); !at.Equal(bt) {
		return at.After(bt)
	}
	if (a.Branch == onto) != (b.Branch == onto) {
		return a.Branch == onto
	}
	return a.Here && !b.Here
}

func clean(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
