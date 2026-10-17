package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/claude"
	"github.com/Torwalt/ploopy/internal/harness/codex"
	"github.com/Torwalt/ploopy/internal/harness/opencode"
)

var fakeDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakeagent-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := buildFake(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	fakeDir = dir
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func buildFake(dir string) error {
	bin := filepath.Join(dir, "fakeagent")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build: %w\n%s", err, out)
	}
	for _, name := range []string{"claude", "codex", "opencode"} {
		link := filepath.Join(dir, name)
		if err := os.Symlink(bin, link); err == nil {
			continue
		}
		data, err := os.ReadFile(bin)
		if err != nil {
			return err
		}
		if err := os.WriteFile(link, data, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func sandbox(t *testing.T, scenario string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PLOOPY_SANDBOX", dir)
	if err := os.WriteFile(filepath.Join(dir, "scenario.toml"), []byte(scenario), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runClaude(t *testing.T, prompt string) harness.Outcome {
	t.Helper()
	return runAdapter(t, claude.New(), prompt)
}

func runCodex(t *testing.T, prompt string) harness.Outcome {
	t.Helper()
	return runAdapter(t, codex.New(), prompt)
}

func runOpencode(t *testing.T, prompt string) harness.Outcome {
	t.Helper()
	return runAdapter(t, opencode.New(), prompt)
}

func runAdapter(t *testing.T, h harness.Harness, prompt string) harness.Outcome {
	t.Helper()
	session, err := h.Start(context.Background(), harness.Spec{
		Prompt:  prompt,
		Dir:     t.TempDir(),
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range session.Events() {
	}
	outcome, err := session.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

const claudeScenario = `
[claude]
seconds = 0
session_used = 0.40
session_resets_in = "2h"
weekly_used = 0.51
weekly_resets_in = "96h"
per_session = 0.15
per_session_weekly = 0.01
cost = 0.80
outcome = "done"
`

func TestClaudeReportsItsBudgetWithoutDrift(t *testing.T) {
	sandbox(t, claudeScenario)

	outcome := runClaude(t, "say hello")
	if outcome.Marker != harness.Done || outcome.SessionID == "" || outcome.CostUSD != 0.80 {
		t.Fatalf("outcome %+v", outcome)
	}
	if outcome.Budget == nil {
		t.Fatal("the session reported no budget")
	}
	if !near(outcome.Budget.Session.Used, 0.55) || !near(outcome.Budget.Weekly.Used, 0.52) {
		t.Fatalf("budget %+v", outcome.Budget)
	}
	if len(outcome.Degraded) != 0 {
		t.Fatalf("degraded %v", outcome.Degraded)
	}
}

const codexScenario = `
[codex]
seconds = 0
weekly_used = 0.10
weekly_resets_in = "24h"
per_session = 0.20
per_session_weekly = 0.02
outcome = "done"
`

func TestCodexReportsItsBudgetWithoutDrift(t *testing.T) {
	sandbox(t, codexScenario)

	now := time.Now()
	budget, err := codex.New().Budget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if budget == nil || !near(budget.Weekly.Used, 0.10) {
		t.Fatalf("budget %+v", budget)
	}
	if reset := budget.Weekly.ResetAt; reset.Sub(now) < 23*time.Hour || reset.Sub(now) > 25*time.Hour {
		t.Fatalf("weekly reset %v, want about 24h from now", reset)
	}

	outcome := runCodex(t, "say hello")
	if outcome.Marker != harness.Done || outcome.SessionID == "" || outcome.Turns == 0 {
		t.Fatalf("outcome %+v", outcome)
	}
	if outcome.BudgetBefore == nil || !near(outcome.BudgetBefore.Weekly.Used, 0.10) {
		t.Fatalf("budget before %+v", outcome.BudgetBefore)
	}
	if outcome.Budget == nil || !near(outcome.Budget.Weekly.Used, 0.12) || !near(outcome.Budget.Session.Used, 0.20) {
		t.Fatalf("budget after %+v", outcome.Budget)
	}
	if len(outcome.Degraded) != 0 {
		t.Fatalf("degraded %v", outcome.Degraded)
	}
}

func TestOpencodeReportsItsSteps(t *testing.T) {
	sandbox(t, `
[opencode]
seconds = 0
cost = 0.05
outcome = "done"
`)

	outcome := runOpencode(t, "say hello")
	if outcome.Marker != harness.Done || outcome.SessionID == "" || outcome.CostUSD != 0.05 {
		t.Fatalf("outcome %+v", outcome)
	}
	if len(outcome.Degraded) != 0 {
		t.Fatalf("degraded %v", outcome.Degraded)
	}
}

func TestEachAdapterRecognisesTheSameLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T, string) harness.Outcome
	}{
		{"claude", runClaude},
		{"codex", runCodex},
		{"opencode", runOpencode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sandbox(t, fmt.Sprintf(`
[%s]
seconds = 0
session_used = 0.40
session_resets_in = "2h"
weekly_used = 0.51
weekly_resets_in = "96h"
outcome = "limit"
`, tc.name))

			outcome := tc.run(t, "say hello")
			if outcome.RateLimit == nil {
				t.Fatalf("outcome %+v", outcome)
			}
			if outcome.Marker == harness.Blocked {
				t.Fatal("a usage limit must never be read as a block")
			}
		})
	}
}

func TestClaudeWeeklyLimitReportsTheWeeklyReset(t *testing.T) {
	dir := sandbox(t, `
[claude]
seconds = 0
session_used = 0.40
session_resets_in = "2h"
weekly_used = 1.0
weekly_resets_in = "96h"
outcome = "done"
`)

	outcome := runClaude(t, "say hello")
	if outcome.RateLimit == nil {
		t.Fatalf("outcome %+v", outcome)
	}
	want := weeklyReset(t, dir, "claude")
	if got := outcome.RateLimit.ResetAt; got.Sub(want) < -5*time.Minute || got.Sub(want) > 5*time.Minute {
		t.Fatalf("limit reset %v, want the weekly reset %v", got, want)
	}
}

func TestCodexWeeklyLimitReportsTheWeeklyReset(t *testing.T) {
	dir := sandbox(t, `
[codex]
seconds = 0
session_used = 0.40
session_resets_in = "2h"
weekly_used = 1.0
weekly_resets_in = "96h"
outcome = "done"
`)

	outcome := runCodex(t, "say hello")
	if outcome.RateLimit == nil {
		t.Fatalf("outcome %+v", outcome)
	}
	want := weeklyReset(t, dir, "codex")
	if got := outcome.RateLimit.ResetAt; got.Sub(want) < -5*time.Minute || got.Sub(want) > 5*time.Minute {
		t.Fatalf("limit reset %v, want the weekly reset %v", got, want)
	}
}

func weeklyReset(t *testing.T, dir, name string) time.Time {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	entry, ok := state[name]
	if !ok {
		t.Fatalf("state has no %s", name)
	}
	return entry.Weekly.ResetAt
}

func TestEachSessionAddsItsShare(t *testing.T) {
	dir := sandbox(t, claudeScenario)

	first := runClaude(t, "say hello")
	second := runClaude(t, "say hello")
	if first.Budget == nil || second.Budget == nil {
		t.Fatal("no budget reported")
	}
	if !near(first.Budget.Session.Used, 0.55) || !near(second.Budget.Session.Used, 0.70) {
		t.Fatalf("sessions used %v then %v", first.Budget.Session.Used, second.Budget.Session.Used)
	}

	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if got := state["claude"].Session.Used; !near(got, 0.70) {
		t.Fatalf("state session used %v, want 0.70", got)
	}
}

func TestRedOutcomeCommitsBROKEN(t *testing.T) {
	sandbox(t, scenarioWithOutcome("red"))
	repo := fakeRepo(t)
	handover := filepath.Join(t.TempDir(), "1.1.md")

	if output, err := invoke(t, repo, "claude", implementingPrompt(handover)); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if subject := strings.TrimSpace(git(t, repo, "log", "-1", "--pretty=%s")); subject != "Do unit 1.1" {
		t.Fatalf("commit subject %q", subject)
	}
	git(t, repo, "cat-file", "-e", "HEAD:BROKEN")
	if dirty := git(t, repo, "status", "--porcelain"); dirty != "" {
		t.Fatalf("the red session left the tree dirty:\n%s", dirty)
	}
}

func TestDirtyOutcomeLeavesWorkUncommitted(t *testing.T) {
	sandbox(t, scenarioWithOutcome("dirty"))
	repo := fakeRepo(t)
	before := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))
	handover := filepath.Join(t.TempDir(), "1.1.md")

	if output, err := invoke(t, repo, "claude", implementingPrompt(handover)); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if after := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD")); after != before {
		t.Fatalf("the dirty session committed: %s became %s", before, after)
	}
	if changed := strings.TrimSpace(git(t, repo, "diff", "--name-only")); changed != "work.txt" {
		t.Fatalf("the dirty session changed %q, want work.txt", changed)
	}
	if staged := strings.TrimSpace(git(t, repo, "diff", "--cached", "--name-only")); staged != "" {
		t.Fatalf("the dirty session staged %q", staged)
	}
	work, err := os.ReadFile(filepath.Join(repo, "work.txt"))
	if err != nil || !strings.Contains(string(work), "unit 1.1 by claude") {
		t.Fatalf("work.txt %q, error %v", work, err)
	}
}

func TestBlockedAndNothingEndWithTheirMarkersAndChangeNothing(t *testing.T) {
	for _, tc := range []struct {
		outcome string
		marker  string
	}{
		{"blocked", "BLOCKED the fake scenario asked for it"},
		{"nothing", "NOTHING-TO-DO the fake scenario found nothing to do"},
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			sandbox(t, scenarioWithOutcome(tc.outcome))
			repo := fakeRepo(t)
			before := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))
			handover := filepath.Join(t.TempDir(), "1.1.md")

			output, err := invoke(t, repo, "claude", implementingPrompt(handover))
			if err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
			if !strings.Contains(output, tc.marker) {
				t.Fatalf("output does not end with %q:\n%s", tc.marker, output)
			}
			if after := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD")); after != before {
				t.Fatalf("HEAD moved from %s to %s", before, after)
			}
			if dirty := git(t, repo, "status", "--porcelain"); dirty != "" {
				t.Fatalf("the %s session left the tree dirty:\n%s", tc.outcome, dirty)
			}
			if work, err := os.ReadFile(filepath.Join(repo, "work.txt")); err != nil || string(work) != "baseline\n" {
				t.Fatalf("work.txt %q, error %v", work, err)
			}
		})
	}
}

