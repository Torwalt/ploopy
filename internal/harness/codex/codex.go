package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
)

type Harness struct{}

func New() *Harness                { return &Harness{} }
func (*Harness) Name() string      { return "codex" }
func (*Harness) Efforts() []string { return []string{"low", "medium", "high", "xhigh", "max"} }
func (*Harness) Models() []string {
	return []string{"gpt-6.1-sol", "gpt-6-astra", "gpt-6-luna", "gpt-6-sol", "gpt-5.6-sol"}
}
func (*Harness) PeakWindows() []harness.Window { return nil }

func (*Harness) Start(ctx context.Context, spec harness.Spec) (harness.Session, error) {
	if spec.MaxBudgetUSD > 0 {
		return nil, fmt.Errorf("codex cannot enforce a dollar spending cap; use a zero budget")
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	command := "'" + strings.ReplaceAll(self, "'", "'\"'\"'") + "' guard"
	hooks := "hooks.PreToolUse=[{matcher=\"^Bash$\",hooks=[{type=\"command\",command=" + strconv.Quote(command) + ",timeout=10}]}]"
	argv := []string{
		"codex", "exec", "--json", "--color", "never",
		"--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust",
		"--config", "features.hooks=true", "--config", hooks,
		"--config", "web_search=\"disabled\"",
	}
	if spec.Model != "" {
		argv = append(argv, "--model", spec.Model)
	}
	if spec.Effort != "" {
		argv = append(argv, "--config", "model_reasoning_effort="+strconv.Quote(spec.Effort))
	}
	argv = append(argv, spec.Extra...)
	argv = append(argv, "-")
	proc, err := harness.StartProc(ctx, argv, spec.Dir, strings.NewReader(spec.Prompt), spec.LogPath, spec.Timeout)
	if err != nil {
		return nil, err
	}
	s := &session{proc: proc, events: make(chan harness.Event, 64), done: make(chan struct{}), calls: map[string]call{}}
	go s.read()
	return s, nil
}

type session struct {
	proc        *harness.Proc
	events      chan harness.Event
	done        chan struct{}
	outcome     harness.Outcome
	said        []string
	diagnostics []string
	calls       map[string]call
}

type call struct {
	tool, text string
	at         time.Time
}

type event struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Message  string `json:"message"`
	Error    struct {
		Message string `json:"message"`
	} `json:"error"`
	Item  item `json:"item"`
	Usage struct {
		Input     int `json:"input_tokens"`
		Cached    int `json:"cached_input_tokens"`
		Output    int `json:"output_tokens"`
		Reasoning int `json:"reasoning_output_tokens"`
	} `json:"usage"`
}

type item struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Command   string          `json:"command"`
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Query     string          `json:"query"`
	Changes   []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"changes"`
}

func (s *session) Events() <-chan harness.Event { return s.events }
func (s *session) Kill()                        { s.proc.Kill() }

func (s *session) read() {
	defer close(s.done)
	defer close(s.events)
	for line := range s.proc.Lines() {
		text := strings.TrimSpace(harness.StripANSI(line))
		if text == "" {
			continue
		}
		var e event
		if json.Unmarshal([]byte(text), &e) != nil || e.Type == "" {
			s.diagnostics = append(s.diagnostics, text)
			s.events <- harness.Event{Kind: harness.EventRaw, Text: text}
			continue
		}
		s.handle(e)
	}
}

func (s *session) handle(e event) {
	switch e.Type {
	case "thread.started":
		s.outcome.SessionID = e.ThreadID
	case "turn.completed":
		u := e.Usage
		cached := min(u.Cached, u.Input)
		reasoning := min(u.Reasoning, u.Output)
		s.outcome.Tokens = s.outcome.Tokens.Add(harness.Tokens{
			Input: u.Input - cached, CacheRead: cached, Output: u.Output - reasoning, Reasoning: reasoning,
		})
		s.outcome.Turns++
		s.events <- harness.Event{Kind: harness.EventUsage, Tokens: s.outcome.Tokens.Total()}
	case "turn.failed", "error":
		message := e.Error.Message
		if message == "" {
			message = e.Message
		}
		s.notice(message)
	case "item.started", "item.completed":
		if e.Item.Type == "agent_message" && e.Type == "item.completed" {
			if text := strings.TrimSpace(e.Item.Text); text != "" {
				s.said = append(s.said, text)
				s.events <- harness.Event{Kind: harness.EventText, Text: text}
			}
			return
		}
		if e.Item.Type == "error" && e.Type == "item.completed" {
			s.notice(e.Item.Text)
			return
		}
		s.tool(e)
	}
}

func (s *session) notice(message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	s.diagnostics = append(s.diagnostics, message)
	s.events <- harness.Event{Kind: harness.EventNotice, Text: message}
}

func (s *session) tool(e event) {
	i := e.Item
	var tool, text string
	switch i.Type {
	case "command_execution":
		tool, text = "Bash", i.Command
	case "mcp_tool_call":
		tool, text = "mcp__"+i.Server+"__"+i.Tool, harness.Summarise(i.Arguments)
	case "file_change":
		tool = "apply_patch"
		var paths []string
		for _, change := range i.Changes {
			paths = append(paths, change.Kind+" "+change.Path)
		}
		text = strings.Join(paths, ", ")
	case "web_search":
		tool, text = "web_search", i.Query
	default:
		return
	}
	pending, found := s.calls[i.ID]
	if !found {
		pending = call{tool: tool, text: text, at: time.Now()}
		s.events <- harness.Event{Kind: harness.EventToolUse, Tool: tool, Text: text}
	}
	if e.Type == "item.started" {
		s.calls[i.ID] = pending
		return
	}
	delete(s.calls, i.ID)
	took := time.Duration(0)
	if found {
		took = time.Since(pending.at)
	}
	if text == "" {
		text = pending.text
	}
	s.events <- harness.Event{Kind: harness.EventToolDone, Tool: tool, Text: text, Took: took}
}

func (s *session) Wait() (harness.Outcome, error) {
	<-s.done
	exit, timedOut := s.proc.Wait()
	outcome := s.outcome
	outcome.Exit, outcome.TimedOut = exit, timedOut
	outcome.Marker, outcome.Reason = harness.ParseMarker(strings.Join(s.said, "\n"))
	if (outcome.Marker == harness.Done || outcome.Marker == harness.NothingToDo) && exit == 0 {
		return outcome, nil
	}
	diagnostics := append(append([]string(nil), s.diagnostics...), s.said...)
	text := strings.Join(diagnostics, "\n")
	outcome.RateLimit = harness.RateLimitFromText(text, time.Now())
	if outcome.RateLimit == nil {
		for _, message := range diagnostics {
			lower := strings.ToLower(message)
			if strings.Contains(lower, "usage limit") || strings.Contains(lower, "rate limit") || strings.Contains(lower, "rate_limit") || strings.Contains(lower, "quota exceeded") {
				outcome.RateLimit = &harness.RateLimit{Reason: message}
			}
		}
	}
	return outcome, nil
}
