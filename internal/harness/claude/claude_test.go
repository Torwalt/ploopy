package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
)

// fakeClaude puts a script named `claude` on PATH that records its arguments
// and prints the stream-json lines the test wants.
func fakeClaude(t *testing.T, lines string) string {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + args + "\ncat <<'STREAM'\n" + lines + "\nSTREAM\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return args
}

func start(t *testing.T, spec harness.Spec) (harness.Outcome, []harness.Event) {
	t.Helper()
	if spec.Timeout == 0 {
		spec.Timeout = 30 * time.Second
	}
	if spec.Dir == "" {
		spec.Dir = t.TempDir()
	}
	session, err := New().Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	var events []harness.Event
	for event := range session.Events() {
		events = append(events, event)
	}
	outcome, err := session.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return outcome, events
}

const successStream = `{"type":"system","subtype":"init","session_id":"abc"}
{"type":"system","subtype":"tokens","estimated_tokens":1200,"session_id":"abc"}
{"type":"assistant","session_id":"abc","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"assistant","session_id":"abc","message":{"content":[{"type":"text","text":"Implemented the unit.\n\nDONE"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"Implemented the unit.\n\nDONE","num_turns":4,"duration_ms":1490,"total_cost_usd":1.25,"usage":{"input_tokens":10,"cache_creation_input_tokens":22015,"cache_read_input_tokens":300,"output_tokens":45,"output_tokens_details":{"thinking_tokens":37}},"session_id":"abc"}`

// The outcome comes from the structured result, not from scraping a tail.
func TestTheOutcomeComesFromTheResultEvent(t *testing.T) {
	fakeClaude(t, successStream)

	outcome, _ := start(t, harness.Spec{Prompt: "do the unit", SessionID: "abc"})
	if outcome.Marker != harness.Done {
		t.Fatalf("marker %q", outcome.Marker)
	}
	if outcome.CostUSD != 1.25 || outcome.Turns != 4 {
		t.Fatalf("cost %v turns %d", outcome.CostUSD, outcome.Turns)
	}
	// Thinking is part of the output count and is not counted twice.
	want := harness.Tokens{Input: 10, Output: 8, Reasoning: 37, CacheRead: 300, CacheWrite: 22015}
	if outcome.Tokens != want {
		t.Fatalf("tokens %+v, want %+v", outcome.Tokens, want)
	}
	if outcome.SessionID != "abc" {
		t.Fatalf("session id %q", outcome.SessionID)
	}
	if outcome.Exit != 0 || outcome.TimedOut {
		t.Fatalf("exit %d timedOut %v", outcome.Exit, outcome.TimedOut)
	}
	if outcome.RateLimit != nil {
		t.Fatalf("a successful session reported a limit: %+v", outcome.RateLimit)
	}
}

func TestToolUsesAndTextBecomeEvents(t *testing.T) {
	fakeClaude(t, successStream)

	_, events := start(t, harness.Spec{Prompt: "do the unit", SessionID: "abc"})
	var tools, texts int
	var tokens, cost float64
	for _, event := range events {
		switch event.Kind {
		case harness.EventToolUse:
			tools++
			if event.Tool != "Bash" || !strings.Contains(event.Text, "go test") {
				t.Fatalf("tool event %+v", event)
			}
		case harness.EventText:
			texts++
		case harness.EventUsage:
			tokens = max(tokens, float64(event.Tokens))
			cost = max(cost, event.CostUSD)
		}
	}
	if tools != 1 || texts != 1 {
		t.Fatalf("tools %d texts %d", tools, texts)
	}
	if tokens != 1200 || cost != 1.25 {
		t.Fatalf("the run was not costed: tokens %v cost %v", tokens, cost)
	}
}

func TestTheSessionIsStartedTheWayAnUnattendedRunNeeds(t *testing.T) {
	argsFile := fakeClaude(t, successStream)
	start(t, harness.Spec{
		Prompt: "do the unit", Model: "sonnet", Effort: "high",
		SessionID: "abc", Title: "ploopy PASS 1.1",
		MaxBudgetUSD: 5,
		Deny:         []string{"WebFetch"},
	})

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	joined := strings.Join(args, " ")

	// stream-json refuses to run with --print unless --verbose is given.
	for _, want := range []string{
		"-p", "--output-format stream-json", "--verbose",
		"--permission-mode bypassPermissions",
		"--model sonnet", "--effort high", "--session-id abc",
		"--max-budget-usd 5",
		"--disallowedTools WebFetch",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the session was started without %q:\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, "--settings") || !strings.Contains(joined, "PreToolUse") {
		t.Fatalf("the guard hook was not installed:\n%s", joined)
	}
}