func TestLoggedOutOutcomeFailsWithoutChangingAnything(t *testing.T) {
	dir := sandbox(t, scenarioWithOutcome("logged-out"))
	repo := fakeRepo(t)
	before := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))
	handover := filepath.Join(t.TempDir(), "1.1.md")

	output, err := invoke(t, repo, "claude", implementingPrompt(handover))
	if err == nil {
		t.Fatalf("a logged-out session exited zero:\n%s", output)
	}
	if after := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD")); after != before {
		t.Fatalf("HEAD moved from %s to %s", before, after)
	}
	if dirty := git(t, repo, "status", "--porcelain"); dirty != "" {
		t.Fatalf("the logged-out session left the tree dirty:\n%s", dirty)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Fatalf("a logged-out session wrote state.json, error %v", err)
	}
}

func TestAPastWindowRestartsWhenTheSessionRuns(t *testing.T) {
	dir := sandbox(t, `
[claude]
seconds = 0
session_used = 0.11
session_resets_in = "2h"
weekly_used = 0.22
weekly_resets_in = "96h"
per_session = 0
per_session_weekly = 0
cost = 0
outcome = "done"
`)

	past := time.Now().Add(-time.Hour)
	state := State{"claude": &HarnessState{
		Session: Window{Used: 0.75, ResetAt: past},
		Weekly:  Window{Used: 0.66, ResetAt: past},
	}}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	outcome := runClaude(t, "say hello")
	if outcome.Budget == nil {
		t.Fatal("no budget reported")
	}
	if !near(outcome.Budget.Session.Used, 0) || !near(outcome.Budget.Weekly.Used, 0) {
		t.Fatalf("budget %+v, want both windows at 0", outcome.Budget)
	}
	now := time.Now()
	if wait := outcome.Budget.Session.ResetAt.Sub(now); wait < 4*time.Hour+50*time.Minute || wait > 5*time.Hour+10*time.Minute {
		t.Fatalf("session reset in %v, want about 5h", wait)
	}
	if wait := outcome.Budget.Weekly.ResetAt.Sub(now); wait < 6*24*time.Hour+23*time.Hour || wait > 7*24*time.Hour+time.Hour {
		t.Fatalf("weekly reset in %v, want about 7d", wait)
	}
}

