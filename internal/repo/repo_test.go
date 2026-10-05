package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "work"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write(t, root, "README.md", "start\n")
	commit(t, root, "first")
	return root
}

func write(t *testing.T, root, path, text string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func commit(t *testing.T, root, message string) {
	t.Helper()
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", message)
}

func TestDirtExcludesTheAuthorsPaths(t *testing.T) {
	root := newRepo(t)
	write(t, root, "NOTES.md", "the author is thinking\n")
	write(t, root, "src/main.go", "package main\n")

	r := New(root, []string{"NOTES.md"})
	dirt, err := r.DirtLines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(dirt, "\n")
	if strings.Contains(joined, "NOTES.md") {
		t.Fatalf("the author's file was counted as dirt: %q", joined)
	}
	if !strings.Contains(joined, "src/main.go") {
		t.Fatalf("the session's file is missing from the dirt: %q", joined)
	}
}

func TestAuthorPatternsMatchDirectoriesAndBaseNames(t *testing.T) {
	r := New("/nowhere", []string{"docs/notes", "*.local.md", "TODO.md"})
	for _, path := range []string{"docs/notes/one.md", "docs/notes", "x.local.md", "TODO.md"} {
		if !r.IsAuthors(path) {
			t.Fatalf("%q should belong to the author", path)
		}
	}
	for _, path := range []string{"docs/plans/PASS.md", "src/main.go", "TODO.md.bak"} {
		if r.IsAuthors(path) {
			t.Fatalf("%q should not belong to the author", path)
		}
	}
}

// Status is read with -z, so a rename and a path with a space survive it.
func TestARenameAndASpacedPathAreClassified(t *testing.T) {
	root := newRepo(t)
	write(t, root, "with space.md", "x\n")
	commit(t, root, "add a spaced path")
	git(t, root, "mv", "README.md", "READZZZ.md")
	write(t, root, "with space.md", "changed\n")

	changes, err := New(root, nil).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, change := range changes {
		paths = append(paths, change.Path)
	}
	for _, want := range []string{"READZZZ.md", "with space.md"} {
		if !containsString(paths, want) {
			t.Fatalf("status lost %q: %v", want, paths)
		}
	}
	for _, change := range changes {
		if change.Path == "READZZZ.md" && change.From != "README.md" {
			t.Fatalf("the rename lost its original path: %+v", change)
		}
	}
}

func TestARenamedAuthorFileStaysTheAuthors(t *testing.T) {
	root := newRepo(t)
	write(t, root, "NOTES.md", "x\n")
	commit(t, root, "add notes")
	git(t, root, "mv", "NOTES.md", "NOTES-renamed.md")

	dirt, err := New(root, []string{"NOTES-renamed.md"}).DirtLines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dirt) != 0 {
		t.Fatalf("the author's rename was counted as dirt: %v", dirt)
	}
}

func TestCommitsSinceABaseLeaveOutTheAuthors(t *testing.T) {
	root := newRepo(t)
	ctx := context.Background()
	r := New(root, []string{"NOTES.md"})

	base, err := r.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "src/main.go", "package main\n")
	commit(t, root, "the unit's work")
	write(t, root, "NOTES.md", "the author's thought\n")
	commit(t, root, "the author's note")

	commits, err := r.Commits(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0].Subject != "the unit's work" {
		t.Fatalf("commits %+v", commits)
	}
}

func TestContainsKnowsWhetherABaseStillApplies(t *testing.T) {
	root := newRepo(t)
	ctx := context.Background()
	r := New(root, nil)

	base, _ := r.Head(ctx)
	if !r.Contains(ctx, base) {
		t.Fatal("HEAD should contain itself")
	}
	if r.Contains(ctx, "0000000000000000000000000000000000000000") {
		t.Fatal("an unknown commit is not an ancestor")
	}
}

func TestCommitOnlyLeavesEverythingElseAlone(t *testing.T) {
	root := newRepo(t)
	ctx := context.Background()
	write(t, root, "state.json", "{}\n")
	write(t, root, "other.go", "package other\n")

	if err := New(root, nil).CommitOnly(ctx, "state.json", "record progress"); err != nil {
		t.Fatal(err)
	}
	status := git(t, root, "status", "--porcelain")
	if !strings.Contains(status, "other.go") {
		t.Fatalf("the other file was swept into the commit: %q", status)
	}
	if strings.Contains(status, "state.json") {
		t.Fatalf("the state file was not committed: %q", status)
	}
}