func TestAUsageLimitIsNotABlock(t *testing.T) {
	fakeClaude(t, `{"type":"result","subtype":"error_rate_limit","is_error":true,`+
		`"result":"You have reached your usage limit. It resets at 4pm.","session_id":"abc"}`)

	outcome, _ := start(t, harness.Spec{Prompt: "x", SessionID: "abc"})
	if outcome.Marker == harness.Blocked {
		t.Fatal("a usage limit must never be read as a block")
	}
	if outcome.RateLimit == nil {
		t.Fatal("the usage limit was not recognised")
	}
	if outcome.RateLimit.ResetAt.IsZero() {
		t.Fatal("the reset time was not read")
	}
}

func TestTheSpendingCapIsADeliberateStop(t *testing.T) {
	fakeClaude(t, `{"type":"result","subtype":"error_budget_exceeded","is_error":true,`+
		`"result":"The session exceeded its budget.","session_id":"abc"}`)

	outcome, _ := start(t, harness.Spec{Prompt: "x", SessionID: "abc"})
	if outcome.Exhausted == "" {
		t.Fatal("a spending cap should stop the run deliberately")
	}
	if outcome.RateLimit != nil {
		t.Fatal("a spending cap is not something to wait out")
	}
}

func TestOutputThatIsNotJsonDoesNotCrashTheAdapter(t *testing.T) {
	fakeClaude(t, "not json at all\n"+successStream)

	outcome, events := start(t, harness.Spec{Prompt: "x", SessionID: "abc"})
	if outcome.Marker != harness.Done {
		t.Fatalf("marker %q", outcome.Marker)
	}
	var raw int
	for _, event := range events {
		if event.Kind == harness.EventRaw {
			raw++
		}
	}
	if raw != 1 {
		t.Fatalf("%d raw events, want 1", raw)
	}
}

func TestASessionThatNeverReportsAResultFallsBackToItsText(t *testing.T) {
	fakeClaude(t, `{"type":"assistant","session_id":"abc","message":{"content":[{"type":"text","text":"I cannot go on.\n\nBLOCKED the premise is wrong"}]}}`)

	outcome, _ := start(t, harness.Spec{Prompt: "x", SessionID: "abc"})
	if outcome.Marker != harness.Blocked || outcome.Reason != "the premise is wrong" {
		t.Fatalf("outcome %+v", outcome)
	}
	if outcome.SessionID != "abc" {
		t.Fatalf("session id %q", outcome.SessionID)
	}
}

func TestTheGuardReadsAToolCall(t *testing.T) {
	denied, reason := Guard([]byte(`{"tool_name":"Bash","tool_input":{"command":"git -C . push"}}`))
	if !denied || reason == "" {
		t.Fatalf("denied=%v reason=%q", denied, reason)
	}
	if denied, _ := Guard([]byte(`{"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)); denied {
		t.Fatal("ordinary work was refused")
	}
	if denied, _ := Guard([]byte(`{"tool_name":"Read","tool_input":{}}`)); denied {
		t.Fatal("a tool call with no command was refused")
	}
}

// Claude Code reports no times, so a call runs from when it was seen until its
// result is.
func TestAToolCallIsTimedUntilItsResult(t *testing.T) {
	fakeClaude(t, `{"type":"assistant","session_id":"abc","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"just test"}}]}}
{"type":"user","session_id":"abc","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}}
{"type":"user","session_id":"abc","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_unknown","content":"ok"}]}}`)

	_, events := start(t, harness.Spec{Prompt: "x", SessionID: "abc"})
	var done []harness.Event
	for _, event := range events {
		if event.Kind == harness.EventToolDone {
			done = append(done, event)
		}
	}
	if len(done) != 1 || done[0].Tool != "Bash" || done[0].Text != "just test" || done[0].Took < 0 {
		t.Fatalf("finished calls %+v", done)
	}
}
