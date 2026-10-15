// Package claude drives Claude Code as one unattended session.
//
// Output is read as `stream-json`, so the outcome, cost and session identity
// come from a structured result rather than from scraping a tail.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// Budget is unknown until a session runs: Claude Code reports its usage
// windows only from inside one.
func (*Harness) Budget(context.Context) (*harness.Budget, error) { return nil, nil }

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
		done: make(chan struct{}), id: spec.SessionID, calls: map[string]call{},
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
	calls   map[string]call

	// said is what the agent wrote, decoded. A session killed before its
	// result event still left its final message here.
	said []string

	drift        harness.Drift
	resulted     bool
	subscription bool // logged in to a subscription, which reports usage windows
	windows      int  // rate_limit_events read
	rejected     *harness.RateLimit
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
	// The limit event's reset is exact; the prose is rounded and names no day.
	if limit := s.outcome.RateLimit; limit != nil && s.rejected != nil && !s.rejected.ResetAt.IsZero() {
		limit.ResetAt = s.rejected.ResetAt
	}
	if s.outcome.SessionID == "" {
		s.outcome.SessionID = s.id
	}
	if exit == 0 && !timedOut {
		s.expect()
	}
	s.outcome.Degraded = s.drift.Notes()
	return s.outcome, nil
}

// expect checks a session that ended normally for everything it reports.
func (s *session) expect() {
	if !s.resulted {
		s.drift.Missing("no result event: cost, tokens and turns are unknown")
		return
	}
	if s.outcome.SessionID == "" {
		s.drift.Missing("the result named no session")
	}
	if s.outcome.Tokens == (harness.Tokens{}) {
		s.drift.Missing("the result reported no token usage")
	}
	if s.outcome.CostUSD <= 0 {
		s.drift.Missing("the result reported no cost")
	}
	if s.subscription && s.windows == 0 {
		s.drift.Missing("no rate_limit_event: the usage windows are unknown")
	}
}

// envelope is what every stream-json object carries. A line is decoded in
// full only once its type is known, so a changed field is named, not lost.
type envelope struct {
	Type string `json:"type"`
}

// streamLine is the subset of a stream-json object ploopy reads. Fields it
// does not name are ignored.
type streamLine struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	SessionID       string          `json:"session_id"`
	EstimatedTokens int             `json:"estimated_tokens"`
	APIKeySource    string          `json:"apiKeySource"`
	Message         json.RawMessage `json:"message"`
	RateLimitInfo   *rateLimitInfo  `json:"rate_limit_info"`

	IsError        bool            `json:"is_error"`
	Result         string          `json:"result"`
	NumTurns       int             `json:"num_turns"`
	TotalCostUSD   float64         `json:"total_cost_usd"`
	Usage          usage           `json:"usage"`
	StopReason     string          `json:"stop_reason"`
	TerminalReason string          `json:"terminal_reason"`
	APIErrorStatus json.RawMessage `json:"api_error_status"`
}

// rateLimitInfo is where a subscription's usage windows stand after a request.
type rateLimitInfo struct {
	Status         string `json:"status"` // allowed, allowed_warning or rejected
	ResetsAt       int64  `json:"resetsAt"`
	RateLimitType  string `json:"rateLimitType"`
	UnifiedWindows map[string]struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    int64   `json:"resetsAt"`
	} `json:"unifiedWindows"`
}

// budget is the windows the event shows, or nil when it shows neither.
func (info *rateLimitInfo) budget(at time.Time) *harness.Budget {
	session, hasSession := info.UnifiedWindows["five_hour"]
	weekly, hasWeekly := info.UnifiedWindows["seven_day"]
	if !hasSession || !hasWeekly {
		return nil
	}
	return &harness.Budget{
		At: at,
		Session: harness.Allowance{
			Used: session.Utilization, ResetAt: time.Unix(session.ResetsAt, 0), Length: harness.SessionWindow,
		},
		Weekly: harness.Allowance{
			Used: weekly.Utilization, ResetAt: time.Unix(weekly.ResetsAt, 0), Length: harness.WeeklyWindow,
		},
	}
}

// LogBudgets reads the first and the last usage windows out of a session's
// raw transcript, for sessions recorded before ploopy kept them. Neither
// carries the instant it was seen.
func LogBudgets(transcript io.Reader) (first, last *harness.Budget) {
	lines := bufio.NewScanner(transcript)
	lines.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for lines.Scan() {
		if !strings.Contains(lines.Text(), `"rate_limit_event"`) {
			continue
		}
		var parsed streamLine
		if json.Unmarshal(lines.Bytes(), &parsed) != nil || parsed.RateLimitInfo == nil {
			continue
		}
		if budget := parsed.RateLimitInfo.budget(time.Time{}); budget != nil {
			if first == nil {
				first = budget
			}
			last = budget
		}
	}
	return first, last
}

