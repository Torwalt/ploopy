package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/fake"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
)

const openTwoUnitPlan = `# Pass

## Stage 1 — Only

### 1.1 One

**Goal:** the first thing.

**Done when:** it is done.

### 1.2 Two

**Goal:** the second thing.

**Done when:** it is done.
`

// openPlanRepo is a main checkout on master holding the given PASS, and a
// worktree of branch pass holding the same plan and the given state.
func openPlanRepo(t *testing.T, planText string) (*env, string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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
	write(t, root, "docs/plans/PASS.md", planText)
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "plans: add PASS")
	git(t, root, "branch", "pass")

	tree := worktreePath(root, "pass")
	git(t, root, "worktree", "add", "--quiet", tree, "pass")
	return &env{root: root, cfg: config.Default(), harnesses: allHarnesses()}, tree
}

func runRun(t *testing.T, e *env, args ...string) error {
	t.Helper()
	cmd := newRun(e)
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	return cmd.ExecuteContext(context.Background())
}

func runArgs(extra ...string) []string {
	args := []string{"--plan", "PASS", "--agent", "fake", "--model", "fake-model", "--effort", "high"}
	return append(args, extra...)
}

func triageArgs(extra ...string) []string {
	return append([]string{"--triage-agent", "fake", "--triage-model", "fake-model", "--triage-effort", "high"}, extra...)
}

// lockIndex makes the next commit fail, leaving a state file written on disk.
func lockIndex(t *testing.T, dir string) error {
	t.Helper()
	gitDir := strings.TrimSpace(gitOut(t, dir, "rev-parse", "--absolute-git-dir"))
	return os.WriteFile(filepath.Join(gitDir, "index.lock"), nil, 0o644)
}

// pushedRepo adds an origin that records each push.
func pushedRepo(t *testing.T, e *env) string {
	t.Helper()
	remote := t.TempDir()
	git(t, remote, "init", "-q", "--bare")
	git(t, e.root, "remote", "add", "origin", remote)
	pushes := filepath.Join(t.TempDir(), "pushes.log")
	write(t, remote, "hooks/pre-receive",
		"#!/bin/sh\ncat >/dev/null\nprintf 'push\\n' >> "+pushes+"\n")
	if err := os.Chmod(filepath.Join(remote, "hooks/pre-receive"), 0o755); err != nil {
		t.Fatal(err)
	}
	return pushes
}

func TestAPlanWithFollowUpRunsItsReviewAndFixPlan(t *testing.T) {
	e, tree := openPlanRepo(t, finishedPlan)
	pushes := pushedRepo(t, e)
	h := fake.New(fixStep("plan.go", "Do the plan"),
		findingsStep(), triageStep(t),
		fixStep("fix1.go", "Do the first fix"), fixStep("fix2.go", "Do the second fix"))
	e.harnesses = harness.Set{h}

	args := runArgs(triageArgs("--follow-up", "--push")...)
	if err := runRun(t, e, args...); err != nil {
		t.Fatal(err)
	}
	if h.Started() != 5 {
		t.Fatalf("the run started %d sessions, want the plan, its review, the triage and two fixes", h.Started())
	}
	if got := h.Specs[0].Model; got != "fake-model" {
		t.Fatalf("the plan ran as %q, want the run's model", got)
	}
	if got := h.Specs[2].Model; got != "fake-model" {
		t.Fatalf("the triage ran as %q, want the flag's model", got)
	}
	if got := h.Specs[3].Model; got != "fix-model" {
		t.Fatalf("the fix plan ran as %q, want its front matter's model", got)
	}

	if !exists(filepath.Join(tree, "docs/plans/PASS_REVIEW.md")) {
		t.Fatal("the plan completed without its review")
	}
	if exists(filepath.Join(tree, "docs/plans/PASS_FIXES_REVIEW.md")) {
		t.Fatal("the fix plan was reviewed")
	}
	if got := branchState(t, e, "pass", "docs/plans/PASS.state.json").Status("1.1"); got != state.Landed {
		t.Fatalf("plan unit 1.1 is %s, want landed", got)
	}
	if got := branchState(t, e, "pass", "docs/plans/PASS_REVIEW.state.json").Status("2.1"); got != state.Landed {
		t.Fatalf("the triage is %s, want landed", got)
	}
	if got := branchState(t, e, "pass", "docs/plans/PASS_FIXES.state.json").Status("1.2"); got != state.Landed {
		t.Fatalf("fix unit 1.2 is %s, want landed", got)
	}

	log := gitOut(t, e.root, "log", "--reverse", "--format=%s", "pass")
	planAt := strings.Index(log, "Do the plan")
	triageAt := strings.Index(log, "plans: triage PASS_REVIEW")
	fixAt := strings.Index(log, "Do the first fix")
	if planAt < 0 || triageAt < 0 || fixAt < 0 || planAt > triageAt || triageAt > fixAt {
		t.Fatalf("the plans did not run in order:\n%s", log)
	}
	pushed, err := os.ReadFile(pushes)
	if err != nil {
		t.Fatalf("the branch was not pushed: %v", err)
	}
	if got := strings.Count(string(pushed), "push\n"); got != 1 {
		t.Fatalf("the branch was pushed %d times, want once after the fix plan", got)
	}
}