// failingHooks installs pre-commit and commit-msg hooks that refuse every
// commit, the way a hook whose config is missing does.
func failingHooks(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"pre-commit", "commit-msg"} {
		hook := filepath.Join(root, ".git", "hooks", name)
		if err := os.WriteFile(hook, []byte("#!/bin/sh\necho refused >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPloopysCommitsSkipTheRepositorysHooks(t *testing.T) {
	root := newRepo(t)
	ctx := context.Background()
	write(t, root, "docs/plans/A.md", "# A\n")
	write(t, root, "docs/plans/A.state.json", "{}\n")
	commit(t, root, "plan")
	failingHooks(t, root)
	r := New(root, nil)

	write(t, root, "docs/plans/A.state.json", "{\"units\": {}}\n")
	if err := r.CommitOnly(ctx, "docs/plans/A.state.json", "plans: record A progress"); err != nil {
		t.Fatalf("the state commit ran the hooks: %v", err)
	}
	if err := r.CommitEmpty(ctx, "ploopy report: A"); err != nil {
		t.Fatalf("the report commit ran the hooks: %v", err)
	}
	if err := r.RemoveAndCommit(ctx, []string{"docs/plans/A.md", "docs/plans/A.state.json"}, "plans: close A"); err != nil {
		t.Fatalf("the close commit ran the hooks: %v", err)
	}
	if log := git(t, root, "log", "--format=%s", "-3"); log != "plans: close A\nploopy report: A\nplans: record A progress\n" {
		t.Fatalf("the commits are:\n%s", log)
	}
}

func TestRootFindsTheRepository(t *testing.T) {
	root := newRepo(t)
	write(t, root, "deep/inside/file.txt", "x\n")

	found, err := Root(context.Background(), filepath.Join(root, "deep/inside"))
	if err != nil {
		t.Fatal(err)
	}
	wanted, _ := filepath.EvalSymlinks(root)
	got, _ := filepath.EvalSymlinks(found)
	if got != wanted {
		t.Fatalf("root %q, want %q", got, wanted)
	}
}

func TestBranchIsEmptyOnADetachedHead(t *testing.T) {
	root := newRepo(t)
	r := New(root, nil)
	ctx := context.Background()

	if branch, err := r.Branch(ctx); err != nil || branch != "work" {
		t.Fatalf("branch %q, %v", branch, err)
	}
	git(t, root, "switch", "-q", "--detach")
	if branch, err := r.Branch(ctx); err != nil || branch != "" {
		t.Fatalf("detached head named branch %q, %v", branch, err)
	}
}

func TestTheDefaultBranchIsWhatOriginsHeadNames(t *testing.T) {
	root := newRepo(t)
	r := New(root, nil)
	ctx := context.Background()

	if _, err := r.DefaultBranch(ctx); err == nil {
		t.Fatal("a repository with only a work branch has no default")
	}
	git(t, root, "branch", "main")
	if name, err := r.DefaultBranch(ctx); err != nil || name != "main" {
		t.Fatalf("default %q, %v; want main", name, err)
	}
	git(t, root, "update-ref", "refs/remotes/origin/trunk", "HEAD")
	git(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	if name, err := r.DefaultBranch(ctx); err != nil || name != "trunk" {
		t.Fatalf("default %q, %v; want trunk", name, err)
	}
}

func TestLinkedTellsAWorktreeFromTheMainCheckout(t *testing.T) {
	root := newRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, root, "worktree", "add", "-q", "-b", "other", linked)
	ctx := context.Background()

	if is, err := New(root, nil).Linked(ctx); err != nil || is {
		t.Fatalf("main checkout linked %v, %v", is, err)
	}
	if is, err := New(linked, nil).Linked(ctx); err != nil || !is {
		t.Fatalf("worktree linked %v, %v", is, err)
	}
}

func TestCommittedSeesOnlyWhatHeadHolds(t *testing.T) {
	root := newRepo(t)
	write(t, root, "docs/plans/NEW.md", "# new\n")
	r := New(root, nil)
	ctx := context.Background()

	if !r.Committed(ctx, "README.md") {
		t.Fatal("README.md is committed")
	}
	if r.Committed(ctx, "docs/plans/NEW.md") {
		t.Fatal("an untracked file is not committed")
	}
	if r.Committed(ctx, "../outside.md") {
		t.Fatal("a path outside the repository is not committed")
	}
}

func TestHandOffMovesTheBranchIntoAWorktree(t *testing.T) {
	root := newRepo(t)
	git(t, root, "branch", "master")
	write(t, root, "work.txt", "work\n")
	commit(t, root, "work")
	path := filepath.Join(t.TempDir(), "repo.worktrees", "sco-1", "work")
	ctx := context.Background()

	if err := New(root, nil).HandOff(ctx, "work", "master", path); err != nil {
		t.Fatal(err)
	}
	if branch, _ := New(root, nil).Branch(ctx); branch != "master" {
		t.Fatalf("the checkout is on %q, want master", branch)
	}
	if branch, _ := New(path, nil).Branch(ctx); branch != "work" {
		t.Fatalf("the worktree is on %q, want work", branch)
	}
	if _, err := os.Stat(filepath.Join(path, "work.txt")); err != nil {
		t.Fatal("the worktree does not hold the branch's commits")
	}
}

func TestAFailedHandOffPutsTheCheckoutBack(t *testing.T) {
	root := newRepo(t)
	git(t, root, "branch", "master")
	path := t.TempDir()
	write(t, path, "occupied.txt", "x\n")
	ctx := context.Background()

	if err := New(root, nil).HandOff(ctx, "work", "master", path); err == nil {
		t.Fatal("a worktree cannot be made in a directory that is not empty")
	}
	if branch, _ := New(root, nil).Branch(ctx); branch != "work" {
		t.Fatalf("the checkout was left on %q, want work", branch)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// remote gives a repository an origin to push to, and returns the bare one.
func remote(t *testing.T, root string) string {
	t.Helper()
	bare := t.TempDir()
	git(t, bare, "init", "-q", "--bare")
	git(t, root, "remote", "add", "origin", bare)
	return bare
}

func TestPushCreatesTheBranchAndSetsItsUpstream(t *testing.T) {
	root := newRepo(t)
	bare := remote(t, root)
	git(t, root, "branch", "master")
	git(t, root, "switch", "-q", "-c", "feature")
	write(t, root, "a.txt", "a\n")
	commit(t, root, "a")
	r := New(root, nil)
	ctx := context.Background()

	where, err := r.Push(ctx)
	if err != nil || where != "feature to origin, upstream set" {
		t.Fatalf("push %q, %v", where, err)
	}
	if upstream := strings.TrimSpace(git(t, root, "rev-parse", "--abbrev-ref", "feature@{upstream}")); upstream != "origin/feature" {
		t.Fatalf("upstream %q", upstream)
	}

	write(t, root, "b.txt", "b\n")
	commit(t, root, "b")
	if where, err := r.Push(ctx); err != nil || where != "feature to origin" {
		t.Fatalf("second push %q, %v", where, err)
	}
	head := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	if pushed := strings.TrimSpace(git(t, bare, "rev-parse", "refs/heads/feature")); pushed != head {
		t.Fatalf("origin has %s, want %s", pushed, head)
	}
}

// A branch made from origin's master tracks master; pushing it must not.
func TestPushNeverSendsABranchToAnUpstreamOfAnotherName(t *testing.T) {
	root := newRepo(t)
	bare := remote(t, root)
	git(t, root, "push", "-q", "origin", "work:master")
	git(t, root, "fetch", "-q", "origin")
	git(t, root, "switch", "-q", "-c", "feature", "--track", "origin/master")
	write(t, root, "a.txt", "a\n")
	commit(t, root, "a")

	if _, err := New(root, nil).Push(context.Background()); err != nil {
		t.Fatal(err)
	}
	if master := strings.TrimSpace(git(t, bare, "rev-parse", "refs/heads/master")); master == strings.TrimSpace(git(t, root, "rev-parse", "HEAD")) {
		t.Fatal("the branch was pushed onto master")
	}
	if upstream := strings.TrimSpace(git(t, root, "rev-parse", "--abbrev-ref", "feature@{upstream}")); upstream != "origin/feature" {
		t.Fatalf("upstream %q", upstream)
	}
}

func TestPushRefusesTheDefaultBranchAndADetachedHead(t *testing.T) {
	root := newRepo(t)
	remote(t, root)
	git(t, root, "branch", "-m", "master")
	r := New(root, nil)
	ctx := context.Background()

	if _, err := r.Push(ctx); err == nil || !strings.Contains(err.Error(), "default branch") {
		t.Fatalf("pushing master: %v", err)
	}
	git(t, root, "switch", "-q", "--detach")
	if _, err := r.Push(ctx); err == nil || !strings.Contains(err.Error(), "detached") {
		t.Fatalf("pushing a detached head: %v", err)
	}
}

func TestPushWithoutARemoteSaysSo(t *testing.T) {
	root := newRepo(t)
	if _, err := New(root, nil).Push(context.Background()); err == nil || !strings.Contains(err.Error(), "no remote") {
		t.Fatalf("push %v", err)
	}
}

func TestWorktreesListsEveryCheckout(t *testing.T) {
	root := newRepo(t)
	other := filepath.Join(t.TempDir(), "side")
	git(t, root, "worktree", "add", "-q", "-b", "side", other)

	trees, err := New(root, nil).Worktrees(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 2 || trees[0].Branch != "work" || trees[1].Branch != "side" {
		t.Fatalf("worktrees %+v", trees)
	}
}

func TestUnmergedLeavesOutWhatTheDefaultBranchHas(t *testing.T) {
	root := newRepo(t)
	git(t, root, "branch", "merged")
	git(t, root, "switch", "-q", "-c", "ahead")
	write(t, root, "a.txt", "a\n")
	commit(t, root, "a")
	git(t, root, "switch", "-q", "work")

	refs, err := New(root, nil).Unmerged(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Branch != "ahead" {
		t.Fatalf("unmerged %+v", refs)
	}
}

func TestFilesAtReadsADirectoryAtManyCommits(t *testing.T) {
	root := newRepo(t)
	write(t, root, "docs/plans/A.md", "# A\n")
	write(t, root, "docs/plans/A.state.json", "{}\n")
	write(t, root, "docs/plans/notes.txt", "skip\n")
	commit(t, root, "plans")
	first := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	write(t, root, "docs/plans/A.state.json", "{\"units\":{}}\n")
	commit(t, root, "progress")
	second := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))
	bare := strings.TrimSpace(git(t, root, "rev-parse", "HEAD~2"))

	keep := func(name string) bool { return strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".json") }
	files, err := New(root, nil).FilesAt(context.Background(), []string{first, second, bare}, "docs/plans", keep)
	if err != nil {
		t.Fatal(err)
	}
	if string(files[first]["docs/plans/A.state.json"]) != "{}\n" ||
		string(files[second]["docs/plans/A.state.json"]) != "{\"units\":{}}\n" ||
		string(files[second]["docs/plans/A.md"]) != "# A\n" {
		t.Fatalf("files %q", files)
	}
	if _, ok := files[first]["docs/plans/notes.txt"]; ok {
		t.Fatal("a file keep refused was read")
	}
	if _, ok := files[bare]; ok {
		t.Fatal("a commit without the directory has files")
	}
}

func TestRemoveAndCommitLeavesOtherStagedWorkAlone(t *testing.T) {
	root := newRepo(t)
	write(t, root, "docs/plans/A.md", "# A\n")
	write(t, root, "docs/plans/A.state.json", "{}\n")
	commit(t, root, "plan")
	write(t, root, "NOTES.md", "staged\n")
	git(t, root, "add", "NOTES.md")

	err := New(root, nil).RemoveAndCommit(context.Background(),
		[]string{"docs/plans/A.md", "docs/plans/A.state.json"}, "plans: close A\n\nthe report")
	if err != nil {
		t.Fatal(err)
	}
	if files := git(t, root, "show", "--name-status", "--format=%B", "HEAD"); !strings.Contains(files, "plans: close A\n\nthe report") ||
		!strings.Contains(files, "D\tdocs/plans/A.md") || !strings.Contains(files, "D\tdocs/plans/A.state.json") ||
		strings.Contains(files, "NOTES.md") {
		t.Fatalf("the close commit is:\n%s", files)
	}
	if staged := git(t, root, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "NOTES.md" {
		t.Fatalf("staged after the close: %q", staged)
	}
}
