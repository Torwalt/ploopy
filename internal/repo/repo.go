// Package repo answers what git knows. Whether a unit landed is read from
// here, never from a session's account of itself.
package repo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WorkDir is ploopy's own directory inside the repository: prompts, session
// logs, handovers and local progress. It is never the session's dirt and never
// a unit's work, whether or not the repository gitignores it.
const WorkDir = ".ploopy"

// Repo is one git repository and the author's paths within it.
type Repo struct {
	Root        string
	AuthorPaths []string
}

// New opens a repository at root.
func New(root string, authorPaths []string) *Repo {
	return &Repo{Root: root, AuthorPaths: authorPaths}
}

// Root finds the repository containing dir.
func Root(ctx context.Context, dir string) (string, error) {
	out, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository")
	}
	return strings.TrimSpace(out), nil
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	return runWith(ctx, dir, nil, args...)
}

func runWith(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	// A credential helper or ssh left behind by a killed git must not hold the
	// output open forever.
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s failed: %s",
			strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (r *Repo) git(ctx context.Context, args ...string) (string, error) {
	return run(ctx, r.Root, args...)
}

// Head is the current commit.
func (r *Repo) Head(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// Branch is the branch checked out here, or empty on a detached head.
func (r *Repo) Branch(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "branch", "--show-current")
	return strings.TrimSpace(out), err
}

// DefaultBranch is the branch work is integrated into: the one origin's HEAD
// names, else master, else main.
func (r *Repo) DefaultBranch(ctx context.Context) (string, error) {
	if out, err := r.git(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(out), "origin/"), nil
	}
	for _, name := range []string{"master", "main"} {
		for _, ref := range []string{"refs/heads/" + name, "refs/remotes/origin/" + name} {
			if _, err := r.git(ctx, "rev-parse", "--verify", "--quiet", ref); err == nil {
				return name, nil
			}
		}
	}
	return "", errors.New("no default branch: origin/HEAD is unset and there is no master or main")
}

// Linked reports whether this checkout is a linked worktree rather than the
// repository's main one.
func (r *Repo) Linked(ctx context.Context) (bool, error) {
	out, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return false, err
	}
	dirs := strings.Split(strings.TrimSpace(out), "\n")
	if len(dirs) != 2 {
		return false, fmt.Errorf("git rev-parse named %d directories, not 2", len(dirs))
	}
	return dirs[0] != dirs[1], nil
}

// Committed reports whether path is in the commit at HEAD.
func (r *Repo) Committed(ctx context.Context, path string) bool {
	_, err := r.git(ctx, "cat-file", "-e", "HEAD:"+filepath.ToSlash(path))
	return err == nil
}

// HandOff moves this checkout onto another branch and checks branch out in a
// new worktree at path. When the worktree cannot be made, the checkout goes
// back to branch.
func (r *Repo) HandOff(ctx context.Context, branch, onto, path string) error {
	// Interrupted halfway, the branch would be checked out nowhere.
	ctx = context.WithoutCancel(ctx)
	if _, err := r.git(ctx, "switch", "--quiet", onto); err != nil {
		return err
	}
	if _, err := r.git(ctx, "worktree", "add", "--quiet", path, branch); err != nil {
		if _, back := r.git(ctx, "switch", "--quiet", branch); back != nil {
			return fmt.Errorf("%w; switching back to %s failed too: %w", err, branch, back)
		}
		return err
	}
	return nil
}

// Change is one entry of the working tree's status.
type Change struct {
	Status string // the two porcelain columns
	Path   string // the new path, for a rename
	From   string // the original path, for a rename
}

// Line renders a change the way `git status --porcelain` would.
func (c Change) Line() string {
	if c.From != "" {
		return c.Status + " " + c.From + " -> " + c.Path
	}
	return c.Status + " " + c.Path
}

// Status is every change in the working tree, the author's included.
// Untracked files are listed one by one rather than collapsed to their
// directory, so an author path inside a new directory is still recognised.
func (r *Repo) Status(ctx context.Context) ([]Change, error) {
	out, err := r.git(ctx, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	fields := strings.Split(out, "\x00")

	var changes []Change
	for i := 0; i < len(fields); i++ {
		record := fields[i]
		if len(record) < 4 {
			continue
		}
		change := Change{Status: record[:2], Path: record[3:]}
		// A rename or copy carries its original path as the next field.
		if strings.ContainsAny(change.Status, "RC") && i+1 < len(fields) {
			i++
			change.From = fields[i]
		}
		changes = append(changes, change)
	}
	return changes, nil
}

// Dirt is the working tree's changes that are not the author's.
func (r *Repo) Dirt(ctx context.Context) ([]Change, error) {
	changes, err := r.Status(ctx)
	if err != nil {
		return nil, err
	}
	var dirt []Change
	for _, change := range changes {
		if !r.excluded(change.Path) {
			dirt = append(dirt, change)
		}
	}
	return dirt, nil
}

// DirtLines renders Dirt for a prompt or a log.
func (r *Repo) DirtLines(ctx context.Context) ([]string, error) {
	dirt, err := r.Dirt(ctx)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(dirt))
	for _, change := range dirt {
		lines = append(lines, change.Line())
	}
	return lines, nil
}

// IsAuthors reports whether a path belongs to the author rather than to the
// session. Patterns are gitignore-shaped: a trailing or implied directory
// matches everything under it, and a pattern without a slash matches a base
// name anywhere.
func (r *Repo) IsAuthors(path string) bool {
	for _, pattern := range r.AuthorPaths {
		if matches(pattern, path) {
			return true
		}
	}
	return false
}

