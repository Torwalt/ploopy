package loop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/fake"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/state"
)

const twoUnits = `---
test: no
---

# Pass

Preamble.

## Stage 1 — Only

### 1.1 One

**Goal:** the first thing.

**Done when:** it is done.

### 1.2 Two

**Goal:** the second thing.

**Done when:** it is done.
`

type fixture struct {
	t    *testing.T
	root string
	h    *fake.Harness
	opts Options
	// slept records what the loop waited for instead of really waiting.
	slept []time.Duration
}

func setup(t *testing.T, planText string, steps ...fake.Step) *fixture {
	t.Helper()
	root := t.TempDir()
	runGit(t, root, "init", "-q", "-b", "work")
	runGit(t, root, "config", "user.name", "Test")
	runGit(t, root, "config", "user.email", "test@example.com")
	writeFile(t, root, "AGENTS.md", "# rules\n")
	writeFile(t, root, "docs/plans/PASS.md", planText)
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-q", "-m", "first")

	f := &fixture{t: t, root: root, h: fake.New(steps...)}
	f.opts = Options{
		PlanPath:     "docs/plans/PASS.md",
		Retries:      1,
		BlockRetries: 1,
		Harness:      f.h,
		Model:        "fake-model",
		Effort:       "high",
		Skill:        "---\nname: plan-unit\n---\n\n# skill body\n",
		BaseContext:  []string{"AGENTS.md"},
		Now:          time.Now,
		Sleep: func(_ context.Context, d time.Duration) error {
			f.slept = append(f.slept, d)
			return nil
		},
	}
	return f
}

func (f *fixture) run() Result {
	f.t.Helper()
	runner, err := New(f.root, f.opts, &silent{})
	if err != nil {
		f.t.Fatal(err)
	}
	return runner.Run(context.Background())
}

func (f *fixture) state() *state.State {
	f.t.Helper()
	s, err := state.Load(filepath.Join(f.root, "docs/plans/PASS.state.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	return runGit(f.t, f.root, args...)
}

type silent struct{}

func (*silent) Say(string, ...any)               {}
func (*silent) Fail(string, ...any)              {}
func (*silent) UnitStart(plan.Unit, int, string) {}
func (*silent) UnitDone(plan.Unit, Verdict)      {}
func (*silent) Event(harness.Event)              {}

func runGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, root, path, text string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commits is a session that does a unit's work and commits it.
func commits(name, message string) func(string) error {
	return func(dir string) error {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("work\n"), 0o644); err != nil {
			return err
		}
		for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", message}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, out)
			}
		}
		return nil
	}
}

func leaves(name string) func(string) error {
	return func(dir string) error {
		return os.WriteFile(filepath.Join(dir, name), []byte("uncommitted\n"), 0o644)
	}
}

func done() harness.Outcome { return harness.Outcome{Marker: harness.Done} }

func TestUnitsLandAndProgressIsCommitted(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "do the first thing"), Outcome: done()},
		fake.Step{Do: commits("two.go", "do the second thing"), Outcome: done()},
	)
	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}

	s := f.state()
	for _, id := range []string{"1.1", "1.2"} {
		entry := s.Entry(id)
		if entry == nil || entry.Status != state.Landed {
			t.Fatalf("unit %s is %+v", id, entry)
		}
		if len(entry.Commits) != 1 {
			t.Fatalf("unit %s recorded %v", id, entry.Commits)
		}
		if entry.Attempts != 1 {
			t.Fatalf("unit %s took %d attempts", id, entry.Attempts)
		}
	}
	if log := f.git("log", "--oneline"); !strings.Contains(log, "record PASS progress") {
		t.Fatalf("progress was not committed:\n%s", log)
	}
	if status := f.git("status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("the run left the tree dirty:\n%s", status)
	}
}

func TestANonzeroExitAfterDoneStillLands(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "first"), Outcome: harness.Outcome{Marker: harness.Done, Exit: 1}},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.run()

	entry := f.state().Entry("1.1")
	if entry == nil || entry.Status != state.Landed {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
	if len(entry.Notes) == 0 || !strings.Contains(entry.Notes[0], "exited 1") {
		t.Fatalf("the exit code was not noted: %+v", entry.Notes)
	}
}

func TestDoneWithoutACommitIsAFailure(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Outcome: done()})
	f.opts.Retries = 0

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "nothing was committed") {
		t.Fatalf("run %+v", result)
	}
	if f.state().Status("1.1") != state.Pending {
		t.Fatal("a failed unit must stay open")
	}
}

