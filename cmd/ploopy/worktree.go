package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/catalog"
	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

// worktreesDir is beside the repository, not inside it, so tools that walk
// the main checkout never find a second copy of it.
func worktreesDir(root string) string {
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+".worktrees")
}

func worktreePath(root, branch string) string {
	return filepath.Join(worktreesDir(root), branch)
}

// mainRoot is the repository's main checkout, wherever ploopy was started.
func (e *env) mainRoot(ctx context.Context) (string, error) {
	trees, err := e.repo().Worktrees(ctx)
	if err != nil {
		return "", err
	}
	if len(trees) == 0 {
		return "", errors.New("git lists no checkout of this repository")
	}
	return trees[0].Path, nil
}

// ours reports whether root is a worktree ploopy made, and not the checkout
// ploopy was started in, whose shell stands in it.
func (e *env) ours(ctx context.Context, root string) bool {
	main, err := e.mainRoot(ctx)
	if err != nil {
		return false
	}
	return within(root, worktreesDir(main)) && !samePath(root, e.root)
}

func within(path, dir string) bool {
	return strings.HasPrefix(cleanPath(path)+string(filepath.Separator), cleanPath(dir)+string(filepath.Separator))
}

func samePath(a, b string) bool { return cleanPath(a) == cleanPath(b) }

func cleanPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

// place puts a plan where every run goes: a worktree of a branch of its own.
// A worktree that holds the branch already is used as it is. A branch checked
// out nowhere gets one; one checked out in the main checkout moves into one,
// and the main checkout to the default branch. A plan on the default branch
// gets a branch named after it. A plan not yet committed is committed on the
// branch the run uses. It returns the worktree.
func (e *env) place(ctx context.Context, c catalog.Copy) (string, error) {
	r := e.repo()
	trees, err := r.Worktrees(ctx)
	if err != nil {
		return "", err
	}
	if len(trees) == 0 {
		return "", errors.New("git lists no checkout of this repository")
	}
	main := trees[0].Path
	onto, err := r.DefaultBranch(ctx)
	if err != nil {
		return "", err
	}

	branched := c.Branch != "" && c.Branch != onto
	switch {
	case branched && c.Root != "" && !samePath(c.Root, main):
		return c.Root, commitPending(ctx, e, c.Root, c.Plan)
	case branched && c.Root != "":
		return e.handOff(ctx, c, main, onto, trees)
	case branched:
		path, err := e.addWorktree(ctx, main, trees, c.Branch)
		if err != nil {
			return "", err
		}
		fmt.Printf("%s holds %s; checked it out in %s\n", c.Branch, c.Plan.Path, path)
		return path, nil
	}
	return e.branchOff(ctx, c, main, onto, trees)
}

// handOff moves the branch the main checkout has out to a worktree, and the
// main checkout to the default branch. Uncommitted changes go with the branch:
// they belong to it, and a run would not see them in the main checkout.
func (e *env) handOff(ctx context.Context, c catalog.Copy, main, onto string, trees []repo.Worktree) (string, error) {
	if err := commitPending(ctx, e, main, c.Plan); err != nil {
		return "", err
	}
	r := repo.New(main, e.cfg.AuthorPaths)
	dirt, err := r.Dirt(ctx)
	if err != nil {
		return "", err
	}
	path, err := e.freePath(ctx, main, trees, c.Branch)
	if err != nil {
		return "", err
	}

	paths := make([]string, 0, 2*len(dirt))
	for _, change := range dirt {
		if change.From != "" {
			paths = append(paths, change.From)
		}
		paths = append(paths, change.Path)
	}
	if len(paths) > 0 {
		if err := r.StashPaths(ctx, paths); err != nil {
			return "", err
		}
	}
	if err := r.HandOff(ctx, c.Branch, onto, path); err != nil {
		if len(paths) > 0 {
			if popErr := r.StashPop(context.WithoutCancel(ctx)); popErr != nil {
				return "", fmt.Errorf("%w; putting the changes back in %s failed too: %w", err, main, popErr)
			}
		}
		return "", err
	}
	if len(paths) > 0 {
		if err := e.popStashIn(context.WithoutCancel(ctx), path); err != nil {
			if undoErr := e.undoHandOff(ctx, main, c.Branch, path); undoErr != nil {
				return "", fmt.Errorf("%w; undoing the hand-off failed too: %w", err, undoErr)
			}
			return "", err
		}
		fmt.Printf("moved %d uncommitted change(s) with %s\n", len(dirt), c.Branch)
	}
	fmt.Printf("%s is on %s now; %s is checked out in %s\n", main, onto, c.Branch, path)
	return path, nil
}

