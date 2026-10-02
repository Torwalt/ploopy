// Package opencode drives opencode running DeepSeek as one unattended session.
//
// opencode reports in prose, so this is the degraded path: the outcome is read
// out of the text and a usage limit out of its wording. It is also the only
// harness that bills by the hour, so it declares peak windows.
package opencode

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
)

// MaxArgumentBytes is roughly where the kernel stops accepting one argument.
// opencode takes the whole prompt as one, so a long plan fails at exec time
// unless it is caught first.
const MaxArgumentBytes = 120_000

// Harness is opencode.
type Harness struct{}

// New builds the adapter.
func New() *Harness { return &Harness{} }

// Name is how a plan's front matter asks for this harness.
func (*Harness) Name() string { return "opencode" }

// Efforts opencode calls variants.
func (*Harness) Efforts() []string { return []string{"high", "max"} }

// Models offered, the default first. An empty model leaves the choice to
// `opencode.json`.
func (*Harness) Models() []string {
	return []string{"deepseek/deepseek-v4-pro", "deepseek/deepseek-flash"}
}

// PeakWindows is when DeepSeek bills double: 01:00-04:00 and 06:00-10:00 UTC,
// Monday to Friday. Everything else, weekends included, is half price.
// Chinese public holidays are also off-peak and are deliberately ignored; the
// only cost is pausing on a day that was already cheap.
func (*Harness) PeakWindows() []harness.Window {
	weekdays := []time.Weekday{
		time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday,
	}
	return []harness.Window{
		{Days: weekdays, Start: 1 * 60, End: 4 * 60},
		{Days: weekdays, Start: 6 * 60, End: 10 * 60},
	}
}

// Start runs one session, with the prompt as a single argument.
func (h *Harness) Start(ctx context.Context, spec harness.Spec) (harness.Session, error) {
	if size := len(spec.Prompt); size > MaxArgumentBytes {
		return nil, fmt.Errorf(
			"the prompt is %d bytes; opencode takes it as one argument, which the kernel caps near %d",
			size, MaxArgumentBytes)
	}

	argv := []string{"opencode", "run", "--auto", "--agent", "plan-unit"}
	if spec.Model != "" {
		argv = append(argv, "--model", spec.Model)
	}
	if spec.Effort != "" {
		argv = append(argv, "--variant", spec.Effort)
	}
	if spec.Title != "" {
		argv = append(argv, "--title", spec.Title)
	}
	argv = append(argv, spec.Extra...)
	argv = append(argv, spec.Prompt)

	proc, err := harness.StartProc(ctx, argv, spec.Dir, nil, spec.LogPath, spec.Timeout)
	if err != nil {
		return nil, err
	}

	s := &session{proc: proc, events: make(chan harness.Event, 64), done: make(chan struct{})}
	go s.read()
	return s, nil
}

type session struct {
	proc   *harness.Proc
	events chan harness.Event
	done   chan struct{}
}

func (s *session) Events() <-chan harness.Event { return s.events }

func (s *session) Kill() { s.proc.Kill() }

func (s *session) read() {
	defer close(s.done)
	defer close(s.events)
	for line := range s.proc.Lines() {
		if text := strings.TrimSpace(harness.StripANSI(line)); text != "" {
			s.events <- harness.Event{Kind: harness.EventText, Text: text}
		}
	}
}

func (s *session) Wait() (harness.Outcome, error) {
	<-s.done
	exit, timedOut := s.proc.Wait()
	tail := strings.Join(s.proc.Tail(), "\n")

	marker, reason := harness.ParseMarker(tail)
	outcome := harness.Outcome{
		Marker: marker, Reason: reason,
		Exit: exit, TimedOut: timedOut,
	}
	if marker == harness.None {
		outcome.RateLimit = harness.RateLimitFromText(tail, time.Now())
	}
	return outcome, nil
}