func TestAPlanWithoutFollowUpStopsAfterItself(t *testing.T) {
	e, tree := openPlanRepo(t, finishedPlan)
	h := fake.New(fixStep("plan.go", "Do the plan"), findingsStep(), triageStep(t))
	e.harnesses = harness.Set{h}

	if err := runRun(t, e, runArgs()...); err != nil {
		t.Fatal(err)
	}
	if h.Started() != 1 {
		t.Fatalf("the run started %d sessions, want the plan only", h.Started())
	}
	if exists(filepath.Join(tree, "docs/plans/PASS_REVIEW.md")) {
		t.Fatal("the plan was reviewed with follow-up off")
	}
	if got := branchState(t, e, "pass", "docs/plans/PASS.state.json").Status("1.1"); got != state.Landed {
		t.Fatalf("plan unit 1.1 is %s, want landed", got)
	}
}

func TestAPlanStoppedEarlyDoesNotGoOnToItsReview(t *testing.T) {
	t.Run("until", func(t *testing.T) {
		e, tree := openPlanRepo(t, openTwoUnitPlan)
		h := fake.New(fixStep("one.go", "Do the first thing"), fixStep("two.go", "Do the second thing"), findingsStep())
		e.harnesses = harness.Set{h}

		if err := runRun(t, e, runArgs(triageArgs("--follow-up", "--until", "1.1")...)...); err != nil {
			t.Fatal(err)
		}
		if h.Started() != 1 {
			t.Fatalf("the run started %d sessions, want the first unit only", h.Started())
		}
		if exists(filepath.Join(tree, "docs/plans/PASS_REVIEW.md")) {
			t.Fatal("a plan stopped at --until was reviewed")
		}
	})

	t.Run("adjust stop", func(t *testing.T) {
		e, tree := openPlanRepo(t, openTwoUnitPlan)
		stop := true
		h := fake.New(fixStep("one.go", "Do the first thing"), fixStep("two.go", "Do the second thing"), findingsStep())
		h.Steps[0].Do = func(dir string) error {
			if err := fixStep("one.go", "Do the first thing").Do(dir); err != nil {
				return err
			}
			_, err := control.Adjust(os.Getpid(), control.Settings{Stop: &stop})
			return err
		}
		e.harnesses = harness.Set{h}

		if err := runRun(t, e, runArgs(triageArgs("--follow-up")...)...); err != nil {
			t.Fatal(err)
		}
		if h.Started() != 1 {
			t.Fatalf("the run started %d sessions, want the first unit only", h.Started())
		}
		if exists(filepath.Join(tree, "docs/plans/PASS_REVIEW.md")) {
			t.Fatal("a plan stopped with adjust --stop was reviewed")
		}
	})

	t.Run("block", func(t *testing.T) {
		e, tree := openPlanRepo(t, finishedPlan)
		h := fake.New(
			fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is wrong"}},
			fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is still wrong"}},
			findingsStep())
		e.harnesses = harness.Set{h}

		if err := runRun(t, e, runArgs(triageArgs("--follow-up")...)...); err == nil {
			t.Fatal("a blocked plan ended without failing the run")
		}
		if h.Started() != 2 {
			t.Fatalf("the run started %d sessions, want the block and its second opinion", h.Started())
		}
		if exists(filepath.Join(tree, "docs/plans/PASS_REVIEW.md")) {
			t.Fatal("a blocked plan was reviewed")
		}
	})
}

func TestAFinishedPlanLimitedByAFlagDoesNotGoOnToItsReview(t *testing.T) {
	for _, limit := range []struct {
		name string
		args []string
	}{
		{"until", []string{"--until", "1.1"}},
		{"units", []string{"--units", "1"}},
	} {
		t.Run(limit.name, func(t *testing.T) {
			e, tree := openPlanRepo(t, finishedPlan)
			h := fake.New(fixStep("plan.go", "Do the plan"), findingsStep(), triageStep(t))
			e.harnesses = harness.Set{h}

			args := runArgs(triageArgs(append([]string{"--follow-up"}, limit.args...)...)...)
			if err := runRun(t, e, args...); err != nil {
				t.Fatal(err)
			}
			if h.Started() != 1 {
				t.Fatalf("the run started %d sessions, want the plan only", h.Started())
			}
			if exists(filepath.Join(tree, "docs/plans/PASS_REVIEW.md")) {
				t.Fatal("a limited run went on to its review")
			}
		})
	}
}

