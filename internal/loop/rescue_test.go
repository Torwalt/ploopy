package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/fake"
	"github.com/Torwalt/ploopy/internal/history"
	"github.com/Torwalt/ploopy/internal/state"
)

// rescue writes the report the loop reads after a rescue session.
func rescue(id string, n int, text string) func(string) error {
	return func(dir string) error {
		path := filepath.Join(dir, ".ploopy", "pass", "handover",
			fmt.Sprintf("%s.rescue-%d.md", id, n))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(text), 0o644)
	}
}

func rescueReport(diagnosis, decisions, next, wait string) string {
	text := "## Diagnosis\n\n" + diagnosis + "\n\n"
	if decisions != "" {
		text += "## Decisions\n\n" + decisions + "\n\n"
	}
	text += "## Next attempt\n\n" + next + "\n"
	if wait != "" {
		text += "\nWait: " + wait + "\n"
	}
	return text
}

// rescuer is a harness the run did not start with, so a rescue is easy to tell
// from a unit session.
func rescuer(t *testing.T, f *fixture, steps ...fake.Step) *fake.Harness {
	t.Helper()
	other := fake.New(steps...)
	other.Named = "rescuer"
	f.opts.Rescue = &Agent{Harness: other, Model: "rescue-model", Effort: "high"}
	f.opts.RescueSkill = "---\nname: unit-rescue\n---\n\n# rescue skill body\n"
	return other
}

func TestARescueRetryLandsTheUnit(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: done()}, // DONE without a commit fails the unit
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.opts.Retries = 0
	other := rescuer(t, f, fake.Step{
		Do: rescue("1.1", 1, rescueReport("the provider returned an empty completion",
			"", "retry after the provider settles", "10m")),
		Outcome: harness.Outcome{Marker: harness.Retry, Reason: "provider stalled"},
	})

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if other.Started() != 1 {
		t.Fatalf("%d rescues ran, want 1", other.Started())
	}
	if len(f.slept) == 0 || f.slept[0] != 10*time.Minute {
		t.Fatalf("slept %v, want the report's 10m", f.slept)
	}
	if !strings.Contains(f.h.Prompts[1], "A rescue diagnosed this:") ||
		!strings.Contains(f.h.Prompts[1], "retry after the provider settles") {
		t.Fatalf("the next attempt was not told what the rescue diagnosed:\n%s", f.h.Prompts[1])
	}
	for _, want := range []string{"### 1.1 One", "# rescue skill body", "### Attempt 1"} {
		if !strings.Contains(other.Prompts[0], want) {
			t.Fatalf("the rescue prompt lacks %q:\n%s", want, other.Prompts[0])
		}
	}
}

