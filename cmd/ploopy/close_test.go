package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Torwalt/ploopy/internal/config"
)

const finishedPlan = "# Pass\n\n## Stage 1 — Only\n\n### 1.1 One\n\n**Goal:** x\n\n**Done when:** y\n"

const finishedState = `{"units": {"1.1": {"status": "landed", "outcome": "done", "date": "2026-10-14T10:00:00Z"}}}
`

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func write(t *testing.T, dir, path, text string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// worktreeRepo is a main checkout on master and, at ploopy's own path, a
// worktree of branch pass holding the finished plan PASS and the given others.
func worktreeRepo(t *testing.T, others map[string]string) (*env, string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q", "-b", "master")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	write(t, root, "README.md", "start\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "first")
	git(t, root, "branch", "pass")

	tree := worktreePath(root, "pass")
	git(t, root, "worktree", "add", "--quiet", tree, "pass")
	write(t, tree, "docs/plans/PASS.md", finishedPlan)
	write(t, tree, "docs/plans/PASS.state.json", finishedState)
	for path, text := range others {
		write(t, tree, path, text)
	}
	git(t, tree, "add", "-A")
	git(t, tree, "commit", "-q", "-m", "plans")

	return &env{root: root, cfg: config.Default(), harnesses: allHarnesses()}, tree
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestClosingTheLastPlanRemovesPloopysWorktree(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	if err := closePlan(context.Background(), e, []string{"PASS"}, false, false); err != nil {
		t.Fatal(err)
	}
	if exists(tree) || exists(worktreesDir(e.root)) {
		t.Fatalf("the worktree and its empty parent should be gone: %s", tree)
	}
}

func TestAWorktreeThatStillHoldsAPlanStays(t *testing.T) {
	e, tree := worktreeRepo(t, map[string]string{"docs/plans/PASS_FIXES.md": finishedPlan})
	if err := closePlan(context.Background(), e, []string{"PASS"}, false, false); err != nil {
		t.Fatal(err)
	}
	if !exists(tree) {
		t.Fatal("the worktree still holds PASS_FIXES and must stay")
	}
	if exists(filepath.Join(tree, "docs/plans/PASS.md")) {
		t.Fatal("PASS itself is closed")
	}
}

func TestKeepWorktreeKeepsIt(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	if err := closePlan(context.Background(), e, []string{"PASS"}, false, true); err != nil {
		t.Fatal(err)
	}
	if !exists(tree) {
		t.Fatal("--keep-worktree keeps it")
	}
}

func TestAWorktreeTheAuthorMadeStays(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	theirs := filepath.Join(filepath.Dir(e.root), "elsewhere")
	git(t, e.root, "worktree", "move", tree, theirs)
	if err := closePlan(context.Background(), e, []string{"PASS"}, false, false); err != nil {
		t.Fatal(err)
	}
	if !exists(theirs) {
		t.Fatal("only ploopy's own worktree is removed")
	}
}

func TestOnlyOpenPlansKeepAFinishedRunsWorktree(t *testing.T) {
	_, tree := worktreeRepo(t, map[string]string{
		"docs/plans/DONE.md":         finishedPlan,
		"docs/plans/DONE.state.json": finishedState,
		"docs/plans/OPEN.md":         finishedPlan,
	})
	open := plansIn(tree, config.Default(), "docs/plans/PASS.md", true)
	if strings.Join(open, ",") != "docs/plans/OPEN.md" {
		t.Fatalf("open plans beside PASS: %v", open)
	}
	left := plansIn(tree, config.Default(), "", false)
	if len(left) != 3 {
		t.Fatalf("plans left: %v", left)
	}
}