func TestNothingToDoLandsWithoutACommit(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{Marker: harness.NothingToDo, Reason: "already at HEAD"}},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.run()

	entry := f.state().Entry("1.1")
	if entry == nil || entry.Status != state.Landed || entry.Outcome != "nothing-to-do" {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
}

func TestARetryThatFindsTheWorkCommittedLandsWithoutANewCommit(t *testing.T) {
	f := setup(t, twoUnits,
		// The first attempt commits but leaves the tree dirty, so it fails.
		fake.Step{Do: func(dir string) error {
			if err := commits("one.go", "first")(dir); err != nil {
				return err
			}
			return leaves("stray.txt")(dir)
		}, Outcome: done()},
		// The second tidies up and reports that the outcome already exists.
		fake.Step{Do: func(dir string) error {
			return os.Remove(filepath.Join(dir, "stray.txt"))
		}, Outcome: harness.Outcome{Marker: harness.NothingToDo, Reason: "already committed"}},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.run()

	entry := f.state().Entry("1.1")
	if entry == nil || entry.Status != state.Landed {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
	if entry.Attempts != 2 {
		t.Fatalf("attempts %d, want 2", entry.Attempts)
	}
	if len(entry.Commits) != 1 {
		t.Fatalf("the first attempt's commit should still count: %v", entry.Commits)
	}
}

func TestADirtyTreeAfterASessionFails(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Do: leaves("stray.txt"), Outcome: done()})
	f.opts.Retries = 0

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "dirty") {
		t.Fatalf("run %+v", result)
	}
}

func TestADirtyTreeTheLoopDidNotLeaveIsRefused(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Do: commits("one.go", "first"), Outcome: done()})
	writeFile(t, f.root, "the-author-was-here.txt", "mine\n")

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "did not leave it so") {
		t.Fatalf("run %+v", result)
	}
	if f.h.Started() != 0 {
		t.Fatal("no session should have started")
	}
}

func TestAnAuthorsFileIsNotTheSessionsDirt(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: func(dir string) error {
			if err := commits("one.go", "first")(dir); err != nil {
				return err
			}
			// The author keeps working while the loop runs.
			return leaves("NOTES.md")(dir)
		}, Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.opts.AuthorPaths = []string{"NOTES.md"}

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if f.state().Status("1.1") != state.Landed {
		t.Fatal("the author's own file stopped the unit landing")
	}
}

func TestAnAuthorCommitIsNotTheUnitsWork(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Do: func(dir string) error {
		return commits("NOTES.md", "the author's note")(dir)
	}, Outcome: done()})
	f.opts.AuthorPaths = []string{"NOTES.md"}
	f.opts.Retries = 0

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "nothing was committed") {
		t.Fatalf("a commit of only the author's files should not count: %+v", result)
	}
}

// A block is the one claim the repository cannot answer, so two sessions must
// agree on it before the run stops.
func TestABlockTwoSessionsAgreeOnStopsTheRun(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{
			Marker: harness.Blocked, Reason: "the premise is wrong"}},
		fake.Step{Outcome: harness.Outcome{
			Marker: harness.Blocked, Reason: "the premise is still wrong"}},
	)

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "still wrong") {
		t.Fatalf("run %+v", result)
	}
	if f.h.Started() != 2 {
		t.Fatalf("%d sessions ran; a block needs a second opinion", f.h.Started())
	}
	if !strings.Contains(f.h.Prompts[1], "the premise is wrong") {
		t.Fatal("the second session was not told what the first claimed")
	}
	entry := f.state().Entry("1.1")
	if entry == nil || entry.Status != state.Blocked || entry.Reason != "the premise is still wrong" {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
	if entry.Attempts != 2 {
		t.Fatalf("unit 1.1 recorded %d attempts", entry.Attempts)
	}
	if !strings.Contains(strings.Join(entry.Notes, " "), "previous session blocked") {
		t.Fatalf("the first block was not recorded: %v", entry.Notes)
	}
}

func TestASecondSessionCanOverturnABlock(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{
			Marker: harness.Blocked, Reason: "the premise is wrong"}},
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if f.state().Status("1.1") != state.Landed {
		t.Fatal("the overturned block did not land")
	}
	if entry := f.state().Entry("1.1"); entry.Attempts != 2 {
		t.Fatalf("unit 1.1 recorded %d attempts", entry.Attempts)
	}
}

