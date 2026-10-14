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
	e, tree := worktreeRepo(t, map[string]string{"docs/plans/NEXT.md": finishedPlan})
	before := output(t, e.root, "rev-parse", "master")
	if err := landing(t, e, "pass"); err == nil || !strings.Contains(err.Error(), "NEXT.md") {
		t.Fatalf("an open plan should stop the landing and be named, got %v", err)
	}
	if output(t, e.root, "rev-parse", "master") != before {
		t.Fatal("master must not move")
	}
	if !exists(tree + "/docs/plans/PASS.md") {
		t.Fatal("nothing is closed while another plan is open")
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