// excluded is everything that is not the session's doing: ploopy's own
// directory, and the paths the author reserved.
func (r *Repo) excluded(path string) bool {
	if path == WorkDir || strings.HasPrefix(path, WorkDir+"/") {
		return true
	}
	return r.IsAuthors(path)
}

func matches(pattern, path string) bool {
	pattern = strings.TrimSuffix(pattern, "/")
	if pattern == "" {
		return false
	}
	if pattern == path || strings.HasPrefix(path, pattern+"/") {
		return true
	}
	if ok, err := filepath.Match(pattern, path); err == nil && ok {
		return true
	}
	if !strings.Contains(pattern, "/") {
		if ok, err := filepath.Match(pattern, filepath.Base(path)); err == nil && ok {
			return true
		}
	}
	return false
}

// Commit is one commit made since a unit started.
type Commit struct {
	Hash    string
	Subject string
}

// Short renders the commit the way the loop reports it.
func (c Commit) Short() string {
	hash := c.Hash
	if len(hash) > 10 {
		hash = hash[:10]
	}
	return hash + " " + c.Subject
}

// Commits lists what landed since base, leaving out commits that touch only
// the author's paths.
func (r *Repo) Commits(ctx context.Context, base string) ([]Commit, error) {
	out, err := r.git(ctx, "log", "--reverse", "--name-only", "--format=%x00%H %s", base+"..HEAD")
	if err != nil {
		return nil, err
	}

	var commits []Commit
	for _, record := range strings.Split(out, "\x00")[1:] {
		var lines []string
		for _, line := range strings.Split(record, "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
		if len(lines) == 0 {
			continue
		}
		head, paths := lines[0], lines[1:]
		if len(paths) > 0 && r.allExcluded(paths) {
			continue
		}
		hash, subject, _ := strings.Cut(head, " ")
		commits = append(commits, Commit{Hash: hash, Subject: subject})
	}
	return commits, nil
}

func (r *Repo) allExcluded(paths []string) bool {
	for _, path := range paths {
		if !r.excluded(path) {
			return false
		}
	}
	return true
}

// Contains reports whether base is an ancestor of HEAD, which tells the loop
// whether a remembered base still applies.
func (r *Repo) Contains(ctx context.Context, base string) bool {
	cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", base, "HEAD")
	cmd.Dir = r.Root
	return cmd.Run() == nil
}

// CommitOnly commits one path and nothing else.
func (r *Repo) CommitOnly(ctx context.Context, path, message string) error {
	if _, err := r.git(ctx, "add", "--", path); err != nil {
		return err
	}
	_, err := r.git(ctx, "commit", "-q", "-m", message, "--", path)
	return err
}

// Push sends the branch checked out here to its remote, and says where it went.
// A branch whose upstream has its own name goes there; any other goes to origin,
// or to the only remote, and the upstream is set. It never forces, and never
// pushes the default branch. Nothing may prompt: nobody is there to answer.
func (r *Repo) Push(ctx context.Context) (string, error) {
	branch, remote, track, err := r.pushTarget(ctx)
	if err != nil {
		return "", err
	}

	args := []string{"push", "--quiet"}
	if track {
		args = append(args, "--set-upstream")
	}
	args = append(args, remote, "refs/heads/"+branch+":refs/heads/"+branch)
	if _, err := runWith(ctx, r.Root, []string{"GIT_TERMINAL_PROMPT=0"}, args...); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("pushing %s to %s timed out", branch, remote)
		}
		return "", err
	}
	where := branch + " to " + remote
	if track {
		where += ", upstream set"
	}
	return where, nil
}

// CanPush says why the branch checked out here cannot be pushed, or nil.
func (r *Repo) CanPush(ctx context.Context) error {
	_, _, _, err := r.pushTarget(ctx)
	return err
}

// pushTarget is the branch to push, where it goes, and whether its upstream is
// to be set.
func (r *Repo) pushTarget(ctx context.Context) (branch, remote string, track bool, err error) {
	if branch, err = r.Branch(ctx); err != nil {
		return "", "", false, err
	}
	if branch == "" {
		return "", "", false, errors.New("a detached head has no branch to push")
	}
	if name, err := r.DefaultBranch(ctx); err == nil && name == branch {
		return "", "", false, fmt.Errorf("not pushing %s, the default branch", branch)
	}

	remote = r.config(ctx, "branch."+branch+".remote")
	upstream := strings.TrimPrefix(r.config(ctx, "branch."+branch+".merge"), "refs/heads/")
	track = remote == "" || remote == "." || upstream != branch
	if track {
		if remote, err = r.pushRemote(ctx, branch); err != nil {
			return "", "", false, err
		}
	}
	return branch, remote, track, nil
}

// CommonDir is the repository's own git directory, shared by all its
// worktrees.
func (r *Repo) CommonDir(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	return strings.TrimSpace(out), err
}

// pushRemote is where a branch without a matching upstream goes.
func (r *Repo) pushRemote(ctx context.Context, branch string) (string, error) {
	for _, key := range []string{"branch." + branch + ".pushRemote", "remote.pushDefault"} {
		if remote := r.config(ctx, key); remote != "" {
			return remote, nil
		}
	}
	out, err := r.git(ctx, "remote")
	if err != nil {
		return "", err
	}
	remotes := strings.Fields(out)
	for _, remote := range remotes {
		if remote == "origin" {
			return remote, nil
		}
	}
	switch len(remotes) {
	case 0:
		return "", errors.New("the repository has no remote to push to")
	case 1:
		return remotes[0], nil
	}
	return "", fmt.Errorf("no origin among %s; set remote.pushDefault", strings.Join(remotes, ", "))
}

// config is one git setting, or empty when it is unset.
func (r *Repo) config(ctx context.Context, key string) string {
	out, err := r.git(ctx, "config", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