// A block the session's own commits contradict is still a block, but the
// record says what it left behind.
func TestABlockRecordsWhatTheSessionLeftBehind(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{
		Do:      commits("one.go", "half of it"),
		Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the rest is impossible"},
	})
	f.opts.BlockRetries = 0

	if result := f.run(); result.Status != "failed" {
		t.Fatalf("run %+v", result)
	}
	entry := f.state().Entry("1.1")
	if entry == nil || len(entry.Commits) != 1 {
		t.Fatalf("unit 1.1 recorded %+v", entry)
	}
	if !strings.Contains(strings.Join(entry.Notes, " "), "committed 1 time") {
		t.Fatalf("the commit was not noted: %v", entry.Notes)
	}
}

func TestBlockRetriesZeroBelievesTheFirstBlock(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{
		Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is wrong"},
	})
	f.opts.BlockRetries = 0

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "the premise is wrong") {
		t.Fatalf("run %+v", result)
	}
	if f.h.Started() != 1 {
		t.Fatalf("%d sessions ran, want 1", f.h.Started())
	}
	if entry := f.state().Entry("1.1"); entry == nil || entry.Status != state.Blocked {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
}

// A blocked unit is open again; the run that picks it up must not start cold.
func TestAReRunOfABlockedUnitIsToldWhatBlockedIt(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{
		Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is wrong"},
	})
	f.opts.BlockRetries = 0
	if result := f.run(); result.Status != "failed" {
		t.Fatalf("first run %+v", result)
	}

	before := f.h.Started()
	f.h.Steps = append(f.h.Steps,
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	if result := f.run(); result.Status != "done" {
		t.Fatalf("second run %+v", result)
	}
	if !strings.Contains(f.h.Prompts[before], "the premise is wrong") {
		t.Fatal("the re-run was not told what blocked the unit")
	}
	if f.state().Status("1.1") != state.Landed {
		t.Fatal("the re-run did not land the unit")
	}
}

func TestAUsageLimitWaitsAndDoesNotSpendARetry(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{RateLimit: &harness.RateLimit{Reason: "usage limit"}}},
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.opts.Retries = 0
	f.opts.LimitWait = 30 * time.Minute

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 1 || f.slept[0] != 30*time.Minute {
		t.Fatalf("slept %v", f.slept)
	}
	if entry := f.state().Entry("1.1"); entry.Attempts != 1 {
		t.Fatalf("a limit spent a retry: attempts %d", entry.Attempts)
	}
}

func TestALimitWithAResetTimeWaitsUntilIt(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{RateLimit: &harness.RateLimit{
			Reason: "usage limit", ResetAt: now.Add(90 * time.Minute)}}},
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.opts.Now = func() time.Time { return now }

	f.run()
	if len(f.slept) != 1 || f.slept[0] != 90*time.Minute {
		t.Fatalf("slept %v, want one wait of 90m", f.slept)
	}
}

func TestASessionThatChangesNothingIsTransient(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Outcome: harness.Outcome{Exit: 1}})
	f.opts.TransientLimit = 3
	f.opts.Backoff = time.Minute

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "without touching") {
		t.Fatalf("run %+v", result)
	}
	if f.h.Started() != 3 {
		t.Fatalf("%d sessions ran, want 3", f.h.Started())
	}
	if len(f.slept) != 2 {
		t.Fatalf("backed off %d times, want 2", len(f.slept))
	}
}

func TestAFlakyCheckIsRerunOnce(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	// Passes preflight, fails the unit's check once, then passes the rerun.
	counter := filepath.Join(t.TempDir(), "calls")
	f.opts.VerifyCmd = fmt.Sprintf(
		"n=$(cat %s 2>/dev/null || echo 0); n=$((n+1)); echo $n > %s; test $n -ne 2", counter, counter)

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	entry := f.state().Entry("1.1")
	if entry == nil || entry.Status != state.Landed {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
	if len(entry.Notes) == 0 || !strings.Contains(entry.Notes[0], "passed on a rerun") {
		t.Fatalf("the rerun was not noted: %v", entry.Notes)
	}
}

func TestACheckThatFailsTwiceFailsTheUnit(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Do: commits("one.go", "first"), Outcome: done()})
	f.opts.Retries = 0
	// Green at preflight, red once the unit has run.
	marker := filepath.Join(t.TempDir(), "started")
	f.opts.VerifyCmd = fmt.Sprintf("test -f %s && { echo broken; exit 1; }; touch %s", marker, marker)

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "failed after 1 attempt") {
		t.Fatalf("run %+v", result)
	}
}

