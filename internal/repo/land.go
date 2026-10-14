package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrMoved is a fast-forward the target branch has moved past: it is no
// longer an ancestor of what was to land on it.
var ErrMoved = errors.New("the target branch has moved")

// noEditor keeps a rebase from opening an editor nobody is there to close.
var noEditor = []string{"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"}

// Rebase replays the branch checked out here onto onto. It reports a stop on
// a conflict as stopped, not as an error.
func (r *Repo) Rebase(ctx context.Context, onto string) (stopped bool, err error) {
	return r.rebaseStep(ctx, "rebase", "--quiet", onto)
}

// ContinueRebase goes on with a rebase whose conflicts are resolved and
// staged. A commit the resolution left empty is dropped.
func (r *Repo) ContinueRebase(ctx context.Context) (stopped bool, err error) {
	return r.rebaseStep(ctx, "rebase", "--continue")
}

func (r *Repo) rebaseStep(ctx context.Context, args ...string) (bool, error) {
	_, err := runWith(ctx, r.Root, noEditor, args...)
	if err == nil {
		return false, nil
	}
	if r.Rebasing(ctx) {
		return true, nil
	}
	return false, err
}

// AbortRebase puts the branch back where it was before the rebase.
func (r *Repo) AbortRebase(ctx context.Context) error {
	_, err := r.git(context.WithoutCancel(ctx), "rebase", "--abort")
	return err
}

// Rebasing reports whether a rebase is stopped in this checkout.
func (r *Repo) Rebasing(ctx context.Context) bool {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		out, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-path", name)
		if err != nil {
			continue
		}
		if _, err := os.Stat(strings.TrimSpace(out)); err == nil {
			return true
		}
	}
	return false
}

// Unresolved lists the paths a stopped merge or rebase left in conflict.
func (r *Repo) Unresolved(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// Markers lists the paths that still hold a conflict marker at the start of a
// line.
func (r *Repo) Markers(paths []string) []string {
	var marked []string
	for _, path := range paths {
		text, err := os.ReadFile(filepath.Join(r.Root, path))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(text), "\n") {
			if strings.HasPrefix(line, "<<<<<<< ") || strings.HasPrefix(line, ">>>>>>> ") {
				marked = append(marked, path)
				break
			}
		}
	}
	return marked
}

// Show is a commit's message and the files it touched.
func (r *Repo) Show(ctx context.Context, rev string) string {
	out, err := r.git(ctx, "show", "--stat", "--format=%h %s%n%n%b", rev)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Log is one line per commit in from..to, newest first, at most limit.
func (r *Repo) Log(ctx context.Context, from, to string, limit int) string {
	out, err := r.git(ctx, "log", "--oneline", fmt.Sprintf("--max-count=%d", limit), from+".."+to)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// MergeBase is the newest commit a and b share.
func (r *Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := r.git(ctx, "merge-base", a, b)
	return strings.TrimSpace(out), err
}

// Resolve is the commit a revision names.
func (r *Repo) Resolve(ctx context.Context, rev string) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("no commit %s", rev)
	}
	return strings.TrimSpace(out), nil
}

// FastForward moves onto to branch's commit, in the checkout that has onto
// checked out, or as a bare ref when none has. It refuses anything but a
// fast-forward, and says ErrMoved when onto is no longer an ancestor.
func (r *Repo) FastForward(ctx context.Context, branch, onto string) error {
	tip, err := r.Resolve(ctx, "refs/heads/"+branch)
	if err != nil {
		return err
	}
	old, err := r.Resolve(ctx, "refs/heads/"+onto)
	if err != nil {
		return err
	}
	if !r.IsAncestor(ctx, old, tip) {
		return ErrMoved
	}
	trees, err := r.Worktrees(ctx)
	if err != nil {
		return err
	}
	for _, tree := range trees {
		if tree.Branch == onto {
			_, err := run(ctx, tree.Path, "merge", "--ff-only", "--quiet", tip)
			return err
		}
	}
	_, err = r.git(ctx, "update-ref", "-m", "ploopy land: fast-forward to "+branch, "refs/heads/"+onto, tip, old)
	return err
}

// Upstream is the remote branch checked out here was pushed to under its own
// name, or empty when it never was.
func (r *Repo) Upstream(ctx context.Context, branch string) string {
	remote := r.config(ctx, "branch."+branch+".remote")
	merge := strings.TrimPrefix(r.config(ctx, "branch."+branch+".merge"), "refs/heads/")
	if remote == "" || remote == "." || merge != branch {
		return ""
	}
	return remote
}

// PushRewritten sends a rebased branch to the upstream it already has. The
// lease refuses the push when the remote holds work this checkout never saw.
func (r *Repo) PushRewritten(ctx context.Context, branch string) (string, error) {
	remote := r.Upstream(ctx, branch)
	if remote == "" {
		return "", fmt.Errorf("%s has no upstream of its own name", branch)
	}
	if name, err := r.DefaultBranch(ctx); err == nil && name == branch {
		return "", fmt.Errorf("not pushing %s, the default branch", branch)
	}
	ref := "refs/heads/" + branch
	if _, err := runWith(ctx, r.Root, []string{"GIT_TERMINAL_PROMPT=0"},
		"push", "--quiet", "--force-with-lease="+ref, remote, ref+":"+ref); err != nil {
		return "", err
	}
	return branch + " to " + remote, nil
}

// Switch checks out branch here.
func (r *Repo) Switch(ctx context.Context, branch string) error {
	_, err := r.git(ctx, "switch", "--quiet", branch)
	return err
}

// DeleteBranch deletes a branch git agrees is merged.
func (r *Repo) DeleteBranch(ctx context.Context, branch string) error {
	_, err := r.git(ctx, "branch", "--quiet", "-d", branch)
	return err
}