func TestARescueDecisionLandsTheUnitAndKeepsTheDecisions(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is wrong"}},
		fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is still wrong"}},
		fake.Step{Outcome: done()}, // DONE without a commit fails, then retries
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.opts.Retries = 1
	other := rescuer(t, f, fake.Step{
		Do: rescue("1.1", 1, rescueReport("the work order leaves the clock open",
			"Use the repository's existing clock, not a new one.", "wire the existing clock", "")),
		Outcome: harness.Outcome{Marker: harness.Decided, Reason: "use the existing clock", CostUSD: 0.25},
	})

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if other.Started() != 1 {
		t.Fatalf("%d rescues ran, want 1", other.Started())
	}
	if prompt := f.h.Prompts[2]; !strings.Contains(prompt, "standing in for the author") ||
		!strings.Contains(prompt, "Use the repository's existing clock") {
		t.Fatalf("the attempt after the decision was not told about it:\n%s", prompt)
	}
	if prompt := f.h.Prompts[3]; !strings.Contains(prompt, "Use the repository's existing clock") {
		t.Fatalf("the attempt after a later failure lost the decision:\n%s", prompt)
	}
	entry := f.state().Entry("1.1")
	if entry == nil || entry.Status != state.Landed {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
	if len(entry.Rescues) != 1 {
		t.Fatalf("unit 1.1 recorded %d rescues, want 1: %+v", len(entry.Rescues), entry.Rescues)
	}
	rescue := entry.Rescues[0]
	if rescue.Trigger != "blocked" || rescue.Verdict != "decided" ||
		rescue.Reason != "use the existing clock" || rescue.Agent != "rescuer rescue-model high" ||
		!strings.Contains(rescue.Report, "Use the repository's existing clock") {
		t.Fatalf("the rescue record is %+v", rescue)
	}
	if rescue.CostUSD != 0.25 || entry.CostUSD != 0.25 {
		t.Fatalf("rescue cost %.2f, unit cost %.2f", rescue.CostUSD, entry.CostUSD)
	}
}

func TestRescueDecisionsSurviveAUsageLimitCut(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is wrong"}},
		fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is still wrong"}},
		fake.Step{Do: commits("partial.go", "partial"), Outcome: harness.Outcome{
			RateLimit: &harness.RateLimit{Reason: "usage limit"}}},
		fake.Step{Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	rescuer(t, f, fake.Step{
		Do: rescue("1.1", 1, rescueReport("the work order leaves the clock open",
			"Use the repository's existing clock, not a new one.", "wire the existing clock", "")),
		Outcome: harness.Outcome{Marker: harness.Decided, Reason: "use the existing clock"},
	})

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if prompt := f.h.Prompts[3]; !strings.Contains(prompt, "Use the repository's existing clock") {
		t.Fatalf("the session after a usage limit lost the decision:\n%s", prompt)
	}
}

func TestRescueDecisionsSurviveAStoppedRun(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is wrong"}},
		fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is still wrong"}},
		fake.Step{Do: func(dir string) error {
			stop()
			return commits("partial.go", "partial")(dir)
		}, Outcome: harness.Outcome{Exit: -1}},
		fake.Step{Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	rescuer(t, f, fake.Step{
		Do: rescue("1.1", 1, rescueReport("the work order leaves the clock open",
			"Use the repository's existing clock, not a new one.", "wire the existing clock", "")),
		Outcome: harness.Outcome{Marker: harness.Decided, Reason: "use the existing clock"},
	})

	if result := f.runUntilStopped(ctx); result.Status != "failed" {
		t.Fatalf("stopped run %+v", result)
	}
	if result := f.run(); result.Status != "done" {
		t.Fatalf("second run %+v", result)
	}
	prompt := f.h.Prompts[3]
	if !strings.Contains(prompt, stoppedMidUnit) ||
		!strings.Contains(prompt, "Use the repository's existing clock") {
		t.Fatalf("the session after a stopped run lost the decision:\n%s", prompt)
	}
}

func TestARescueBlockStopsTheRunWithItsReason(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is wrong"}},
		fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is still wrong"}},
	)
	other := rescuer(t, f, fake.Step{
		Do: rescue("1.1", 1, rescueReport("the work needs a credential nobody has",
			"", "ask the author for the credential", "")),
		Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the author must supply the credential"},
	})

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "the author must supply the credential") {
		t.Fatalf("run %+v", result)
	}
	if other.Started() != 1 {
		t.Fatalf("%d rescues ran, want 1", other.Started())
	}
	entry := f.state().Entry("1.1")
	if entry == nil || entry.Status != state.Blocked || entry.Reason != "the author must supply the credential" {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
}

func TestALandedUnitKeepsTheRescuesFromAnEarlierBlockedSettlement(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is wrong"}},
		fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the premise is still wrong"}},
		fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "still no credential"}},
		fake.Step{Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "still no credential, again"}},
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	rescuer(t, f,
		fake.Step{
			Do: rescue("1.1", 1, rescueReport("the work needs a credential nobody has",
				"", "ask the author for the credential", "")),
			Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the author must supply the credential"},
		},
		fake.Step{
			Do:      rescue("1.1", 1, rescueReport("the provider failed", "", "start again", "")),
			Outcome: harness.Outcome{Marker: harness.Retry, Reason: "provider failed"},
		},
	)

	if result := f.run(); result.Status != "failed" {
		t.Fatalf("first run %+v", result)
	}
	if entry := f.state().Entry("1.1"); entry == nil || len(entry.Rescues) != 1 {
		t.Fatalf("the blocked unit recorded %+v", entry)
	}

	if result := f.run(); result.Status != "done" {
		t.Fatalf("second run %+v", result)
	}
	entry := f.state().Entry("1.1")
	if entry == nil || entry.Status != state.Landed {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
	if len(entry.Rescues) != 2 {
		t.Fatalf("the landed unit recorded %d rescues, want 2: %+v", len(entry.Rescues), entry.Rescues)
	}
	if entry.Rescues[0].Verdict != "blocked" || entry.Rescues[1].Verdict != "retry" {
		t.Fatalf("the unit kept %+v", entry.Rescues)
	}
}

func TestARescueBlockRecordsWhatTheSessionCommitted(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "half of it"), Outcome: harness.Outcome{
			Marker: harness.Blocked, Reason: "the premise is wrong"}},
		fake.Step{Outcome: harness.Outcome{
			Marker: harness.Blocked, Reason: "the premise is still wrong"}},
	)
	rescuer(t, f, fake.Step{
		Do:      rescue("1.1", 1, rescueReport("the rest needs the author", "", "ask the author", "")),
		Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the author must decide"},
	})

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "the author must decide") {
		t.Fatalf("run %+v", result)
	}
	entry := f.state().Entry("1.1")
	if entry == nil || entry.Status != state.Blocked {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
	if len(entry.Commits) != 1 || !strings.Contains(entry.Commits[0], "half of it") {
		t.Fatalf("the blocked unit did not record its commit: %+v", entry.Commits)
	}
}