// popStashIn restores a hand-off's stash in a checkout. Tests replace it to
// make the pop in the new worktree fail.
func (e *env) popStashIn(ctx context.Context, root string) error {
	if e.popStash != nil {
		return e.popStash(ctx, root)
	}
	return repo.New(root, nil).StashPop(ctx)
}

// undoHandOff takes back a hand-off whose stash pop failed in the worktree it
// made: the worktree and whatever the failed pop half-applied there go, the
// main checkout returns to the branch, and the stash is popped there. The
// changes end where they began, in the main checkout, on the branch.
func (e *env) undoHandOff(ctx context.Context, main, branch, path string) error {
	ctx = context.WithoutCancel(ctx)
	r := repo.New(main, e.cfg.AuthorPaths)
	if err := r.DiscardWorktree(ctx, path, worktreesDir(main)); err != nil {
		return fmt.Errorf("removing %s failed: %w", path, err)
	}
	if err := r.Switch(ctx, branch); err != nil {
		return fmt.Errorf("switching %s back to %s failed: %w", main, branch, err)
	}
	if err := r.StashPop(ctx); err != nil {
		return fmt.Errorf("putting the changes back in %s failed: %w", main, err)
	}
	return nil
}

// branchOff gives a plan on the default branch, or on no branch, a branch of
// its own at the commit it is on, named after it. A branch of that name that
// holds the plan already is the one an earlier run made, and is used again.
func (e *env) branchOff(ctx context.Context, c catalog.Copy, main, onto string, trees []repo.Worktree) (string, error) {
	if c.Root == "" {
		return "", fmt.Errorf("%s is on %s but checked out nowhere", c.Plan.Path, onto)
	}
	from := repo.New(c.Root, e.cfg.AuthorPaths)
	base, err := from.Head(ctx)
	if err != nil {
		return "", err
	}
	pending, err := pendingPaths(ctx, from, c.Root, c.Plan)
	if err != nil {
		return "", err
	}

	r := e.repo()
	stem := branchFor(c.Plan)
	branch, path := "", ""
	for n := 1; path == ""; n++ {
		branch = stem
		if n > 1 {
			branch = fmt.Sprintf("%s-%d", stem, n)
		}
		switch {
		case !r.HasBranch(ctx, branch):
			if path, err = e.freePath(ctx, main, trees, branch); err != nil {
				return "", err
			}
			if err := r.NewWorktree(ctx, path, branch, base); err != nil {
				return "", err
			}
			fmt.Printf("%s is on %s; made the branch %s for it, checked out in %s\n", c.Plan.Path, onto, branch, path)
		case r.CommittedAt(ctx, branch, c.Plan.Path):
			path = checkedOutAt(trees, branch)
			if path == "" {
				if path, err = e.addWorktree(ctx, main, trees, branch); err != nil {
					return "", err
				}
			}
			fmt.Printf("%s runs on %s, in %s\n", c.Plan.Path, branch, path)
		}
	}

	if len(pending) == 0 {
		return path, nil
	}
	for _, p := range pending {
		text, err := os.ReadFile(filepath.Join(c.Root, p))
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(path, p)), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(path, p), text, 0o644); err != nil {
			return "", err
		}
	}
	if err := repo.New(path, nil).CommitPaths(ctx, pending, "plans: add "+c.Plan.Name()); err != nil {
		return "", err
	}
	if err := from.Discard(ctx, pending); err != nil {
		return "", fmt.Errorf("%s is committed on %s, but taking it out of %s failed: %w", c.Plan.Path, branch, c.Root, err)
	}
	fmt.Printf("committed %s on %s and took it out of %s\n", strings.Join(pending, ", "), branch, c.Root)
	return path, nil
}

// commitPending commits the plan, and the authority it names, where they are
// not committed yet.
func commitPending(ctx context.Context, e *env, root string, p *plan.Plan) error {
	r := repo.New(root, e.cfg.AuthorPaths)
	pending, err := pendingPaths(ctx, r, root, p)
	if err != nil || len(pending) == 0 {
		return err
	}
	if err := r.CommitPaths(ctx, pending, "plans: add "+p.Name()); err != nil {
		return err
	}
	branch, _ := r.Branch(ctx)
	fmt.Printf("committed %s on %s\n", strings.Join(pending, ", "), branch)
	return nil
}

// pendingPaths is the plan and its authority, where either differs from what
// HEAD has.
func pendingPaths(ctx context.Context, r *repo.Repo, root string, p *plan.Plan) ([]string, error) {
	paths := []string{p.Path}
	if authority := p.Authority(root); authority != "" {
		paths = append(paths, authority)
	}
	changes, err := r.Status(ctx)
	if err != nil {
		return nil, err
	}
	changed := map[string]bool{}
	for _, change := range changes {
		changed[change.Path] = true
	}
	var pending []string
	for _, path := range paths {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			continue
		}
		if changed[filepath.ToSlash(path)] || !r.Committed(ctx, path) {
			pending = append(pending, path)
		}
	}
	return pending, nil
}

