package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func output(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func landing(t *testing.T, e *env, args ...string) error {
	t.Helper()
	cmd := newLand(e)
	cmd.SetArgs(append([]string{"--no-agent"}, args...))
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	return cmd.ExecuteContext(context.Background())
}

func TestLandingClosesThePlansAndLandsTheBranch(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	write(t, e.root, "master.txt", "moved on\n")
	git(t, e.root, "add", "-A")
	git(t, e.root, "commit", "-q", "-m", "master moved on")

	if err := landing(t, e, "pass"); err != nil {
		t.Fatal(err)
	}
	log := output(t, e.root, "log", "--format=%s", "master")
	if !strings.HasPrefix(log, "plans: close PASS\nplans\nmaster moved on\n") {
		t.Fatalf("master should end at the closed plan, rebased on its own work:\n%s", log)
	}
	if exists(tree) {
		t.Fatal("ploopy's worktree should be gone")
	}
	if out := output(t, e.root, "branch", "--list", "pass"); out != "" {
		t.Fatalf("the landed branch should be deleted, found %q", out)
	}
}

func TestLandingRefusesABranchWithAnOpenPlan(t *testing.T) {
	e, tree := worktreeRepo(t, map[string]string{"docs/plans/PASS_FIXES.md": finishedPlan})
	before := output(t, e.root, "rev-parse", "master")
	if err := landing(t, e, "pass"); err == nil || !strings.Contains(err.Error(), "PASS_FIXES.md") {
		t.Fatalf("an open plan should stop the landing and be named, got %v", err)
	}
	if output(t, e.root, "rev-parse", "master") != before {
		t.Fatal("master must not move")
	}
	if !exists(tree + "/docs/plans/PASS.md") {
		t.Fatal("nothing is closed while another plan is open")
	}
}

func TestLandingIgnoresAnotherFamilysPlan(t *testing.T) {
	e, tree := worktreeRepo(t, map[string]string{"docs/plans/NEXT.md": finishedPlan})
	if err := landing(t, e, "pass"); err != nil {
		t.Fatalf("NEXT is no plan of pass and must not stop its landing: %v", err)
	}
	log := output(t, e.root, "log", "--format=%s", "master")
	if !strings.Contains(log, "plans: close PASS") || strings.Contains(log, "plans: close NEXT") {
		t.Fatalf("only PASS is closed:\n%s", log)
	}
	if exists(tree) {
		t.Fatal("ploopy's worktree should be gone")
	}
}

func TestLandingABranchNamedAfterNoPlanRefusesItsOwnOpenPlan(t *testing.T) {
	e, tree := worktreeRepo(t, map[string]string{"docs/plans/NEXT.md": finishedPlan})
	git(t, tree, "branch", "-m", "pass", "feature")
	if err := landing(t, e, "feature"); err == nil || !strings.Contains(err.Error(), "NEXT.md") {
		t.Fatalf("a plan the branch wrote is its own there, got %v", err)
	}
}

func TestLandingABranchCheckedOutNowhereChecksItOutFirst(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	git(t, e.root, "worktree", "remove", tree)

	if err := landing(t, e, "pass"); err != nil {
		t.Fatal(err)
	}
	if got := output(t, e.root, "log", "-1", "--format=%s", "master"); got != "plans: close PASS" {
		t.Fatalf("master should end at the closed plan, not %q", got)
	}
	if exists(tree) {
		t.Fatal("the worktree land made should be gone again")
	}
}

func TestLandingLeavesAPlanItInheritedFromMasterAlone(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	write(t, e.root, "docs/plans/ELSEWHERE.md", finishedPlan)
	git(t, e.root, "add", "-A")
	git(t, e.root, "commit", "-q", "-m", "plan elsewhere")
	git(t, tree, "rebase", "-q", "master")
	write(t, tree, "work.txt", "work\n")
	git(t, tree, "add", "-A")
	git(t, tree, "commit", "-q", "-m", "work")
	git(t, e.root, "rm", "-q", "docs/plans/ELSEWHERE.md")
	git(t, e.root, "commit", "-q", "-m", "plans: close ELSEWHERE elsewhere")

	if err := landing(t, e, "pass"); err != nil {
		t.Fatalf("an inherited plan with no progress must not stop the landing: %v", err)
	}
	log := output(t, e.root, "log", "--format=%s", "master")
	if strings.Count(log, "plans: close ELSEWHERE") != 1 || !strings.Contains(log, "plans: close PASS") {
		t.Fatalf("only the branch's own plan is closed by the landing:\n%s", log)
	}
	if exists(e.root + "/docs/plans/ELSEWHERE.md") {
		t.Fatal("the rebase should carry master's close of the inherited plan")
	}
}

func TestLandingLeavesAnotherPlanRunOnTheBranchAlone(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	write(t, e.root, "docs/plans/LATER.md", finishedPlan)
	git(t, e.root, "add", "-A")
	git(t, e.root, "commit", "-q", "-m", "plan later")
	git(t, tree, "rebase", "-q", "master")
	write(t, tree, "docs/plans/LATER.state.json", finishedState)
	git(t, tree, "add", "-A")
	git(t, tree, "commit", "-q", "-m", "plans: record LATER progress")

	if err := landing(t, e, "pass"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output(t, e.root, "log", "--format=%s", "master"), "plans: close LATER") {
		t.Fatal("the pass branch lands PASS's family only")
	}
}
