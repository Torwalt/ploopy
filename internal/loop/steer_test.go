package loop

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/fake"
	"github.com/Torwalt/ploopy/internal/state"
)

// steering is the author changing a run from outside it.
type steering struct {
	mu      sync.Mutex
	now     Steer
	changed chan struct{}
}

func newSteering(initial Steer) *steering {
	return &steering{now: initial, changed: make(chan struct{}, 1)}
}

func (s *steering) Steer() Steer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

func (s *steering) Changed() <-chan struct{} { return s.changed }

func (s *steering) set(change func(*Steer)) {
	s.mu.Lock()
	change(&s.now)
	s.mu.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// changingDuringSleep is a sleep the author interrupts with a change; it
// returns once the loop ends the wait.
func changingDuringSleep(f *fixture, s *steering, change func(*Steer)) func(context.Context, time.Duration) error {
	return func(ctx context.Context, d time.Duration) error {
		f.slept = append(f.slept, d)
		s.set(change)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return nil
		}
	}
}

func TestAStopEndsTheRunOnceTheCurrentUnitIsDone(t *testing.T) {
	s := newSteering(Steer{})
	f := setup(t, twoUnits,
		fake.Step{Do: func(dir string) error {
			s.set(func(now *Steer) { now.Stop = true })
			return commits("one.go", "first")(dir)
		}, Outcome: done()},
	)
	f.opts.Steering = s

	result := f.run()
	if result.Status != "done" || !strings.Contains(result.Message, "stopped after 1.1") {
		t.Fatalf("run %+v", result)
	}
	if f.state().Status("1.1") != state.Landed || f.state().Status("1.2") != state.Pending {
		t.Fatal("the unit under way should land and the next stay open")
	}
}

func TestASecondaryNamedDuringALimitWaitTakesOverAtOnce(t *testing.T) {
	s := newSteering(Steer{})
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{RateLimit: &harness.RateLimit{Reason: "usage limit"}}},
	)
	other := fake.New(
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	other.Named = "secondary"
	f.opts.Steering = s
	f.opts.Sleep = changingDuringSleep(f, s, func(now *Steer) {
		now.Secondary = &Agent{Harness: other, Effort: "high"}
	})

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if f.h.Started() != 1 || other.Started() != 2 {
		t.Fatalf("primary ran %d sessions, secondary %d", f.h.Started(), other.Started())
	}
}

func TestAStopAskedForDuringALimitWaitEndsIt(t *testing.T) {
	s := newSteering(Steer{})
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{RateLimit: &harness.RateLimit{Reason: "usage limit"}}},
	)
	f.opts.Steering = s
	f.opts.Sleep = changingDuringSleep(f, s, func(now *Steer) { now.Stop = true })

	result := f.run()
	if result.Status != "done" || !strings.Contains(result.Message, "stopped during 1.1") {
		t.Fatalf("run %+v", result)
	}
	if f.h.Started() != 1 {
		t.Fatalf("%d sessions after the stop", f.h.Started())
	}
}

func TestRunningThroughPeakHoursChosenDuringTheWaitEndsIt(t *testing.T) {
	f, _ := peakFixture(t, time.Date(2026, 10, 2, 2, 30, 0, 0, time.UTC), peakWindow)
	s := newSteering(Steer{WaitOffPeak: true})
	f.opts.Steering = s
	f.opts.Sleep = changingDuringSleep(f, s, func(now *Steer) { now.WaitOffPeak = false })

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 1 || f.h.Started() != 2 {
		t.Fatalf("slept %v, %d sessions", f.slept, f.h.Started())
	}
}