func TestEditingTheScenarioReseedsTheState(t *testing.T) {
	dir := sandbox(t, claudeScenario)
	first := runClaude(t, "say hello")
	if first.Budget == nil || !near(first.Budget.Session.Used, 0.55) {
		t.Fatalf("first budget %+v, want the session at 0.55", first.Budget)
	}

	updated := strings.Replace(claudeScenario, "session_used = 0.40", "session_used = 0.70", 1)
	path := filepath.Join(dir, "scenario.toml")
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}

	second := runClaude(t, "say hello")
	if second.BudgetBefore == nil || !near(second.BudgetBefore.Session.Used, 0.70) {
		t.Fatalf("second session started at %+v, want the reseeded 0.70", second.BudgetBefore)
	}
	if second.Budget == nil || !near(second.Budget.Session.Used, 0.85) {
		t.Fatalf("second session ended at %+v, want 0.85", second.Budget)
	}
}

func scenarioWithOutcome(outcome string) string {
	return strings.Replace(claudeScenario, `outcome = "done"`, `outcome = "`+outcome+`"`, 1)
}

func implementingPrompt(handover string) string {
	return "You are running unattended under the plan loop. You implement exactly one unit\n" +
		"of an authorized, in-flight task document: unit 1.1, *Fake agents*, of\n" +
		"`docs/plans/TOY.md`.\n\n" +
		"* Write the handover to `" + handover + "`, overwriting whatever is there.\n"
}

func fakeRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	writeFileForTest(t, filepath.Join(repo, "work.txt"), "baseline\n")
	git(t, repo, "add", "work.txt")
	git(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.invalid",
		"commit", "-q", "-m", "initial")
	return repo
}

func TestImplementingAndCollectingRoles(t *testing.T) {
	sandbox(t, claudeScenario)
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.invalid",
		"commit", "-q", "--allow-empty", "-m", "initial")

	handover := filepath.Join(t.TempDir(), "1.1.md")
	implementing := "You are running unattended under the plan loop. You implement exactly one unit\n" +
		"of an authorized, in-flight task document: unit 1.1, *Fake agents*, of\n" +
		"`docs/plans/TOY.md`.\n\n" +
		"* Write the handover to `" + handover + "`, overwriting whatever is there.\n"
	if output, err := invoke(t, repo, "claude", implementing); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}

	if text, err := os.ReadFile(handover); err != nil || !strings.Contains(string(text), "Implemented unit 1.1") {
		t.Fatalf("handover %q, error %v", text, err)
	}
	if subject := strings.TrimSpace(git(t, repo, "log", "-1", "--pretty=%s")); subject != "Do unit 1.1" {
		t.Fatalf("commit subject %q", subject)
	}
	work, err := os.ReadFile(filepath.Join(repo, "work.txt"))
	if err != nil || !strings.Contains(string(work), "unit 1.1 by claude") {
		t.Fatalf("work.txt %q, error %v", work, err)
	}
	if dirty := git(t, repo, "status", "--porcelain"); dirty != "" {
		t.Fatalf("the implementing session left the tree dirty:\n%s", dirty)
	}

	before := strings.TrimSpace(git(t, repo, "rev-list", "--count", "HEAD"))
	findings := filepath.Join(t.TempDir(), "findings.md")
	collecting := "You are running unattended under the plan loop, reviewing work that other\n" +
		"sessions already landed. This session is unit 1.1, *Review 1.1*, of\n" +
		"`docs/plans/TOY_REVIEW.md`.\n\n" +
		"* Write your findings to `" + findings + "`, overwriting whatever is there.\n"
	if output, err := invoke(t, repo, "claude", collecting); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}

	if text, err := os.ReadFile(findings); err != nil || strings.TrimSpace(string(text)) != "No findings." {
		t.Fatalf("findings %q, error %v", text, err)
	}
	if after := strings.TrimSpace(git(t, repo, "rev-list", "--count", "HEAD")); after != before {
		t.Fatalf("a collecting session committed: %s commits became %s", before, after)
	}
	if dirty := git(t, repo, "status", "--porcelain"); dirty != "" {
		t.Fatalf("the collecting session left the tree dirty:\n%s", dirty)
	}
}