func TestARescueBlockAfterAFailedCheckRecordsTheCommit(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{
		Do: commits("one.go", "half of it"), Outcome: done()})
	f.opts.Retries = 0
	f.opts.VerifyCmd = "test ! -f one.go"
	rescuer(t, f, fake.Step{
		Do:      rescue("1.1", 1, rescueReport("the check cannot pass yet", "", "ask the author", "")),
		Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the author must decide"},
	})

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "the author must decide") {
		t.Fatalf("run %+v", result)
	}
	entry := f.state().Entry("1.1")
	if entry == nil || entry.Status != state.Blocked {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
	if len(entry.Commits) != 1 || !strings.Contains(entry.Commits[0], "half of it") {
		t.Fatalf("the blocked unit did not record its commit: %+v", entry.Commits)
	}
}

func TestARescueBlockAfterSpentRetriesSettlesTheUnitBlocked(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Outcome: done()})
	f.opts.Retries = 0
	rescuer(t, f, fake.Step{
		Do:      rescue("1.1", 1, rescueReport("the unit cannot proceed", "", "anything", "")),
		Outcome: harness.Outcome{Marker: harness.Blocked, Reason: "the unit needs a decision only the author can make"},
	})

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "only the author can make") {
		t.Fatalf("run %+v", result)
	}
	entry := f.state().Entry("1.1")
	if entry == nil || entry.Status != state.Blocked || entry.Reason != "the unit needs a decision only the author can make" {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
}

func TestARetryFromAUnitSessionIsOnlyAMissingMarker(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{
		Do:      commits("one.go", "first"),
		Outcome: harness.Outcome{Marker: harness.Retry, Reason: "provider stalled"},
	})
	f.opts.Retries = 0

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "did not end with DONE") {
		t.Fatalf("run %+v", result)
	}
	if entry := f.state().Entry("1.1"); entry != nil {
		t.Fatalf("a RETRY unit session settled: %+v", entry)
	}
}

func TestARescueRunsOnTheTransientTrigger(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{Exit: 1}},
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.opts.TransientLimit = 1
	f.opts.Backoff = 3 * time.Minute
	other := rescuer(t, f, fake.Step{
		Do: rescue("1.1", 1, rescueReport("the provider exited without doing any work",
			"", "start again", "")),
		Outcome: harness.Outcome{Marker: harness.Retry, Reason: "provider failed"},
	})

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if other.Started() != 1 {
		t.Fatalf("%d rescues ran, want 1", other.Started())
	}
	if len(f.slept) == 0 || f.slept[0] != 3*time.Minute {
		t.Fatalf("slept %v, want the backoff", f.slept)
	}
}

func TestAtMostTwoRescuesRunForOneUnit(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Outcome: harness.Outcome{Exit: 1}})
	f.opts.TransientLimit = 1
	other := rescuer(t, f,
		fake.Step{
			Do:      rescue("1.1", 1, rescueReport("still failing", "", "try once more", "")),
			Outcome: harness.Outcome{Marker: harness.Retry, Reason: "provider failed"},
		},
		fake.Step{
			Do:      rescue("1.1", 2, rescueReport("still failing again", "", "try once more", "")),
			Outcome: harness.Outcome{Marker: harness.Retry, Reason: "provider failed again"},
		},
	)

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "without touching") {
		t.Fatalf("run %+v", result)
	}
	if other.Started() != 2 {
		t.Fatalf("%d rescues ran, want 2", other.Started())
	}
}

func TestARescueThatCommitsFailsAndLeavesItsCommit(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Outcome: done()})
	f.opts.Retries = 0
	rescuer(t, f, fake.Step{
		Do:      commits("rescued.txt", "a rescue commit"),
		Outcome: harness.Outcome{Marker: harness.Retry, Reason: "provider failed"},
	})

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "committed") {
		t.Fatalf("run %+v", result)
	}
	if !strings.Contains(f.git("log", "--oneline"), "a rescue commit") {
		t.Fatal("the rescue's commit was reverted")
	}
}

func TestARescueThatLeavesTheTreeDirtyFailsAndLeavesIt(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Outcome: done()})
	f.opts.Retries = 0
	rescuer(t, f, fake.Step{
		Do:      leaves("rescued.txt"),
		Outcome: harness.Outcome{Marker: harness.Retry, Reason: "provider failed"},
	})

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "dirty") {
		t.Fatalf("run %+v", result)
	}
	if _, err := os.Stat(filepath.Join(f.root, "rescued.txt")); err != nil {
		t.Fatal("the rescue's change was reverted")
	}
}