func TestNoVerifyCommandMeansNoCheck(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
}

// A repository that is already red is the author's problem, not unit 1.1's.
func TestPreflightStopsARunOnAnAlreadyRedRepository(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Do: commits("one.go", "first"), Outcome: done()})
	f.opts.VerifyCmd = "echo already broken; exit 1"

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "preflight") {
		t.Fatalf("run %+v", result)
	}
	if f.h.Started() != 0 {
		t.Fatal("preflight must stop the run before any session")
	}
}

// The plan-writing skill promises an unlanded unit may be edited mid-run.
func TestThePlanIsReadAgainBetweenUnits(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: func(dir string) error {
			// While unit 1.1 runs, the author retitles unit 1.2.
			edited := strings.Replace(twoUnits, "### 1.2 Two", "### 1.2 Two, rewritten", 1)
			if err := os.WriteFile(filepath.Join(dir, "docs/plans/PASS.md"), []byte(edited), 0o644); err != nil {
				return err
			}
			return commits("one.go", "first")(dir)
		}, Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.h.Prompts) != 2 {
		t.Fatalf("%d prompts", len(f.h.Prompts))
	}
	if !strings.Contains(f.h.Prompts[1], "Two, rewritten") {
		t.Fatal("the second session was given the stale unit text")
	}
}

func TestASessionInterruptedBeforeJudgingIsJudgedOnResume(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Do: commits("one.go", "first"), Outcome: done()})
	f.opts.Until = "1.1"
	if result := f.run(); result.Status != "done" {
		t.Fatalf("first run %+v", result)
	}

	// Pretend the loop died between the session ending and the verdict.
	progressPath := filepath.Join(f.root, ".ploopy/pass/progress.json")
	base := strings.TrimSpace(f.git("rev-parse", "HEAD~1"))
	writeFile(t, f.root, ".ploopy/pass/progress.json", fmt.Sprintf(
		`{"unit":"1.1","base":%q,"session":{"head":"x","outcome":{"Marker":"DONE"},"stem":"1.1-0","tail":"DONE"}}`,
		base))
	s := f.state()
	s.Clear("1.1")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "reset progress for the test")

	before := f.h.Started()
	f.opts.Until = ""
	f.h.Steps = append(f.h.Steps, fake.Step{Do: commits("two.go", "second"), Outcome: done()})
	if result := f.run(); result.Status != "done" {
		t.Fatalf("second run %+v", result)
	}

	if f.state().Status("1.1") != state.Landed {
		t.Fatal("the unjudged session was not judged on resume")
	}
	if f.h.Started() != before+1 {
		t.Fatalf("%d extra sessions ran; the unjudged one should not be re-run",
			f.h.Started()-before)
	}
	_ = progressPath
}