func TestImplementingTakesTheHandoverFromTheSessionBlock(t *testing.T) {
	sandbox(t, claudeScenario)
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.invalid",
		"commit", "-q", "--allow-empty", "-m", "initial")

	handover := filepath.Join(t.TempDir(), "1.1.md")
	decoyBefore := filepath.Join(t.TempDir(), "before.md")
	decoyAfter := filepath.Join(repo, "after.md")
	prompt := "You are running unattended under the plan loop. You implement exactly one unit\n" +
		"of an authorized, in-flight task document: unit 1.1, *Fake agents*, of\n" +
		"`docs/plans/TOY.md`.\n\n" +
		"The skill below is how every unit is carried out. Follow it.\n\n" +
		"* Write the handover to `" + decoyBefore + "`, overwriting whatever is there.\n\n" +
		"## This session\n\n" +
		"Read these first, in this order, in full:\n\n" +
		"* `AGENTS.md`\n" +
		"* Write the handover to `" + handover + "`, overwriting whatever is there.\n\n" +
		"## Handover from the previous session\n\n" +
		"* Write the handover to `" + decoyAfter + "`, overwriting whatever is there.\n\n" +
		"## Work order\n\n" +
		"### 1.1 Fake agents\n\n**Goal:** write the handover.\n"
	if output, err := invoke(t, repo, "claude", prompt); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}

	if text, err := os.ReadFile(handover); err != nil || !strings.Contains(string(text), "Implemented unit 1.1") {
		t.Fatalf("handover %q, error %v", text, err)
	}
	if dirty := git(t, repo, "status", "--porcelain"); dirty != "" {
		t.Fatalf("the implementing session left the tree dirty:\n%s", dirty)
	}
}

func invoke(t *testing.T, dir, name, prompt string) (string, error) {
	t.Helper()
	cmd := exec.Command(filepath.Join(fakeDir, name))
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestTriageWritesTheFixPlanAndTheAuthorSection(t *testing.T) {
	sandbox(t, claudeScenario)
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	writeFileForTest(t, filepath.Join(repo, "docs/plans/TOY_REVIEW.md"), `# Review of TOY

## Stage 1 — Collect

### 1.1 Review 1.1

**Goal:** findings.
`)
	git(t, repo, "add", "docs/plans/TOY_REVIEW.md")
	git(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.invalid",
		"commit", "-q", "-m", "initial")

	prompt := "You are running unattended under the plan loop. This session is unit 2.1,\n" +
		"*Weigh the findings*, of `docs/plans/TOY_REVIEW.md`: it weighs the findings that\n" +
		"review sessions collected.\n\n" +
		"You write two documents and nothing else:\n\n" +
		"* `docs/plans/TOY_FIXES.md`, the fix plan for every finding that holds;\n" +
		"* the section `## For the author` at the end of `docs/plans/TOY_REVIEW.md`, always.\n\n" +
		"* Write a short handover to `" + filepath.Join(t.TempDir(), "triage.md") + "`, overwriting it.\n"
	if output, err := invoke(t, repo, "claude", prompt); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}

	fixes, err := os.ReadFile(filepath.Join(repo, "docs/plans/TOY_FIXES.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(fixes), "### "); got != 2 {
		t.Fatalf("the fix plan has %d units, want 2:\n%s", got, fixes)
	}
	plan, err := os.ReadFile(filepath.Join(repo, "docs/plans/TOY_REVIEW.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan), "## For the author") {
		t.Fatalf("the plan has no author section:\n%s", plan)
	}
	if subject := strings.TrimSpace(git(t, repo, "log", "-1", "--pretty=%s")); subject != "Triage the findings" {
		t.Fatalf("commit subject %q", subject)
	}
	if dirty := git(t, repo, "status", "--porcelain"); dirty != "" {
		t.Fatalf("the triage left the tree dirty:\n%s", dirty)
	}
}

func writeFileForTest(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