// usage is the session's whole consumption. Thinking is part of the output
// count, so it is taken out of it rather than counted twice.
type usage struct {
	Input         int `json:"input_tokens"`
	Output        int `json:"output_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	OutputDetails struct {
		Thinking int `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

func (u usage) tokens() harness.Tokens {
	thinking := min(u.OutputDetails.Thinking, u.Output)
	return harness.Tokens{
		Input:      u.Input,
		Output:     u.Output - thinking,
		Reasoning:  thinking,
		CacheRead:  u.CacheRead,
		CacheWrite: u.CacheCreation,
	}
}

type assistantMessage struct {
	Content []struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Text      string          `json:"text"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
	} `json:"content"`
}

// call is a tool call waiting for its result. Claude Code reports no times,
// so a call runs from when it was seen until its result is.
type call struct {
	tool, text string
	at         time.Time
}

func (s *session) read() {
	defer close(s.done)
	defer close(s.events)

	for line := range s.proc.Lines() {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var head envelope
		if err := json.Unmarshal([]byte(line), &head); err != nil {
			if harness.Structured(line) {
				s.drift.Undecoded("a line", err)
			}
			s.events <- harness.Event{Kind: harness.EventRaw, Text: line}
			continue
		}
		switch head.Type {
		case "assistant", "user", "system", "result", "rate_limit_event":
		default:
			continue
		}
		var parsed streamLine
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			s.drift.Undecoded(head.Type, err)
			continue
		}
		switch parsed.Type {
		case "assistant":
			s.assistant(parsed)
		case "user":
			s.toolResults(parsed)
		case "system":
			if parsed.Subtype == "init" {
				s.subscription = parsed.APIKeySource == "none"
			}
			if parsed.EstimatedTokens > 0 {
				s.events <- harness.Event{Kind: harness.EventUsage, Tokens: parsed.EstimatedTokens}
			}
		case "rate_limit_event":
			s.rateLimit(parsed.RateLimitInfo)
		case "result":
			s.result(parsed)
		}
	}
}

// rateLimit reads where the usage windows stand.
func (s *session) rateLimit(info *rateLimitInfo) {
	if info == nil {
		s.drift.Missing("a rate_limit_event carried no rate_limit_info")
		return
	}
	s.windows++
	now := time.Now()
	if info.Status == "rejected" {
		s.rejected = &harness.RateLimit{Reason: "claude's " + strings.ReplaceAll(info.RateLimitType, "_", "-") + " usage limit"}
		if info.ResetsAt > 0 {
			s.rejected.ResetAt = time.Unix(info.ResetsAt, 0).Add(2 * time.Minute)
		}
	}
	budget := info.budget(now)
	if budget == nil {
		s.drift.Missing("rate_limit_event without the five_hour and seven_day windows")
		return
	}
	if s.outcome.BudgetBefore == nil {
		s.outcome.BudgetBefore = budget
	}
	s.outcome.Budget = budget
	s.events <- harness.Event{Kind: harness.EventBudget, Budget: budget}
}

func (s *session) assistant(parsed streamLine) {
	var message assistantMessage
	if err := json.Unmarshal(parsed.Message, &message); err != nil {
		s.drift.Undecoded("an assistant message", err)
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
			text := harness.Summarise(block.Input)
			if block.ID != "" {
				s.calls[block.ID] = call{tool: block.Name, text: text, at: time.Now()}
			}
			s.events <- harness.Event{Kind: harness.EventToolUse, Tool: block.Name, Text: text}
		}
	}
}

func (s *session) toolResults(parsed streamLine) {
	var message assistantMessage
	if err := json.Unmarshal(parsed.Message, &message); err != nil {
		s.drift.Undecoded("a tool result", err)
		return
	}
	for _, block := range message.Content {
		pending, ok := s.calls[block.ToolUseID]
		if block.Type != "tool_result" || !ok {
			continue
		}
		delete(s.calls, block.ToolUseID)
		s.events <- harness.Event{
			Kind: harness.EventToolDone, Tool: pending.tool, Text: pending.text, Took: time.Since(pending.at),
		}
	}
}

func (s *session) result(parsed streamLine) {
	s.resulted = true
	marker, reason := harness.ParseMarker(parsed.Result)
	s.outcome.Marker = marker
	s.outcome.Reason = reason
	s.outcome.SessionID = parsed.SessionID
	s.outcome.CostUSD = parsed.TotalCostUSD
	s.outcome.Tokens = parsed.Usage.tokens()
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
