package loop

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/fake"
)

// secondaryFixture runs on a clock that moves only when the loop sleeps or a
// session moves it. The primary is the fixture's own harness.
func secondaryFixture(t *testing.T, primary []fake.Step, secondary []fake.Step) (*fixture, *fake.Harness, *time.Time) {
	t.Helper()
	f := setup(t, twoUnits, primary...)
	f.h.Named = "primary"
	other := fake.New(secondary...)
	other.Named = "secondary"
	f.opts.Secondary = &Agent{Harness: other, Model: "other-model", Effort: "high"}

	clock := time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)
	f.opts.Now = func() time.Time { return clock }
	f.opts.Sleep = func(_ context.Context, d time.Duration) error {
		f.slept = append(f.slept, d)
		clock = clock.Add(d)
		return nil
	}
	return f, other, &clock
}

func limited(reset time.Time) fake.Step {
	return fake.Step{Outcome: harness.Outcome{RateLimit: &harness.RateLimit{Reason: "usage limit", ResetAt: reset}}}
}

func TestAUsageLimitHandsTheRunToTheSecondary(t *testing.T) {
	start := time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)
	f, other, _ := secondaryFixture(t,
		[]fake.Step{limited(start.Add(3 * time.Hour))},
		[]fake.Step{
			{Do: commits("one.go", "first"), Outcome: done()},
			{Do: commits("two.go", "second"), Outcome: done()},
		})

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 0 {
		t.Fatalf("the run waited %v instead of switching", f.slept)
	}
	if f.h.Started() != 1 || other.Started() != 2 {
		t.Fatalf("primary ran %d sessions, secondary %d", f.h.Started(), other.Started())
	}
	for _, id := range []string{"1.1", "1.2"} {
		if entry := f.state().Entry(id); entry.Agent != "secondary other-model high" || entry.Harness != "secondary" {
			t.Fatalf("unit %s was recorded as %q on %q", id, entry.Agent, entry.Harness)
		}
	}
	if spec := other.Specs[0]; spec.Model != "other-model" || spec.Effort != "high" {
		t.Fatalf("the secondary ran with %+v", spec)
	}
}

func TestTheRunGoesBackToThePrimaryOnceItsLimitResets(t *testing.T) {
	start := time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)
	var clock *time.Time
	f, other, c := secondaryFixture(t,
		[]fake.Step{
			limited(start.Add(30 * time.Minute)),
			{Do: commits("two.go", "second"), Outcome: done()},
		},
		[]fake.Step{{Do: func(dir string) error {
			*clock = clock.Add(time.Hour)
			return commits("one.go", "first")(dir)
		}, Outcome: done()}})
	clock = c

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if f.h.Started() != 2 || other.Started() != 1 {
		t.Fatalf("primary ran %d sessions, secondary %d", f.h.Started(), other.Started())
	}
	if entry := f.state().Entry("1.2"); !strings.HasPrefix(entry.Agent, "primary") {
		t.Fatalf("unit 1.2 ran on %q", entry.Agent)
	}
}

// When the secondary is limited too, the run waits for whichever comes back
// first and goes on with it.
func TestWhenBothAreLimitedTheRunWaitsForThePrimary(t *testing.T) {
	start := time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)
	f, other, _ := secondaryFixture(t,
		[]fake.Step{
			limited(start.Add(time.Hour)),
			{Do: commits("one.go", "first"), Outcome: done()},
			{Do: commits("two.go", "second"), Outcome: done()},
		},
		[]fake.Step{limited(start.Add(5 * time.Hour))})

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 1 || f.slept[0] != time.Hour {
		t.Fatalf("slept %v, want one wait for the primary's reset", f.slept)
	}
	if f.h.Started() != 3 || other.Started() != 1 {
		t.Fatalf("primary ran %d sessions, secondary %d", f.h.Started(), other.Started())
	}
}

// A session cut off mid-unit hands its work on: the secondary is told to
// continue from what the repository holds.
func TestTheSecondaryIsToldWhereTheCutOffSessionLeftOff(t *testing.T) {
	start := time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)
	cut := limited(start.Add(3 * time.Hour))
	cut.Do = commits("half.go", "half of the first")
	f, other, _ := secondaryFixture(t,
		[]fake.Step{cut},
		[]fake.Step{
			{Do: commits("one.go", "first"), Outcome: done()},
			{Do: commits("two.go", "second"), Outcome: done()},
		})

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if !strings.Contains(other.Prompts[0], "usage limit cut the previous session off") {
		t.Fatal("the secondary was not told the previous session was cut off")
	}
	if entry := f.state().Entry("1.1"); len(entry.Commits) != 2 {
		t.Fatalf("unit 1.1 recorded %v, want both sessions' commits", entry.Commits)
	}
}

func TestTheSessionAfterAWaitedOutLimitIsToldWhereItLeftOff(t *testing.T) {
	cut := fake.Step{Do: commits("half.go", "half of the first"),
		Outcome: harness.Outcome{RateLimit: &harness.RateLimit{Reason: "usage limit"}}}
	f := setup(t, twoUnits, cut,
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if !strings.Contains(f.h.Prompts[1], "usage limit cut the previous session off") {
		t.Fatal("the session after the wait was not told it continues cut-off work")
	}
}
