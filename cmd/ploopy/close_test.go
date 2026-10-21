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

// worktreeRepo is a main checkout on master holding the plan PASS and, at
// ploopy's own path, a worktree of branch pass holding PASS finished and the
// given others.
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
	write(t, root, "docs/plans/PASS.md", finishedPlan)
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "plans: add PASS")
	git(t, root, "branch", "pass")

	tree := worktreePath(root, "pass")
	git(t, root, "worktree", "add", "--quiet", tree, "pass")
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

func TestClosingAPlanDropsItOnMaster(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	pass := output(t, e.root, "rev-parse", "pass")
	if err := closePlan(context.Background(), e, []string{"PASS"}, false); err != nil {
		t.Fatal(err)
	}
	if got := output(t, e.root, "log", "-1", "--format=%s", "master"); got != "plans: close PASS" {
		t.Fatalf("the plan should be closed on master, last commit %q", got)
	}
	if exists(filepath.Join(e.root, "docs/plans/PASS.md")) {
		t.Fatal("the main checkout should no longer hold the plan")
	}
	if output(t, e.root, "rev-parse", "pass") != pass || !exists(tree) {
		t.Fatal("the plan's branch and worktree are left as they are")
	}
	if _, err := e.find(context.Background(), "PASS"); err == nil {
		t.Fatal("a closed plan should be gone, though its branch still holds it")
	}
}

func TestClosingAPlanNeedsMasterInTheMainCheckout(t *testing.T) {
	e, _ := worktreeRepo(t, nil)
	git(t, e.root, "switch", "-q", "-c", "elsewhere")
	if err := closePlan(context.Background(), e, []string{"PASS"}, false); err == nil ||
		!strings.Contains(err.Error(), "git switch master") {
		t.Fatalf("closing off master should be refused, got %v", err)
	}
}

func TestAReviewIsClosedOnItsBranch(t *testing.T) {
	e, tree := worktreeRepo(t, map[string]string{
		"docs/plans/PASS_REVIEW.md":         finishedPlan,
		"docs/plans/PASS_REVIEW.state.json": finishedState,
	})
	master := output(t, e.root, "rev-parse", "master")
	if err := closePlan(context.Background(), e, []string{"PASS_REVIEW"}, false); err != nil {
		t.Fatal(err)
	}
	if got := output(t, tree, "log", "-1", "--format=%s"); got != "plans: close PASS_REVIEW" {
		t.Fatalf("the review should be closed on pass, last commit %q", got)
	}
	if output(t, e.root, "rev-parse", "master") != master {
		t.Fatal("master is untouched")
	}
	if !exists(filepath.Join(tree, "docs/plans/PASS.md")) {
		t.Fatal("the plan itself stays open")
	}
}

func TestOnlyOpenPlansOfTheFamilyKeepAFinishedRunsWorktree(t *testing.T) {
	_, tree := worktreeRepo(t, map[string]string{
		"docs/plans/PASS_REVIEW.md":         finishedPlan,
		"docs/plans/PASS_REVIEW.state.json": finishedState,
		"docs/plans/PASS_FIXES.md":          finishedPlan,
		"docs/plans/PLAYTEST_FIXES.md":      finishedPlan,
	})
	open := familyIn(tree, config.Default(), "pass", "docs/plans/PASS.md", true)
	if strings.Join(open, ",") != "docs/plans/PASS_FIXES.md" {
		t.Fatalf("open plans beside PASS: %v", open)
	}
	left := familyIn(tree, config.Default(), "pass", "docs/plans/PASS.md", false)
	if strings.Join(left, ",") != "docs/plans/PASS_FIXES.md,docs/plans/PASS_REVIEW.md" {
		t.Fatalf("plans of PASS left: %v", left)
	}
}

func TestABranchNamedAfterNoPlanHoldsTheFamilyOfItsPlan(t *testing.T) {
	_, tree := worktreeRepo(t, map[string]string{
		"docs/plans/PASS_FIXES.md":     finishedPlan,
		"docs/plans/PLAYTEST_FIXES.md": finishedPlan,
	})
	open := familyIn(tree, config.Default(), "feature", "docs/plans/PASS.md", true)
	if strings.Join(open, ",") != "docs/plans/PASS_FIXES.md" {
		t.Fatalf("open plans beside PASS on feature: %v", open)
	}
}

func TestAFixPlanOnItsOwnBranchHoldsItsReview(t *testing.T) {
	_, tree := worktreeRepo(t, map[string]string{
		"docs/plans/PLAYTEST_FIXES.md":         finishedPlan,
		"docs/plans/PLAYTEST_FIXES.state.json": finishedState,
		"docs/plans/PLAYTEST_FIXES_REVIEW.md":  finishedPlan,
		"docs/plans/PLAYTEST.md":               finishedPlan,
	})
	open := familyIn(tree, config.Default(), "playtest-fixes", "docs/plans/PLAYTEST_FIXES.md", true)
	if strings.Join(open, ",") != "docs/plans/PLAYTEST_FIXES_REVIEW.md" {
		t.Fatalf("open plans on playtest-fixes: %v", open)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
