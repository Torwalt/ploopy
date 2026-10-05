package main

import (
	"time"

	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/ui"
)

// watchEvery is how often a run looks for what the author changed.
const watchEvery = 2 * time.Second

// steering lets `ploopy adjust` reach a run: it announces the run, tells the
// loop what the author wants now, and says so in the run's feed.
type steering struct {
	run       *control.Run
	base      control.Settings
	harnesses harness.Set
	changed   chan struct{}
	stop      chan struct{}
}

// announce registers the run. Without a runtime directory the run goes on
// unannounced, keeping what it started with.
func announce(status control.Status, harnesses harness.Set, report *ui.Reporter) *steering {
	s := &steering{base: status.Settings, harnesses: harnesses, changed: make(chan struct{}, 1)}
	run, err := control.Register(status)
	if err != nil {
		report.Say("`ploopy adjust` cannot reach this run: %v", err)
		return s
	}
	s.run, s.stop = run, make(chan struct{})
	go run.Watch(s.stop, watchEvery, func(now control.Settings) {
		report.Say("adjusted: %s", now.Describe())
		select {
		case s.changed <- struct{}{}:
		default:
		}
	})
	return s
}

// close withdraws the announcement.
func (s *steering) close() {
	if s.run == nil {
		return
	}
	close(s.stop)
	s.run.Close()
}

// wanted is what the author wants now.
func (s *steering) wanted() control.Settings {
	if s.run == nil {
		return s.base
	}
	return s.run.Wanted()
}

// loopSteering is what the loop reads, or nil for a run nobody can reach.
func (s *steering) loopSteering() loop.Steering {
	if s.run == nil {
		return nil
	}
	return s
}

// Steer answers the loop.
func (s *steering) Steer() loop.Steer {
	wanted := s.wanted()
	return loop.Steer{
		WaitOffPeak: wanted.Peak == "wait",
		Secondary:   toLoopAgent(s.harnesses, wanted.SecondaryAgent()),
		Stop:        wanted.Stops(),
	}
}

// Changed fires when the author changed something.
func (s *steering) Changed() <-chan struct{} { return s.changed }

// tracked tells the registry which unit the run is on.
type tracked struct {
	*ui.Reporter
	steer *steering
}

func (t tracked) UnitStart(u plan.Unit, attempt int, logPath string) {
	if t.steer.run != nil {
		t.steer.run.SetUnit(u.ID)
	}
	t.Reporter.UnitStart(u, attempt, logPath)
}

func toControlAgent(agent *loop.Agent) *control.Agent {
	if agent == nil {
		return nil
	}
	return &control.Agent{Harness: agent.Harness.Name(), Model: agent.Model, Effort: agent.Effort}
}

// toLoopAgent resolves a named agent. A harness this build does not know is
// no agent at all.
func toLoopAgent(harnesses harness.Set, agent *control.Agent) *loop.Agent {
	if agent == nil {
		return nil
	}
	h := harnesses.Get(agent.Harness)
	if h == nil {
		return nil
	}
	return &loop.Agent{Harness: h, Model: agent.Model, Effort: agent.Effort}
}

func peakName(waitOffPeak bool) string {
	if waitOffPeak {
		return "wait"
	}
	return "run"
}
