package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
)

// triageCommand parses the triage flags the way `ploopy review` would.
func triageCommand(t *testing.T, args ...string) (*cobra.Command, *reviewFlags) {
	t.Helper()
	f := &reviewFlags{}
	cmd := &cobra.Command{Use: "review"}
	addTriageFlags(cmd, f)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd, f
}

func TestTheTriageDefaultsToTheStrongestModelUncapped(t *testing.T) {
	e, _ := worktreeRepo(t, nil)
	cmd, f := triageCommand(t)
	got, err := chooseTriage(context.Background(), cmd, e, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != defaultTriage {
		t.Fatalf("triage %+v", got)
	}
}

func TestTheLastTriageIsRememberedApartFromTheRun(t *testing.T) {
	e, _ := worktreeRepo(t, nil)
	cmd, f := triageCommand(t, "--triage-model", "sonnet", "--triage-effort", "medium", "--triage-budget", "2")
	if _, err := chooseTriage(context.Background(), cmd, e, f, nil); err != nil {
		t.Fatal(err)
	}
	if path := lastRunPath(context.Background(), e.root); path != "" {
		if _, err := os.Stat(path); err == nil {
			t.Fatal("the triage must not overwrite the run's settings")
		}
	}

	cmd, f = triageCommand(t)
	got, err := chooseTriage(context.Background(), cmd, e, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := review.Triage{Agent: "claude", Model: "sonnet", Effort: "medium", Budget: 2}
	if got != want {
		t.Fatalf("the next review starts from %+v, not %+v", got, want)
	}
}

func TestAnotherTriageAgentTakesItsOwnDefaults(t *testing.T) {
	e, _ := worktreeRepo(t, nil)
	cmd, f := triageCommand(t, "--triage-agent", "opencode")
	got, err := chooseTriage(context.Background(), cmd, e, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := e.harnesses.Get("opencode")
	if got.Agent != "opencode" || got.Model != h.Models()[0] || got.Effort != "high" {
		t.Fatalf("triage %+v", got)
	}

	cmd, f = triageCommand(t, "--triage-agent", "opencode", "--triage-effort", "xhigh")
	if _, err := chooseTriage(context.Background(), cmd, e, f, nil); err == nil ||
		!strings.Contains(err.Error(), "effort") {
		t.Fatalf("an effort opencode does not offer: %v", err)
	}
}

func TestAWrittenReviewsTriageChangesUntilItRuns(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	reviewed := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	s := &state.State{Units: map[string]*state.Entry{"1.1": {
		Status: state.Landed, Outcome: "done", Base: "aaa", Commits: []string{"bbb one"},
	}}}
	own := review.Triage{Agent: "claude", Model: "sonnet", Effort: "medium"}
	text, err := review.Generate(reviewed, s, review.Options{Triage: own})
	if err != nil {
		t.Fatal(err)
	}
	path := review.Path(reviewed.Path)
	write(t, tree, path, text)
	git(t, tree, "add", "-A")
	git(t, tree, "commit", "-q", "-m", "plans: review PASS")

	p := plan.Parse(text, path)
	pending := &state.State{Units: map[string]*state.Entry{}}
	if got := triageLock(context.Background(), e, p, pending); got != "" {
		t.Fatalf("a pending review's triage is locked with %q", got)
	}

	cmd, f := triageCommand(t)
	cmd.SetContext(context.Background())
	chosen, _, ran, err := reviewTriage(context.Background(), cmd, e, f, p, pending)
	if err != nil {
		t.Fatal(err)
	}
	if ran || chosen != own {
		t.Fatalf("the pending triage is %+v, ran %v; want %+v", chosen, ran, own)
	}
	if changed, err := changeTriage(context.Background(), e, tree, p, pending, own, chosen); err != nil || changed {
		t.Fatalf("an unchanged triage changed %v, %v", changed, err)
	}

	want := review.Triage{Agent: "claude", Model: "sonnet", Effort: "high", Budget: 5}
	changed, err := changeTriage(context.Background(), e, tree, p, pending, own, want)
	if err != nil || !changed {
		t.Fatalf("changing a pending triage: %v, %v", changed, err)
	}
	updated, err := plan.Load(filepath.Join(tree, path), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, got := review.TriageOf(updated); got != want {
		t.Fatalf("triage %+v", got)
	}
	if out := gitOut(t, tree, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("the change is committed:\n%s", out)
	}

	u, _ := review.TriageOf(updated)
	landed := &state.State{Units: map[string]*state.Entry{u.ID: {Status: state.Landed}}}
	if got := triageLock(context.Background(), e, updated, landed); got != triageRanLock {
		t.Fatalf("a triage that has run is locked with %q, want %q", got, triageRanLock)
	}
	if again, err := changeTriage(context.Background(), e, tree, updated, landed, want, own); err != nil || again {
		t.Fatalf("a triage that has run changed %v, %v", again, err)
	}
	final, err := plan.Load(filepath.Join(tree, path), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, got := review.TriageOf(final); got != want {
		t.Fatalf("a triage that has run is not changed, but it is now %+v", got)
	}
}

// liveReview announces that a review plan in root is being run.
func liveReview(t *testing.T, e *env, root, path string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if _, err := control.Register(control.Status{
		Root: root, Plan: path, Branch: "pass",
		Agent: "claude sonnet medium",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestALiveReviewsTriageIsLockedAndAFlagCommitsNothing(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	reviewed := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	s := &state.State{Units: map[string]*state.Entry{"1.1": {
		Status: state.Landed, Outcome: "done", Base: "aaa", Commits: []string{"bbb one"},
	}}}
	own := review.Triage{Agent: "claude", Model: "sonnet", Effort: "medium"}
	text, err := review.Generate(reviewed, s, review.Options{Triage: own})
	if err != nil {
		t.Fatal(err)
	}
	path := review.Path(reviewed.Path)
	write(t, tree, path, text)
	git(t, tree, "add", "-A")
	git(t, tree, "commit", "-q", "-m", "plans: review PASS")
	head := strings.TrimSpace(gitOut(t, tree, "rev-parse", "HEAD"))

	p := plan.Parse(text, path)
	pending := &state.State{Units: map[string]*state.Entry{}}
	liveReview(t, e, tree, path)
	if got := triageLock(context.Background(), e, p, pending); got != triageLiveLock {
		t.Fatalf("a running review's triage is locked with %q, want %q", got, triageLiveLock)
	}

	cmd, f := triageCommand(t, "--triage-model", "opus")
	cmd.SetContext(context.Background())
	f.writeOnly = true
	if err := reviewPlan(cmd, e, []string{"PASS"}, f); err != nil {
		t.Fatal(err)
	}

	unchanged, err := plan.Load(filepath.Join(tree, path), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, got := review.TriageOf(unchanged); got != own {
		t.Fatalf("a running review's triage changed to %+v", got)
	}
	if out := strings.TrimSpace(gitOut(t, tree, "status", "--porcelain")); out != "" {
		t.Fatalf("a running review's triage committed:\n%s", out)
	}
	if got := strings.TrimSpace(gitOut(t, tree, "rev-parse", "HEAD")); got != head {
		t.Fatalf("the review's head moved from %s to %s", head, got)
	}
}
