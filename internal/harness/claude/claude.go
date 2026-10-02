// Package claude drives Claude Code as one unattended session.
//
// Output is read as `stream-json`, so the outcome, cost and session identity
// come from a structured result rather than from scraping a tail.
package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/guard"
	"github.com/Torwalt/ploopy/internal/harness"
)

// Harness is Claude Code.
type Harness struct{}

// New builds the adapter.
func New() *Harness { return &Harness{} }

// Name is how a plan's front matter asks for this harness.
func (*Harness) Name() string { return "claude" }

// Efforts Claude Code accepts for an unattended run.
func (*Harness) Efforts() []string { return []string{"medium", "high", "xhigh"} }

// Models offered, the default first. Any model name still works as an
// override; this is what the selection form shows.
func (*Harness) Models() []string { return []string{"sonnet", "opus"} }

// PeakWindows is empty: Anthropic does not bill by hour.
func (*Harness) PeakWindows() []harness.Window { return nil }

// Start runs one session. The prompt goes on stdin, which has no size limit.
func (h *Harness) Start(ctx context.Context, spec harness.Spec) (harness.Session, error) {
	argv := []string{
		"claude", "-p",
		// stream-json refuses to run with --print unless --verbose is given.
		"--output-format", "stream-json", "--verbose",
		"--permission-mode", "bypassPermissions",
	}
	if spec.Model != "" {
		argv = append(argv, "--model", spec.Model)
	}
	if spec.Effort != "" {
		argv = append(argv, "--effort", spec.Effort)
	}
	if spec.SessionID != "" {
		argv = append(argv, "--session-id", spec.SessionID)
	}
	if spec.Title != "" {
		argv = append(argv, "--name", spec.Title)
	}
	if len(spec.FallbackModels) > 0 {
		argv = append(argv, "--fallback-model", strings.Join(spec.FallbackModels, ","))
	}
	if spec.MaxBudgetUSD > 0 {
		argv = append(argv, "--max-budget-usd", fmt.Sprintf("%g", spec.MaxBudgetUSD))
	}
	if len(spec.Deny) > 0 {
		argv = append(argv, "--disallowedTools")
		argv = append(argv, spec.Deny...)
	}
	if settings := hookSettings(); settings != "" {
		argv = append(argv, "--settings", settings)
	}
	argv = append(argv, spec.Extra...)

	proc, err := harness.StartProc(ctx, argv, spec.Dir, strings.NewReader(spec.Prompt), spec.LogPath, spec.Timeout)
	if err != nil {
		return nil, err
	}

	s := &session{
		proc: proc, events: make(chan harness.Event, 64),
		done: make(chan struct{}), id: spec.SessionID,
	}
	go s.read()
	return s, nil
}

// hookSettings asks Claude Code to run ploopy itself as a PreToolUse hook, so
// a refused git command is refused by inspection rather than by matching the
// string it was written as.
func hookSettings() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{
				"matcher": "Bash",
				"hooks":   []any{map[string]any{"type": "command", "command": self + " guard"}},
			}},
		},
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return ""
	}
	return string(encoded)
}

type session struct {
	proc    *harness.Proc
	events  chan harness.Event
	done    chan struct{}
	id      string
	outcome harness.Outcome

	// said is what the agent wrote, decoded. A session killed before its
	// result event still left its final message here.
	said []string
}

func (s *session) Events() <-chan harness.Event { return s.events }

func (s *session) Kill() { s.proc.Kill() }

func (s *session) Wait() (harness.Outcome, error) {
	<-s.done
	exit, timedOut := s.proc.Wait()
	s.outcome.Exit = exit
	s.outcome.TimedOut = timedOut
	said := strings.Join(s.said, "\n")
	if s.outcome.Marker == harness.None && !timedOut {
		s.outcome.Marker, s.outcome.Reason = harness.ParseMarker(said)
	}
	if s.outcome.RateLimit == nil && s.outcome.Exhausted == "" {
		s.outcome.RateLimit = harness.RateLimitFromText(said, time.Now())
	}
	if s.outcome.SessionID == "" {
		s.outcome.SessionID = s.id
	}
	return s.outcome, nil
}

