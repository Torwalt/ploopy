// Package opencode drives opencode running DeepSeek as one unattended session.
//
// Output is read as `--format json`, one event a line, so tool calls, token
// use, cost and the session's identity come from structured events. The
// outcome is still read out of the final text, and a usage limit out of its
// wording. It is also the only harness that bills by the hour, so it declares
// peak windows.
package opencode

import (
	"context"
	"encoding/json"
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

	argv := []string{"opencode", "run", "--format", "json", "--auto", "--agent", "plan-unit"}
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
	proc    *harness.Proc
	events  chan harness.Event
	done    chan struct{}
	outcome harness.Outcome

	// said is the agent's text and anything printed outside the event stream;
	// the marker and a usage limit are read from it.
	said []string
}

// event is one line of `--format json`. Fields ploopy does not name are
// ignored, so a new opencode release cannot break it.
type event struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionID"`
	Part      part            `json:"part"`
	Error     json.RawMessage `json:"error"`
}

type part struct {
	Text  string `json:"text"`
	Tool  string `json:"tool"`
	State struct {
		Input json.RawMessage `json:"input"`
		Time  struct {
			Start int64 `json:"start"`
			End   int64 `json:"end"`
		} `json:"time"`
	} `json:"state"`
	Tokens struct {
		Input     int `json:"input"`
		Output    int `json:"output"`
		Reasoning int `json:"reasoning"`
		Cache     struct {
			Read  int `json:"read"`
			Write int `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Cost float64 `json:"cost"`
}

func (s *session) Events() <-chan harness.Event { return s.events }

func (s *session) Kill() { s.proc.Kill() }

func (s *session) read() {
	defer close(s.done)
	defer close(s.events)
	for line := range s.proc.Lines() {
		text := strings.TrimSpace(harness.StripANSI(line))
		if text == "" {
			continue
		}
		var parsed event
		if !strings.HasPrefix(text, "{") || json.Unmarshal([]byte(text), &parsed) != nil {
			s.said = append(s.said, text)
			s.events <- harness.Event{Kind: harness.EventRaw, Text: text}
			continue
		}
		if parsed.SessionID != "" {
			s.outcome.SessionID = parsed.SessionID
		}
		s.handle(parsed)
	}
}

func (s *session) handle(e event) {
	switch e.Type {
	case "text":
		if text := strings.TrimSpace(e.Part.Text); text != "" {
			s.said = append(s.said, text)
			s.events <- harness.Event{Kind: harness.EventText, Text: text}
		}
	case "tool_use":
		// opencode reports a call once it is over, with its own clock.
		text := harness.Summarise(e.Part.State.Input)
		s.events <- harness.Event{Kind: harness.EventToolUse, Tool: e.Part.Tool, Text: text}
		if took := e.Part.State.Time.End - e.Part.State.Time.Start; e.Part.State.Time.Start > 0 && took >= 0 {
			s.events <- harness.Event{
				Kind: harness.EventToolDone, Tool: e.Part.Tool, Text: text, Took: time.Duration(took) * time.Millisecond,
			}
		}
	case "step_finish":
		// Each step reports its own use; the session's is the sum.
		tokens := e.Part.Tokens
		s.outcome.Tokens = s.outcome.Tokens.Add(harness.Tokens{
			Input: tokens.Input, Output: tokens.Output, Reasoning: tokens.Reasoning,
			CacheRead: tokens.Cache.Read, CacheWrite: tokens.Cache.Write,
		})
		s.outcome.CostUSD += e.Part.Cost
		s.outcome.Turns++
		s.events <- harness.Event{
			Kind: harness.EventUsage, CostUSD: s.outcome.CostUSD, Tokens: s.outcome.Tokens.Total(),
		}
	case "error":
		if message := errorText(e.Error); message != "" {
			s.said = append(s.said, message)
			s.events <- harness.Event{Kind: harness.EventNotice, Text: message}
		}
	}
}

// errorText finds the message in an error event, whatever shape the
// provider's error took.
func errorText(raw json.RawMessage) string {
	var shaped struct {
		Name    string `json:"name"`
		Message string `json:"message"`
		Data    struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &shaped) == nil {
		for _, text := range []string{shaped.Data.Message, shaped.Message, shaped.Name} {
			if strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return strings.TrimSpace(plain)
	}
	return strings.TrimSpace(string(raw))
}

func (s *session) Wait() (harness.Outcome, error) {
	<-s.done
	exit, timedOut := s.proc.Wait()
	said := strings.Join(s.said, "\n")

	outcome := s.outcome
	outcome.Marker, outcome.Reason = harness.ParseMarker(said)
	outcome.Exit, outcome.TimedOut = exit, timedOut
	if outcome.Marker == harness.None {
		outcome.RateLimit = harness.RateLimitFromText(said, time.Now())
	}
	return outcome, nil
}
