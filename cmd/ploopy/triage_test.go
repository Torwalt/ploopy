package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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

func TestAWrittenReviewsTriageCanStillBeChanged(t *testing.T) {
	e, _ := worktreeRepo(t, nil)
	root := e.root
	reviewed := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	s := &state.State{Units: map[string]*state.Entry{"1.1": {
		Status: state.Landed, Outcome: "done", Base: "aaa", Commits: []string{"bbb one"},
	}}}
	text, err := review.Generate(reviewed, s, review.Options{Triage: defaultTriage})
	if err != nil {
		t.Fatal(err)
	}
	path := "docs/plans/PASS_REVIEW.md"
	write(t, root, path, text)
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "plans: review PASS")

	p := plan.Parse(text, path)
	cmd, f := triageCommand(t, "--triage-model", "sonnet", "--triage-budget", "5")
	cmd.SetContext(context.Background())
	if err := retriage(cmd, e, root, p, &state.State{Units: map[string]*state.Entry{}}, f); err != nil {
		t.Fatal(err)
	}
	changed, err := plan.Load(filepath.Join(root, path), path)
	if err != nil {
		t.Fatal(err)
	}
	want := review.Triage{Agent: "claude", Model: "sonnet", Effort: "high", Budget: 5}
	if _, got := review.TriageOf(changed); got != want {
		t.Fatalf("triage %+v", got)
	}
	if errs, _ := plan.Lint(changed, root, e.harnesses, nil); len(errs) != 1 || !strings.Contains(errs[0], "does not exist") {
		t.Fatalf("the changed review should lint but for the plan it reviews: %v", errs)
	}
	if out := gitOut(t, root, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("the change is committed:\n%s", out)
	}

	landed := &state.State{Units: map[string]*state.Entry{"2.1": {Status: state.Landed}}}
	cmd, f = triageCommand(t, "--triage-model", "opus")
	cmd.SetContext(context.Background())
	if err := retriage(cmd, e, root, changed, landed, f); err != nil {
		t.Fatal(err)
	}
	again, err := plan.Load(filepath.Join(root, path), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, got := review.TriageOf(again); got != want {
		t.Fatalf("a triage that has run is not changed, but it is now %+v", got)
	}
}