func TestAReadingDocumentThatNoLongerExistsIsLeftOut(t *testing.T) {
	text := strings.Replace(twoUnits, "test: no", "test: no\ncontext: AGENTS.md gone.md", 1)
	f := setup(t, text,
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.run()

	if strings.Contains(f.h.Prompts[0], "gone.md") {
		t.Fatal("a document that does not exist was offered to the session")
	}
	if !strings.Contains(f.h.Prompts[0], "AGENTS.md") {
		t.Fatal("the context document is missing from the prompt")
	}
}

func TestTheHandoverReachesTheNextPrompt(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: func(dir string) error {
			handover := filepath.Join(dir, ".ploopy/pass/handover/1.1.md")
			if err := os.MkdirAll(filepath.Dir(handover), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(handover, []byte("the trap is in one.go\n"), 0o644); err != nil {
				return err
			}
			return commits("one.go", "first")(dir)
		}, Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.run()

	if !strings.Contains(f.h.Prompts[1], "the trap is in one.go") {
		t.Fatal("the handover did not reach the next session")
	}
	if entry := f.state().Entry("1.1"); entry == nil || !strings.Contains(entry.Handover, "the trap") {
		t.Fatalf("the handover was not recorded: %+v", entry)
	}
}

func TestAFailedAttemptTellsTheNextOneWhy(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: leaves("stray.txt"), Outcome: done()},
		fake.Step{Do: func(dir string) error {
			if err := os.Remove(filepath.Join(dir, "stray.txt")); err != nil {
				return err
			}
			return commits("one.go", "first")(dir)
		}, Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.run()

	if len(f.h.Prompts) < 2 {
		t.Fatalf("%d prompts", len(f.h.Prompts))
	}
	if !strings.Contains(f.h.Prompts[1], "failed the loop check") {
		t.Fatal("the retry was not told what went wrong")
	}
	if !strings.Contains(f.h.Prompts[1], "dirty") {
		t.Fatal("the retry was not told the tree was dirty")
	}
}

func TestMaxUnitsStopsTheRunEarly(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.opts.MaxUnits = 1

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if f.state().Status("1.2") != state.Pending {
		t.Fatal("--units 1 ran the second unit too")
	}
}

func TestACompletePlanRunsNothing(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.run()

	before := f.h.Started()
	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if f.h.Started() != before {
		t.Fatal("a complete plan started another session")
	}
}

func TestTheSessionIsGivenTheSkillAndItsWorkOrder(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.run()

	prompt := f.h.Prompts[0]
	for _, want := range []string{"# skill body", "Preamble.", "### 1.1 One", "unit 1.1"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the prompt is missing %q", want)
		}
	}
	if strings.Contains(prompt, "name: plan-unit") {
		t.Fatal("the skill's front matter leaked into the prompt")
	}
	if strings.Contains(prompt, "1.2 Two") {
		t.Fatal("the prompt carried a later unit")
	}
}

func TestEachSessionIsGivenItsOwnIdentity(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.run()

	first, second := f.h.Specs[0].SessionID, f.h.Specs[1].SessionID
	if first == "" || first == second {
		t.Fatalf("session ids %q and %q", first, second)
	}
	if entry := f.state().Entry("1.1"); entry == nil || entry.SessionID != first {
		t.Fatalf("the session id was not recorded: %+v", entry)
	}
}

func TestTheRunStopsWhenTheSpendingCapIsReached(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{
		Outcome: harness.Outcome{Exhausted: "the session reached the spending cap ploopy set"},
	})

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "spending cap") {
		t.Fatalf("run %+v", result)
	}
	if f.state().Status("1.1") != state.Pending {
		t.Fatal("a spending cap is not a block; the unit stays open")
	}
}

// Friday 2026-10-02 at 02:30 UTC is inside DeepSeek's first peak window.
var peakWindow = []harness.Window{{
	Days:  []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
	Start: 60, End: 240,
}}

func peakFixture(t *testing.T, at time.Time) *fixture {
	t.Helper()
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.h.Windows = peakWindow
	f.opts.Now = func() time.Time { return at }
	return f
}

func TestThePeakGateWaitsWhenTheAuthorSaysSo(t *testing.T) {
	f := peakFixture(t, time.Date(2026, 10, 2, 2, 30, 0, 0, time.UTC))
	asked := 0
	f.opts.Confirm = func(string) bool { asked++; return true }

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if asked == 0 {
		t.Fatal("the author was never asked")
	}
	if len(f.slept) == 0 || f.slept[0] != 90*time.Minute {
		t.Fatalf("slept %v, want a wait to 04:00 UTC", f.slept)
	}
}

func TestThePeakGateRunsAnywayWhenTheAuthorDeclines(t *testing.T) {
	f := peakFixture(t, time.Date(2026, 10, 2, 2, 30, 0, 0, time.UTC))
	f.opts.Confirm = func(string) bool { return false }

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 0 {
		t.Fatalf("declining should not wait: %v", f.slept)
	}
}

func TestOffPeakIsNeverAsked(t *testing.T) {
	f := peakFixture(t, time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC))
	asked := 0
	f.opts.Confirm = func(string) bool { asked++; return true }

	f.run()
	if asked != 0 {
		t.Fatalf("the author was asked %d times off-peak", asked)
	}
}

// Nobody is watching, so nothing is asked and nothing waits.
func TestANonInteractiveRunNeverWaitsForOffPeak(t *testing.T) {
	f := peakFixture(t, time.Date(2026, 10, 2, 2, 30, 0, 0, time.UTC))
	f.opts.Confirm = nil

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 0 {
		t.Fatalf("slept %v", f.slept)
	}
}

func TestAHarnessWithoutPeakWindowsIsNeverGated(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.opts.Now = func() time.Time { return time.Date(2026, 10, 2, 2, 30, 0, 0, time.UTC) }
	asked := 0
	f.opts.Confirm = func(string) bool { asked++; return true }

	f.run()
	if asked != 0 {
		t.Fatalf("a harness with no windows asked %d times", asked)
	}
}
