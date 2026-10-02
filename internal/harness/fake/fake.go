// Package fake is a scripted harness for tests: it acts on the repository the
// way a session would, then reports whatever outcome the test asked for.
package fake

import (
	"context"
	"sync"

	"github.com/Torwalt/ploopy/internal/harness"
)

// Step is one session: what it does to the repository, what it says while it
// does it, and how it ends.
type Step struct {
	Do      func(dir string) error
	Events  []harness.Event
	Outcome harness.Outcome
	Err     error
}

// Harness hands out scripted sessions in order. The last step repeats once the
// script runs out, so a test need not count retries it does not care about.
type Harness struct {
	Steps   []Step
	Windows []harness.Window

	mu      sync.Mutex
	started int
	Prompts []string
	Specs   []harness.Spec
}

// New builds a harness from a script.
func New(steps ...Step) *Harness { return &Harness{Steps: steps} }

// Name is what a plan's front matter would call this harness.
func (*Harness) Name() string { return "fake" }

// Efforts offered.
func (*Harness) Efforts() []string { return []string{"high"} }

// Models offered.
func (*Harness) Models() []string { return []string{"fake-model"} }

// PeakWindows the test asked for.
func (h *Harness) PeakWindows() []harness.Window { return h.Windows }

// Started counts the sessions the loop has asked for.
func (h *Harness) Started() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.started
}

// Start runs the next step of the script.
func (h *Harness) Start(_ context.Context, spec harness.Spec) (harness.Session, error) {
	h.mu.Lock()
	step := Step{Outcome: harness.Outcome{Marker: harness.Done}}
	if len(h.Steps) > 0 {
		if h.started < len(h.Steps) {
			step = h.Steps[h.started]
		} else {
			step = h.Steps[len(h.Steps)-1]
		}
	}
	h.started++
	h.Prompts = append(h.Prompts, spec.Prompt)
	h.Specs = append(h.Specs, spec)
	h.mu.Unlock()

	if step.Err != nil {
		return nil, step.Err
	}
	if step.Do != nil {
		if err := step.Do(spec.Dir); err != nil {
			return nil, err
		}
	}

	events := make(chan harness.Event, len(step.Events)+1)
	for _, event := range step.Events {
		events <- event
	}
	close(events)

	outcome := step.Outcome
	if outcome.SessionID == "" {
		outcome.SessionID = spec.SessionID
	}
	return &session{events: events, outcome: outcome}, nil
}

type session struct {
	events  chan harness.Event
	outcome harness.Outcome
}

func (s *session) Events() <-chan harness.Event { return s.events }

func (s *session) Wait() (harness.Outcome, error) { return s.outcome, nil }

func (s *session) Kill() {}
