package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/fake"
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
)

const fixesPlan = `---
model: fix-model
---

# Fixes

## Stage 1 — Fix

### 1.1 First fix

**Goal:** fix the first finding.

**Done when:** it is fixed.

### 1.2 Second fix

**Goal:** fix the second finding.

**Done when:** it is fixed.
`

// chainRepo is a worktree holding a finished PASS with one landed unit that
// has commits a review can read.
func chainRepo(t *testing.T) (*env, string) {
	t.Helper()
	e, tree := worktreeRepo(t, nil)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	write(t, tree, "one.go", "package one\n")
	git(t, tree, "add", "one.go")
	git(t, tree, "commit", "-q", "-m", "Do the first thing")
	hash := strings.TrimSpace(gitOut(t, tree, "rev-parse", "HEAD"))
	base := strings.TrimSpace(gitOut(t, tree, "rev-parse", hash+"^"))
	write(t, tree, "docs/plans/PASS.state.json", fmt.Sprintf(
		`{"units":{"1.1":{"status":"landed","outcome":"done","date":"2026-10-14T10:00:00Z",`+
			`"base":"%s","commits":["%s Do the first thing"]}}}`, base, hash))
	git(t, tree, "add", "-A")
	git(t, tree, "commit", "-q", "-m", "plans: land PASS 1.1")
	return e, tree
}