func TestARescueDoesNotFailForDirtTheUnitLeftBeforeIt(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: leaves("partial.txt"), Outcome: done()}, // DONE without a commit fails
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.opts.Retries = 0
	other := rescuer(t, f, fake.Step{
		// The rescue answers without touching the tree the unit left dirty.
		Do:      rescue("1.1", 1, rescueReport("the unit ran out of attempts", "", "finish the unit", "")),
		Outcome: harness.Outcome{Marker: harness.Retry, Reason: "the unit still has work to do"},
	})

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if other.Started() != 1 {
		t.Fatalf("%d rescues ran, want 1", other.Started())
	}
	if entry := f.state().Entry("1.1"); entry == nil || entry.Status != state.Landed {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
}

func TestARescueWithNoMarkerStopsTheRun(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Outcome: done()})
	f.opts.Retries = 0
	rescuer(t, f, fake.Step{
		Do:      rescue("1.1", 1, rescueReport("no answer", "", "nothing", "")),
		Outcome: harness.Outcome{},
	})

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "RETRY, DECIDED or BLOCKED") {
		t.Fatalf("run %+v", result)
	}
}

func TestARescueWaitIsCappedAtTwoHours(t *testing.T) {
	for _, tc := range []struct {
		wait string
		want time.Duration
	}{
		{"90m", 90 * time.Minute},
		{"3h", 2 * time.Hour},
	} {
		t.Run(tc.wait, func(t *testing.T) {
			f := setup(t, twoUnits,
				fake.Step{Outcome: done()},
				fake.Step{Do: commits("one.go", "first"), Outcome: done()},
				fake.Step{Do: commits("two.go", "second"), Outcome: done()},
			)
			f.opts.Retries = 0
			rescuer(t, f, fake.Step{
				Do:      rescue("1.1", 1, rescueReport("stalled", "", "retry", tc.wait)),
				Outcome: harness.Outcome{Marker: harness.Retry, Reason: "provider stalled"},
			})

			if result := f.run(); result.Status != "done" {
				t.Fatalf("run %+v", result)
			}
			if len(f.slept) == 0 || f.slept[0] != tc.want {
				t.Fatalf("slept %v, want %v", f.slept, tc.want)
			}
		})
	}
}

func TestNoRescueLeavesTheStopAsItWas(t *testing.T) {
	f := setup(t, twoUnits, fake.Step{Outcome: done()})
	f.opts.Retries = 0

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "failed after 1 attempt") {
		t.Fatalf("run %+v", result)
	}
}

func TestARescueIsMarkedInStatsAndHistoryAndLeftOutOfEstimates(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: done()},
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.opts.Retries = 0
	f.opts.History = history.Path(t.TempDir())
	rescuer(t, f, fake.Step{
		Do:      rescue("1.1", 1, rescueReport("stalled", "", "retry", "")),
		Outcome: harness.Outcome{Marker: harness.Retry, Reason: "provider stalled"},
	})

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}

	rescues := 0
	for _, r := range f.records() {
		if r.Kind != "session" {
			continue
		}
		if r.Agent == "rescuer rescue-model high" {
			rescues++
			if r.Role != "rescue" {
				t.Fatalf("rescue stats record has role %q", r.Role)
			}
		} else if r.Role != "" {
			t.Fatalf("a unit session has role %q", r.Role)
		}
	}
	if rescues != 1 {
		t.Fatalf("%d rescue records, want 1", rescues)
	}

	records, err := history.Load(f.opts.History)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range records {
		if r.Role == "rescue" {
			found = true
		}
	}
	if !found {
		t.Fatal("the history has no rescue record")
	}
	if e := history.For(records, "rescuer rescue-model high"); e.Units != 0 {
		t.Fatalf("the estimate counted rescue sessions: %+v", e)
	}
}

func TestSteeringTurnsTheRescueOffAtTheNextStopPoint(t *testing.T) {
	f := setup(t, twoUnits)
	f.opts.Retries = 0
	other := rescuer(t, f)
	s := newSteering(Steer{Rescue: &Agent{Harness: other, Model: "rescue-model", Effort: "high"}})
	f.opts.Steering = s
	f.h.Steps = append(f.h.Steps, fake.Step{Do: func(string) error {
		s.set(func(now *Steer) { now.Rescue = nil })
		return nil
	}, Outcome: done()})

	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "failed after 1 attempt") {
		t.Fatalf("run %+v", result)
	}
	if other.Started() != 0 {
		t.Fatalf("%d rescues ran after the rescue was turned off", other.Started())
	}
}