// streamLine is the subset of a stream-json object ploopy reads. Fields it
// does not name are ignored, so a new Claude Code release cannot break it.
type streamLine struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	SessionID       string          `json:"session_id"`
	EstimatedTokens int             `json:"estimated_tokens"`
	Message         json.RawMessage `json:"message"`

	IsError        bool            `json:"is_error"`
	Result         string          `json:"result"`
	NumTurns       int             `json:"num_turns"`
	TotalCostUSD   float64         `json:"total_cost_usd"`
	StopReason     string          `json:"stop_reason"`
	TerminalReason string          `json:"terminal_reason"`
	APIErrorStatus json.RawMessage `json:"api_error_status"`
}

type assistantMessage struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
}

func (s *session) read() {
	defer close(s.done)
	defer close(s.events)

	for line := range s.proc.Lines() {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var parsed streamLine
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			s.events <- harness.Event{Kind: harness.EventRaw, Text: line}
			continue
		}
		switch parsed.Type {
		case "assistant":
			s.assistant(parsed)
		case "system":
			if parsed.EstimatedTokens > 0 {
				s.events <- harness.Event{Kind: harness.EventUsage, Tokens: parsed.EstimatedTokens}
			}
		case "result":
			s.result(parsed)
		}
	}
}

func (s *session) assistant(parsed streamLine) {
	var message assistantMessage
	if json.Unmarshal(parsed.Message, &message) != nil {
		return
	}
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				s.said = append(s.said, text)
				s.events <- harness.Event{Kind: harness.EventText, Text: text}
			}
		case "tool_use":
			s.events <- harness.Event{
				Kind: harness.EventToolUse,
				Tool: block.Name,
				Text: summarise(block.Input),
			}
		}
	}
}

func (s *session) result(parsed streamLine) {
	marker, reason := harness.ParseMarker(parsed.Result)
	s.outcome.Marker = marker
	s.outcome.Reason = reason
	s.outcome.SessionID = parsed.SessionID
	s.outcome.CostUSD = parsed.TotalCostUSD
	s.outcome.Turns = parsed.NumTurns
	if parsed.TotalCostUSD > 0 {
		s.events <- harness.Event{Kind: harness.EventUsage, CostUSD: parsed.TotalCostUSD}
	}

	if !parsed.IsError {
		return
	}
	signals := strings.ToLower(strings.Join([]string{
		parsed.Subtype, parsed.StopReason, parsed.TerminalReason, parsed.Result,
	}, " "))
	switch {
	case strings.Contains(signals, "budget"):
		s.outcome.Exhausted = "the session reached the spending cap ploopy set"
	case strings.Contains(string(parsed.APIErrorStatus), "429"),
		strings.Contains(signals, "rate_limit"),
		strings.Contains(signals, "rate limit"),
		strings.Contains(signals, "usage limit"):
		s.outcome.RateLimit = &harness.RateLimit{Reason: firstLine(parsed.Result)}
		if limit := harness.RateLimitFromText(parsed.Result, time.Now()); limit != nil {
			s.outcome.RateLimit = limit
		}
	}
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if line == "" {
		return "the harness reported a usage limit"
	}
	return line
}

// summarise reduces a tool's input to the one value worth watching scroll by.
func summarise(input json.RawMessage) string {
	var fields map[string]any
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	for _, key := range []string{"command", "file_path", "path", "pattern", "url", "description"} {
		if value, ok := fields[key].(string); ok && value != "" {
			return truncate(value, 120)
		}
	}
	return ""
}

func truncate(text string, limit int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= limit {
		return text
	}
	return text[:limit-1] + "…"
}

// Guard answers a PreToolUse hook: it reads the tool call on stdin and refuses
// the git commands a session must never run.
func Guard(stdin []byte) (bool, string) {
	var call struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(stdin, &call) != nil {
		return false, ""
	}
	if call.ToolInput.Command == "" {
		return false, ""
	}
	return guard.Check(call.ToolInput.Command)
}