var unbranchable = regexp.MustCompile(`[^a-z0-9.-]+`)

// branchFor names the branch a plan runs on: PLAYTEST_FIXES runs on
// playtest-fixes, and a two-file plan on the name of its pass.
func branchFor(p *plan.Plan) string {
	name := strings.ToLower(strings.TrimSuffix(p.Name(), "_EXECUTION"))
	name = strings.Trim(unbranchable.ReplaceAllString(strings.ReplaceAll(name, "_", "-"), "-"), "-.")
	if name == "" {
		return "plan"
	}
	return name
}

func checkedOutAt(trees []repo.Worktree, branch string) string {
	for _, tree := range trees {
		if tree.Branch == branch {
			return tree.Path
		}
	}
	return ""
}

// addWorktree checks a branch out at ploopy's path for it.
func (e *env) addWorktree(ctx context.Context, main string, trees []repo.Worktree, branch string) (string, error) {
	path, err := e.freePath(ctx, main, trees, branch)
	if err != nil {
		return "", err
	}
	if err := repo.New(main, nil).AddWorktree(ctx, path, branch); err != nil {
		return "", err
	}
	return path, nil
}

// freePath is where a new worktree for a branch goes: ploopy's path for it,
// cleared of what an earlier run left there. A worktree of another branch
// sitting there keeps it, and the new one goes beside it.
func (e *env) freePath(ctx context.Context, main string, trees []repo.Worktree, branch string) (string, error) {
	if err := repo.New(main, nil).PruneWorktrees(ctx); err != nil {
		return "", err
	}
	path := worktreePath(main, branch)
	for n := 2; registered(trees, path); n++ {
		path = fmt.Sprintf("%s-%d", worktreePath(main, branch), n)
	}
	return path, clearStale(path)
}

func registered(trees []repo.Worktree, path string) bool {
	for _, tree := range trees {
		if samePath(tree.Path, path) {
			return true
		}
	}
	return false
}

// clearStale empties the way for a new worktree at path. A directory holding
// nothing but ploopy's own is what a removed worktree left behind and goes;
// anything else is moved aside rather than lost.
func clearStale(path string) error {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	leftover := true
	for _, entry := range entries {
		leftover = leftover && entry.Name() == repo.WorkDir
	}
	if leftover {
		fmt.Printf("removed %s, left behind by an earlier worktree\n", path)
		return os.RemoveAll(path)
	}
	aside := path + ".old-" + time.Now().Format("20060102-150405")
	fmt.Printf("%s is no checkout of this repository; moved it aside to %s\n", path, aside)
	return os.Rename(path, aside)
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
		if err := removeWorktree(ctx, e, root); err != nil {
			report.Fail("the worktree stays at %s: %v", root, err)
			runner.Note("worktree", "kept: "+err.Error())
			return
		}
		report.Say("removed the worktree at %s; `git switch %s` picks the branch up here", root, branch)
	}
}

func removeWorktree(ctx context.Context, e *env, root string) error {
	main, err := e.mainRoot(ctx)
	if err != nil {
		return err
	}
	return repo.New(main, nil).RemoveWorktree(ctx, root, worktreesDir(main))
}

// closeWorktree removes ploopy's worktree for a branch once the last plan in
// it is closed. A worktree the author made is theirs, and one that still holds
// a plan or a run stays.
func closeWorktree(ctx context.Context, e *env, root, branch string, cfg config.Config, keep bool) {
	switch {
	case !e.ours(ctx, root):
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
	if err := removeWorktree(ctx, e, root); err != nil {
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

// checkedOut is a plan's copy in a checkout: as it is when one holds it, else
// in a worktree made for its branch.
func (e *env) checkedOut(ctx context.Context, c catalog.Copy) (catalog.Copy, error) {
	if c.Root != "" {
		return c, nil
	}
	trees, err := e.repo().Worktrees(ctx)
	if err != nil {
		return c, err
	}
	if len(trees) == 0 {
		return c, errors.New("git lists no checkout of this repository")
	}
	path, err := e.addWorktree(ctx, trees[0].Path, trees, c.Branch)
	if err != nil {
		return c, err
	}
	fmt.Printf("%s holds %s; checked it out in %s\n", c.Branch, c.Plan.Path, path)
	s, err := state.Load(filepath.Join(path, state.PathFor(c.Plan.Path)))
	if err != nil {
		return c, err
	}
	c.Root, c.State = path, s
	return c, nil
}
