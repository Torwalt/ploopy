package loop

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/fake"
)

type routedHarness struct {
	*fake.Harness
	harness.Preferences
}

func routeFixture(t *testing.T, steps ...fake.Step) (*fixture, *time.Time) {
	t.Helper()
	f := setup(t, twoUnits, steps...)
	h := &routedHarness{Harness: f.h, Preferences: harness.Preferences{
		Fallbacks: map[string]string{"fake-model": "api-model"},
	}}
	f.opts.Harness = h
	clock := time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)
	f.opts.Now = func() time.Time { return clock }
	f.opts.Sleep = func(_ context.Context, d time.Duration) error {
		f.slept = append(f.slept, d)
		clock = clock.Add(d)
		return nil
	}
	return f, &clock
}

func TestTheSameModelFallsBackWithoutSpendingAnAttemptOrLosingWork(t *testing.T) {
	cut := limited(time.Date(2026, 10, 6, 3, 0, 0, 0, time.UTC))
	cut.Do = commits("half.go", "half")
	f, _ := routeFixture(t, cut,
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()})
	f.opts.Retries = 0
	f.opts.Secondary = &Agent{Harness: fake.New(), Model: "unwanted"}
	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 0 || f.h.Started() != 3 {
		t.Fatalf("slept %v, sessions %d", f.slept, f.h.Started())
	}
	for i, want := range []string{"fake-model", "api-model", "api-model"} {
		if got := f.h.Specs[i].Model; got != want {
			t.Fatalf("session %d model %s, want %s", i, got, want)
		}
	}
	if !strings.Contains(f.h.Prompts[1], cutOffMidUnit) {
		t.Fatal("the fallback was not told to continue partial work")
	}
	entry := f.state().Entry("1.1")
	if entry.Agent != "fake api-model high" || len(entry.Commits) != 2 || entry.Attempts != 1 {
		t.Fatalf("entry %+v", entry)
	}
}

func TestTheSubscriptionRouteReturnsAfterItsReset(t *testing.T) {
	var clock *time.Time
	f, c := routeFixture(t,
		limited(time.Date(2026, 10, 5, 22, 30, 0, 0, time.UTC)),
		fake.Step{Do: func(dir string) error {
			*clock = clock.Add(time.Hour)
			return commits("one.go", "first")(dir)
		}, Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()})
	clock = c
	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if f.h.Specs[1].Model != "api-model" || f.h.Specs[2].Model != "fake-model" {
		t.Fatalf("routes %+v", f.h.Specs)
	}
}

func TestAnAPILimitWaitsOnlyUntilTheSubscriptionResets(t *testing.T) {
	start := time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)
	f, _ := routeFixture(t, limited(start.Add(time.Hour)), limited(start.Add(5*time.Hour)),
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()})
	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 1 || f.slept[0] != time.Hour || f.h.Specs[2].Model != "fake-model" {
		t.Fatalf("slept %v, next model %s", f.slept, f.h.Specs[2].Model)
	}
}

func TestAUnitWithItsOwnAgentUsesThatAgentsConfiguredRoute(t *testing.T) {
	f, _ := routeFixture(t, limited(time.Time{}),
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()})
	f.opts.Harnesses = harness.Set{f.opts.Harness}
	writeFile(t, f.root, f.opts.PlanPath, strings.Replace(twoUnits, "**Goal:** the first thing.", "Agent: fake\nEffort: high\n\n**Goal:** the first thing.", 1))
	f.git("add", f.opts.PlanPath)
	f.git("commit", "-q", "-m", "choose agent")
	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if f.h.Specs[0].Model != "fake-model" || f.h.Specs[1].Model != "api-model" {
		t.Fatalf("routes %+v", f.h.Specs)
	}
}

type peakRoutedHarness struct{ *routedHarness }

func (h *peakRoutedHarness) PeakWindowsFor(model string) []harness.Window {
	if model == "fake-model" {
		return nil
	}
	return h.Harness.PeakWindows()
}

func TestTheAPIFallbackRespectsPeakPolicyAndWakesWhenTheSubscriptionReturns(t *testing.T) {
	start := time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)
	f, clock := routeFixture(t, limited(start.Add(time.Hour)),
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()})
	*clock = start
	f.h.Windows = []harness.Window{{Days: []time.Weekday{time.Monday}, Start: 60, End: 240}}
	f.opts.Harness = &peakRoutedHarness{f.opts.Harness.(*routedHarness)}
	f.opts.WaitOffPeak = true
	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 1 || f.slept[0] != time.Hour || f.h.Specs[1].Model != "fake-model" {
		t.Fatalf("slept %v, next model %s", f.slept, f.h.Specs[1].Model)
	}
}