func sandboxWrite(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func sandboxGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

func findingsStep() fake.Step {
	return fake.Step{
		Do: func(dir string) error {
			return sandboxWrite(filepath.Join(dir, ".ploopy/pass_review/handover/1.1.md"),
				"No findings.\n")
		},
		Outcome: harness.Outcome{Marker: harness.Done},
	}
}

func triageStep(t *testing.T) fake.Step {
	t.Helper()
	return triageStepWithPlan(t, fixesPlan)
}

func triageStepWithPlan(t *testing.T, fixes string) fake.Step {
	t.Helper()
	return fake.Step{
		Do: func(dir string) error {
			path := review.FixesPath("docs/plans/PASS.md")
			if err := sandboxWrite(filepath.Join(dir, path), fixes); err != nil {
				return err
			}
			reviewPath := review.Path("docs/plans/PASS.md")
			text, err := os.ReadFile(filepath.Join(dir, reviewPath))
			if err != nil {
				return err
			}
			text = append(text, []byte("\n"+review.AuthorHeading+"\n\nNo design choices were needed.\n")...)
			if err := sandboxWrite(filepath.Join(dir, reviewPath), string(text)); err != nil {
				return err
			}
			if err := sandboxWrite(filepath.Join(dir, ".ploopy/pass_review/handover/2.1.md"),
				"The triage wrote the fix plan.\n"); err != nil {
				return err
			}
			if err := sandboxGit(dir, "add", reviewPath, path); err != nil {
				return err
			}
			return sandboxGit(dir, "commit", "-q", "-m", "plans: triage PASS_REVIEW")
		},
		Outcome: harness.Outcome{Marker: harness.Done},
	}
}

func fixStep(name, message string) fake.Step {
	return fake.Step{
		Do: func(dir string) error {
			if err := sandboxWrite(filepath.Join(dir, name), "fixed\n"); err != nil {
				return err
			}
			if err := sandboxGit(dir, "add", "-A"); err != nil {
				return err
			}
			return sandboxGit(dir, "commit", "-q", "-m", message)
		},
		Outcome: harness.Outcome{Marker: harness.Done},
	}
}

func runReview(t *testing.T, e *env, args ...string) error {
	t.Helper()
	cmd := newReview(e)
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	return cmd.ExecuteContext(context.Background())
}

func branchState(t *testing.T, e *env, branch, path string) *state.State {
	t.Helper()
	raw := gitOut(t, e.root, "show", branch+":"+path)
	s, err := state.Parse([]byte(raw), path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func reviewArgs(extra ...string) []string {
	args := []string{"PASS", "--agent", "fake", "--model", "fake-model", "--effort", "high",
		"--triage-agent", "fake", "--triage-model", "fake-model", "--triage-effort", "high"}
	return append(args, extra...)
}

func TestAReviewWithThenFixesRunsTheFixPlanAfterTheTriage(t *testing.T) {
	e, tree := chainRepo(t)
	h := fake.New(findingsStep(), triageStep(t),
		fixStep("fix1.go", "Do the first fix"), fixStep("fix2.go", "Do the second fix"))
	e.harnesses = harness.Set{h}

	if err := runReview(t, e, reviewArgs("--then", "fixes")...); err != nil {
		t.Fatal(err)
	}
	if h.Started() != 4 {
		t.Fatalf("the run started %d sessions, want the review, its triage and two fixes", h.Started())
	}
	if got := h.Specs[0].Model; got != "fake-model" {
		t.Fatalf("the review ran as %q, want the review's model", got)
	}
	if got := h.Specs[2].Model; got != "fix-model" {
		t.Fatalf("the fix plan ran as %q, want its front matter's model", got)
	}
	if exists(tree) {
		t.Fatal("the worktree should be left once, after the fix plan")
	}

	fixes := branchState(t, e, "pass", "docs/plans/PASS_FIXES.state.json")
	for _, id := range []string{"1.1", "1.2"} {
		if fixes.Status(id) != state.Landed {
			t.Fatalf("fix unit %s is %s, want landed", id, fixes.Status(id))
		}
	}
	reviewed := branchState(t, e, "pass", "docs/plans/PASS_REVIEW.state.json")
	if reviewed.Status("2.1") != state.Landed {
		t.Fatalf("the triage is %s, want landed", reviewed.Status("2.1"))
	}

	log := gitOut(t, e.root, "log", "--reverse", "--format=%s", "pass")
	triageAt := strings.Index(log, "plans: triage PASS_REVIEW")
	fixAt := strings.Index(log, "Do the first fix")
	if triageAt < 0 || fixAt < 0 || triageAt > fixAt {
		t.Fatalf("the fix landed before the triage:\n%s", log)
	}
}

func TestAFixPlansOwnTestSettingOverridesTheReviewsNoTest(t *testing.T) {
	e, _ := chainRepo(t)
	withTests := strings.Replace(fixesPlan, "model: fix-model", "model: fix-model\ntest: yes", 1)
	h := fake.New(findingsStep(), triageStepWithPlan(t, withTests),
		fixStep("fix1.go", "Do the first fix"), fixStep("fix2.go", "Do the second fix"))
	e.harnesses = harness.Set{h}

	if err := runReview(t, e, reviewArgs("--then", "fixes", "--no-test", "--test-cmd", "true")...); err != nil {
		t.Fatal(err)
	}
	if prompt := h.Prompts[2]; !strings.Contains(prompt, "the loop runs them after you") {
		t.Fatalf("the fix unit's prompt does not ask for tests:\n%s", prompt)
	}
}

func TestAFixPlanWithoutATestSettingTakesTheReviewsNoTest(t *testing.T) {
	e, _ := chainRepo(t)
	h := fake.New(findingsStep(), triageStep(t),
		fixStep("fix1.go", "Do the first fix"), fixStep("fix2.go", "Do the second fix"))
	e.harnesses = harness.Set{h}

	if err := runReview(t, e, reviewArgs("--then", "fixes", "--no-test", "--test-cmd", "true")...); err != nil {
		t.Fatal(err)
	}
	if prompt := h.Prompts[2]; strings.Contains(prompt, "the loop runs them after you") {
		t.Fatalf("the fix unit's prompt asks for tests despite --no-test:\n%s", prompt)
	}
}

func TestAReviewWithThenStopEndsAfterTheReview(t *testing.T) {
	e, tree := chainRepo(t)
	h := fake.New(findingsStep(), triageStep(t), fixStep("fix1.go", "Do the first fix"))
	e.harnesses = harness.Set{h}

	if err := runReview(t, e, reviewArgs("--then", "stop")...); err != nil {
		t.Fatal(err)
	}
	if h.Started() != 2 {
		t.Fatalf("the run started %d sessions, want the review and its triage only", h.Started())
	}
	if !exists(tree) {
		t.Fatal("the worktree stays for the fix plan that has not run")
	}
	if exists(filepath.Join(tree, "docs/plans/PASS_FIXES.state.json")) {
		t.Fatal("the fix plan ran under then stop")
	}
	reviewed := branchState(t, e, "pass", "docs/plans/PASS_REVIEW.state.json")
	if reviewed.Status("2.1") != state.Landed {
		t.Fatalf("the triage is %s, want landed", reviewed.Status("2.1"))
	}
}

func TestAReviewStoppedBeforeTheTriageDoesNotStartItsFixPlan(t *testing.T) {
	e, tree := chainRepo(t)
	stop := true
	h := fake.New(findingsStep())
	h.Steps[0].Do = func(dir string) error {
		if err := findingsStep().Do(dir); err != nil {
			return err
		}
		_, err := control.Adjust(os.Getpid(), control.Settings{Stop: &stop})
		return err
	}
	e.harnesses = harness.Set{h}

	if err := runReview(t, e, reviewArgs("--then", "fixes")...); err != nil {
		t.Fatal(err)
	}
	if h.Started() != 1 {
		t.Fatalf("the run started %d sessions, want the collecting unit only", h.Started())
	}
	if exists(filepath.Join(tree, "docs/plans/PASS_FIXES.md")) {
		t.Fatal("the triage wrote a fix plan after the review was stopped")
	}
	reviewed := branchState(t, e, "pass", "docs/plans/PASS_REVIEW.state.json")
	if reviewed.Status("1.1") != state.Landed || reviewed.Status("2.1") != state.Pending {
		t.Fatalf("the review is 1.1 %s, 2.1 %s", reviewed.Status("1.1"), reviewed.Status("2.1"))
	}
}

func TestAnAdjustmentToThenDuringAReviewIsHonoured(t *testing.T) {
	e, tree := chainRepo(t)
	h := fake.New(findingsStep(), triageStep(t),
		fixStep("fix1.go", "Do the first fix"), fixStep("fix2.go", "Do the second fix"))
	h.Steps[0].Do = func(dir string) error {
		if err := findingsStep().Do(dir); err != nil {
			return err
		}
		_, err := control.Adjust(os.Getpid(), control.Settings{Then: "fixes"})
		return err
	}
	e.harnesses = harness.Set{h}

	if err := runReview(t, e, reviewArgs()...); err != nil {
		t.Fatal(err)
	}
	if h.Started() != 4 {
		t.Fatalf("the run started %d sessions, want the fixes the adjustment asked for", h.Started())
	}
	if exists(tree) {
		t.Fatal("the worktree should be left after the fix plan")
	}
	fixes := branchState(t, e, "pass", "docs/plans/PASS_FIXES.state.json")
	if fixes.Status("1.2") != state.Landed {
		t.Fatalf("fix unit 1.2 is %s, want landed", fixes.Status("1.2"))
	}
}

func TestAStopAfterTheTriageDoesNotStartTheFixPlan(t *testing.T) {
	e, tree := chainRepo(t)
	stop := true
	h := fake.New(findingsStep(), triageStep(t))
	h.Steps[1].Do = func(dir string) error {
		if err := triageStep(t).Do(dir); err != nil {
			return err
		}
		_, err := control.Adjust(os.Getpid(), control.Settings{Stop: &stop})
		return err
	}
	e.harnesses = harness.Set{h}

	if err := runReview(t, e, reviewArgs("--then", "fixes")...); err != nil {
		t.Fatal(err)
	}
	if h.Started() != 2 {
		t.Fatalf("the run started %d sessions, want the review and its triage", h.Started())
	}
	if exists(filepath.Join(tree, "docs/plans/PASS_FIXES.state.json")) {
		t.Fatal("the fix plan ran after the review was stopped")
	}
}

// captureSettings records what a run announced while this step runs.
func captureSettings(step fake.Step, planPath string, into *control.Settings) fake.Step {
	do := step.Do
	step.Do = func(dir string) error {
		lives, err := control.List()
		if err != nil {
			return err
		}
		for _, live := range lives {
			if live.Plan == planPath {
				*into = live.Settings
			}
		}
		return do(dir)
	}
	return step
}

func TestTheFixPlanInheritsTheReviewsSpendAllAdjustment(t *testing.T) {
	e, _ := chainRepo(t)
	h := fake.New(findingsStep(), triageStep(t),
		fixStep("fix1.go", "Do the first fix"), fixStep("fix2.go", "Do the second fix"))
	h.Steps[0].Do = func(dir string) error {
		if err := findingsStep().Do(dir); err != nil {
			return err
		}
		spendAll := true
		_, err := control.Adjust(os.Getpid(), control.Settings{SpendAll: &spendAll})
		return err
	}
	var fix control.Settings
	h.Steps[2] = captureSettings(h.Steps[2], "docs/plans/PASS_FIXES.md", &fix)
	h.Steps[3] = captureSettings(h.Steps[3], "docs/plans/PASS_FIXES.md", &fix)
	e.harnesses = harness.Set{h}

	if err := runReview(t, e, reviewArgs("--then", "fixes")...); err != nil {
		t.Fatal(err)
	}
	if fix.SpendAll == nil {
		t.Fatal("the fix run did not keep the review's spend-all adjustment")
	}
	if !*fix.SpendAll {
		t.Fatal("the fix run keeps reserves despite the review's spend-all adjustment")
	}
}

// namedFake is a scripted harness with its own name and choices, so a test can
// tell one from another.
type namedFake struct {
	*fake.Harness
	name    string
	models  []string
	efforts []string
}

func (h *namedFake) Name() string     { return h.name }
func (h *namedFake) Models() []string { return h.models }
func (h *namedFake) Efforts() []string {
	return h.efforts
}

func TestAFixPlansFrontMatterWinsForAgentEffortAndSubscriptions(t *testing.T) {
	e, _ := chainRepo(t)
	fixes := strings.Replace(fixesPlan, "model: fix-model",
		"agent: other\neffort: low\nsubscriptions: fake", 1)
	reviewHarness := &namedFake{
		Harness: fake.New(findingsStep(), triageStepWithPlan(t, fixes),
			fixStep("fix1.go", "Do the first fix"), fixStep("fix2.go", "Do the second fix")),
		name: "fake", models: []string{"fake-model"}, efforts: []string{"high"},
	}
	fixHarness := &namedFake{
		Harness: fake.New(fixStep("fix1.go", "Do the first fix"), fixStep("fix2.go", "Do the second fix")),
		name:    "other", models: []string{"other-model"}, efforts: []string{"low", "high"},
	}
	var fix control.Settings
	fixHarness.Steps[0] = captureSettings(fixHarness.Steps[0], "docs/plans/PASS_FIXES.md", &fix)
	fixHarness.Steps[1] = captureSettings(fixHarness.Steps[1], "docs/plans/PASS_FIXES.md", &fix)
	e.harnesses = harness.Set{reviewHarness, fixHarness}

	if err := runReview(t, e, reviewArgs("--then", "fixes")...); err != nil {
		t.Fatal(err)
	}
	if reviewHarness.Started() != 2 {
		t.Fatalf("the review ran %d sessions, want it and its triage", reviewHarness.Started())
	}
	if fixHarness.Started() != 2 {
		t.Fatalf("the fix plan ran %d sessions, want both units on its agent", fixHarness.Started())
	}
	if got := fixHarness.Specs[0].Effort; got != "low" {
		t.Fatalf("the fix plan ran at %q effort, want its front matter's", got)
	}
	if got := fixHarness.Specs[0].Model; got != "other-model" {
		t.Fatalf("the fix plan ran as %q, want its new agent's default", got)
	}
	if fix.Subscriptions != "fake" {
		t.Fatalf("the fix run uses %q, want its front matter's subscription set", fix.Subscriptions)
	}
}

func TestAReviewWithoutThenStartsNoFixPlan(t *testing.T) {
	e, tree := chainRepo(t)
	h := fake.New(findingsStep(), triageStep(t), fixStep("fix1.go", "Do the first fix"))
	e.harnesses = harness.Set{h}

	if err := runReview(t, e, reviewArgs()...); err != nil {
		t.Fatal(err)
	}
	if h.Started() != 2 {
		t.Fatalf("the run started %d sessions, want the review and its triage only", h.Started())
	}
	if exists(filepath.Join(tree, "docs/plans/PASS_FIXES.state.json")) {
		t.Fatal("the fix plan ran without --then fixes")
	}
}

func TestAPushHappensOnceAfterTheFixPlan(t *testing.T) {
	e, tree := chainRepo(t)
	remote := t.TempDir()
	git(t, remote, "init", "-q", "--bare")
	git(t, e.root, "remote", "add", "origin", remote)
	pushes := filepath.Join(t.TempDir(), "pushes.log")
	write(t, remote, "hooks/pre-receive",
		"#!/bin/sh\ncat >/dev/null\nprintf 'push\\n' >> "+pushes+"\n")
	if err := os.Chmod(filepath.Join(remote, "hooks/pre-receive"), 0o755); err != nil {
		t.Fatal(err)
	}

	h := fake.New(findingsStep(), triageStep(t),
		fixStep("fix1.go", "Do the first fix"), fixStep("fix2.go", "Do the second fix"))
	e.harnesses = harness.Set{h}

	if err := runReview(t, e, reviewArgs("--then", "fixes", "--push")...); err != nil {
		t.Fatal(err)
	}
	if h.Started() != 4 {
		t.Fatalf("the run started %d sessions, want the fixes too", h.Started())
	}
	if exists(tree) {
		t.Fatal("the worktree should be left once, after the fix plan")
	}
	log, err := os.ReadFile(pushes)
	if err != nil {
		t.Fatalf("the branch was not pushed: %v", err)
	}
	if got := strings.Count(string(log), "push\n"); got != 1 {
		t.Fatalf("the branch was pushed %d times, want once after the fix plan", got)
	}
	pushed := gitOut(t, remote, "log", "--format=%s", "pass")
	for _, want := range []string{"Do the first fix", "Do the second fix"} {
		if !strings.Contains(pushed, want) {
			t.Fatalf("the push happened before %q:\n%s", want, pushed)
		}
	}
}

func TestAnotherPlansOpenUnitsDoNotKeepAFinishedRunsWorktree(t *testing.T) {
	e, tree := chainRepo(t)
	// The branch inherited an open plan from master: it is master's, not the
	// worktree's.
	write(t, tree, "docs/plans/PLAYTEST_FIXES.md", finishedPlan)
	git(t, tree, "add", "-A")
	git(t, tree, "commit", "-q", "-m", "plan playtest fixes")
	h := fake.New(findingsStep(), triageStep(t),
		fixStep("fix1.go", "Do the first fix"), fixStep("fix2.go", "Do the second fix"))
	e.harnesses = harness.Set{h}

	if err := runReview(t, e, reviewArgs("--then", "fixes")...); err != nil {
		t.Fatal(err)
	}
	if exists(tree) {
		t.Fatal("PASS and its review and fixes are done; PLAYTEST_FIXES must not keep the worktree")
	}
}