func TestARunThatFailedAfterItsLastUnitLandedDoesNotGoOnToItsReview(t *testing.T) {
	e, tree := openPlanRepo(t, finishedPlan)
	h := fake.New(
		fake.Step{
			Do: func(dir string) error {
				if err := fixStep("plan.go", "Do the plan").Do(dir); err != nil {
					return err
				}
				return lockIndex(t, dir)
			},
			Outcome: harness.Outcome{Marker: harness.Done},
		},
		findingsStep(), triageStep(t))
	e.harnesses = harness.Set{h}

	err := runRun(t, e, runArgs(triageArgs("--follow-up")...)...)
	if err == nil {
		t.Fatal("a run whose state commit failed ended without an error")
	}
	if !strings.Contains(err.Error(), "Resume with") {
		t.Fatalf("the run ended with %q, want the loop's failure", err)
	}
	if h.Started() != 1 {
		t.Fatalf("the run started %d sessions, want the plan only", h.Started())
	}
	if exists(filepath.Join(tree, "docs/plans/PASS_REVIEW.md")) {
		t.Fatal("a failed run went on to its review")
	}
	raw, err := os.ReadFile(filepath.Join(tree, "docs/plans/PASS.state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := state.Parse(raw, "docs/plans/PASS.state.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Status("1.1"); got != state.Landed {
		t.Fatalf("plan unit 1.1 is %s, want landed", got)
	}
}

func TestTheFollowUpSuffixNamesOnlyWhatTheRunWillDo(t *testing.T) {
	ordinary := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	reviewed := plan.Parse("---\nreviews: docs/plans/PASS.md\n---\n\n"+finishedPlan, "docs/plans/PASS_REVIEW.md")
	fix := plan.Parse(finishedPlan, "docs/plans/PASS_FIXES.md")

	cases := []struct {
		name     string
		p        *plan.Plan
		followUp bool
		limited  bool
		want     string
	}{
		{"ordinary", ordinary, true, false, ", then its review and fix plan"},
		{"review", reviewed, true, false, ", then its fix plan"},
		{"fix plan", fix, true, false, ""},
		{"limited ordinary", ordinary, true, true, ""},
		{"limited review", reviewed, true, true, ""},
		{"follow-up off", ordinary, false, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := followUpSuffix(c.followUp, c.p, c.limited); got != c.want {
				t.Fatalf("the suffix is %q, want %q", got, c.want)
			}
		})
	}
}

func TestAFixPlanWithFollowUpDoesNotStartAReview(t *testing.T) {
	e, tree := worktreeRepo(t, map[string]string{"docs/plans/PASS_FIXES.md": fixesPlan})
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	h := fake.New(fixStep("fix1.go", "Do the first fix"), fixStep("fix2.go", "Do the second fix"), findingsStep())
	e.harnesses = harness.Set{h}

	args := append([]string{"--plan", "PASS_FIXES", "--agent", "fake", "--model", "fake-model", "--effort", "high"},
		triageArgs("--follow-up")...)
	if err := runRun(t, e, args...); err != nil {
		t.Fatal(err)
	}
	if h.Started() != 2 {
		t.Fatalf("the run started %d sessions, want the fix plan's two units", h.Started())
	}
	if exists(filepath.Join(tree, review.Path("docs/plans/PASS_FIXES.md"))) {
		t.Fatal("a fix plan was reviewed")
	}
	if got := branchState(t, e, "pass", "docs/plans/PASS_FIXES.state.json").Status("1.2"); got != state.Landed {
		t.Fatalf("fix unit 1.2 is %s, want landed", got)
	}
}

func TestARescueFromTheFileRunsAtTheStopPoint(t *testing.T) {
	e, _ := openPlanRepo(t, finishedPlan)
	e.cfg.Automation = config.Automation{Rescue: "fake", RescueEffort: "high"}
	h := fake.New(
		fake.Step{Outcome: harness.Outcome{Marker: harness.Done}},
		fake.Step{Do: func(dir string) error {
			return sandboxWrite(filepath.Join(dir, ".ploopy/pass/handover/1.1.rescue-1.md"),
				"## Diagnosis\n\nThe provider returned an empty completion.\n\n"+
					"## Next attempt\n\nThe work is still to be done.\n\nWait: 1ms\n")
		}, Outcome: harness.Outcome{Marker: harness.Retry, Reason: "provider stalled"}},
		fixStep("one.go", "Do the plan"),
	)
	e.harnesses = harness.Set{h}

	if err := runRun(t, e, runArgs("--retries", "0")...); err != nil {
		t.Fatal(err)
	}
	if h.Started() != 3 {
		t.Fatalf("the run started %d sessions, want the unit, its rescue and the retry", h.Started())
	}
	entry := branchState(t, e, "pass", "docs/plans/PASS.state.json").Entry("1.1")
	if entry == nil || entry.Status != state.Landed {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
	if len(entry.Rescues) != 1 || entry.Rescues[0].Agent != "fake fake-model high" {
		t.Fatalf("the rescue on the entry is %+v", entry.Rescues)
	}
}
