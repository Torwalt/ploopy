package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
)

// shrink makes the warming constants tens of milliseconds, keeping the ratios
// that decide an outcome: the ping comes before the margin, and a call with the
// default timeout ends well before it.
func shrink(t *testing.T) {
	t.Helper()
	was := [...]time.Duration{pingAfter, deadlineMargin, answerWait, defaultBashTimeout}
	wasCap := pingCap
	pingAfter, deadlineMargin, answerWait, defaultBashTimeout = 100*time.Millisecond, 112*time.Millisecond, 400*time.Millisecond, 40*time.Millisecond
	pingCap = 3
	t.Cleanup(func() {
		pingAfter, deadlineMargin, answerWait, defaultBashTimeout = was[0], was[1], was[2], was[3]
		pingCap = wasCap
	})
	t.Setenv("BASH_DEFAULT_TIMEOUT_MS", "")
	t.Setenv("BASH_MAX_TIMEOUT_MS", "")
}

const (
	// A main-thread call with a 150 ms timeout, after a request that read 1203
	// tokens of context.
	bashCall = `{"type":"assistant","parent_tool_use_id":null,"message":{"usage":{"input_tokens":3,"cache_read_input_tokens":1000,"cache_creation_input_tokens":200,"output_tokens":5},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"just test","timeout":150}}]}}`
	// The same with no timeout, so the default applies.
	quickBashCall = `{"type":"assistant","parent_tool_use_id":null,"message":{"usage":{"input_tokens":3,"cache_read_input_tokens":1000,"cache_creation_input_tokens":200,"output_tokens":5},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"just test"}}]}}`
	// A tool whose runtime is not known.
	agentCall  = `{"type":"assistant","parent_tool_use_id":null,"message":{"usage":{"input_tokens":3,"cache_read_input_tokens":1000,"cache_creation_input_tokens":200,"output_tokens":5},"content":[{"type":"tool_use","id":"t1","name":"Agent","input":{"prompt":"look"}}]}}`
	toolResult = `{"type":"user","parent_tool_use_id":null,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`
	resultLine = `{"type":"result","subtype":"success","is_error":false,"result":"done\n\nDONE","num_turns":2,"total_cost_usd":1,"usage":{"input_tokens":10,"output_tokens":5},"session_id":"abc"}`
)

// warmingClaude puts a `claude` on PATH that reads its stdin a line at a time.
// body runs after the prompt; in it, `reply KIND` reads one request and answers
// it as ok, error, synthetic or silent. Requests are recorded, and anything on
// stdin left unread when the session ends.
func warmingClaude(t *testing.T, body string) (requests, unread, ttl string) {
	t.Helper()
	dir := t.TempDir()
	requests, unread, ttl = filepath.Join(dir, "requests"), filepath.Join(dir, "unread"), filepath.Join(dir, "ttl")
	script := `#!/bin/sh
printf '%s' "$CLAUDE_CODE_PROMPT_CACHE_TTL" > ` + ttl + `
: > ` + requests + `
IFS= read -r prompt
reply() {
	IFS= read -r request || exit 0
	printf '%s\n' "$request" >> ` + requests + `
	id=$(printf '%s' "$request" | sed 's/.*"request_id":"\([^"]*\)".*/\1/')
	case "$1" in
	ok) printf '{"type":"control_response","response":{"subtype":"success","request_id":"%s","response":{"response":"OK","synthetic":false}}}\n' "$id" ;;
	error) printf '{"type":"control_response","response":{"subtype":"error","request_id":"%s","error":"boom"}}\n' "$id" ;;
	synthetic) printf '{"type":"control_response","response":{"subtype":"success","request_id":"%s","response":{"response":"","synthetic":true}}}\n' "$id" ;;
	esac
}
say() { printf '%s\n' "$1"; }
` + body + `
cat > ` + unread + `
`
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return requests, unread, ttl
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.FieldsFunc(string(raw), func(r rune) bool { return r == '\n' })
}

func shell(parts ...string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		if strings.HasPrefix(part, "{") {
			part = "say '" + part + "'"
		}
		quoted[i] = part
	}
	return strings.Join(quoted, "\n")
}

func TestAWaitingSessionIsPingedBeforeItsCacheExpires(t *testing.T) {
	shrink(t)
	requests, unread, _ := warmingClaude(t, shell(bashCall, "reply ok", toolResult, resultLine))

	outcome := waitWithin(t, startSession(t, "x"))
	if outcome.Marker != harness.Done || len(outcome.Degraded) != 0 {
		t.Fatalf("marker %q degraded %v", outcome.Marker, outcome.Degraded)
	}
	if outcome.Pings != 1 {
		t.Fatalf("%d pings, want 1", outcome.Pings)
	}
	// A ping reads the context the last main-thread request reported.
	if want := (harness.Tokens{CacheRead: 1203}); outcome.PingTokens != want {
		t.Fatalf("ping tokens %+v, want %+v", outcome.PingTokens, want)
	}
	// The session's own usage is not credited with them.
	if outcome.Tokens.CacheRead != 0 {
		t.Fatalf("session tokens %+v", outcome.Tokens)
	}
	sent := lines(t, requests)
	if len(sent) != 1 || !strings.Contains(sent[0], `"subtype":"side_question"`) ||
		!strings.Contains(sent[0], `"request_id":"ploopy-warm-1"`) {
		t.Fatalf("requests %q", sent)
	}
	if left := lines(t, unread); len(left) != 0 {
		t.Fatalf("unread stdin %q", left)
	}
}

func TestACallThatEndsBeforeTheCacheDoesIsNotPinged(t *testing.T) {
	shrink(t)
	requests, unread, _ := warmingClaude(t, shell(quickBashCall, "sleep 0.35", toolResult, resultLine))

	outcome := waitWithin(t, startSession(t, "x"))
	if outcome.Pings != 0 || outcome.PingTokens != (harness.Tokens{}) {
		t.Fatalf("pings %d tokens %+v", outcome.Pings, outcome.PingTokens)
	}
	if len(lines(t, requests)) != 0 || len(lines(t, unread)) != 0 {
		t.Fatal("a request was written to stdin")
	}
}

// With no deadline to wait for, a ping goes out every time the cache is about
// to expire, until a rewrite would have been as cheap.
func TestAToolWithNoKnownDeadlineIsPingedUpToTheCap(t *testing.T) {
	shrink(t)
	_, unread, _ := warmingClaude(t, shell(agentCall, "reply ok", "reply ok", "reply ok", "sleep 0.45", toolResult, resultLine))

	outcome := waitWithin(t, startSession(t, "x"))
	if outcome.Pings != pingCap {
		t.Fatalf("%d pings, want %d", outcome.Pings, pingCap)
	}
	if want := (harness.Tokens{CacheRead: 3 * 1203}); outcome.PingTokens != want {
		t.Fatalf("ping tokens %+v, want %+v", outcome.PingTokens, want)
	}
	if left := lines(t, unread); len(left) != 0 {
		t.Fatalf("a ping past the cap: %q", left)
	}
	if len(outcome.Degraded) != 0 {
		t.Fatalf("degraded %v", outcome.Degraded)
	}
}

func TestAnErrorAnswerStopsWarming(t *testing.T) {
	for _, kind := range []string{"error", "synthetic"} {
		t.Run(kind, func(t *testing.T) {
			shrink(t)
			_, unread, _ := warmingClaude(t, shell(agentCall, "reply "+kind, "sleep 0.45", toolResult, resultLine))

			outcome := waitWithin(t, startSession(t, "x"))
			if outcome.Pings != 1 {
				t.Fatalf("%d pings, want 1", outcome.Pings)
			}
			if joined := strings.Join(outcome.Degraded, "\n"); !strings.Contains(joined, "cache warming stopped") ||
				!strings.Contains(joined, kind) {
				t.Fatalf("degraded %v", outcome.Degraded)
			}
			if left := lines(t, unread); len(left) != 0 {
				t.Fatalf("pinged again after stopping: %q", left)
			}
			if outcome.Marker != harness.Done {
				t.Fatalf("marker %q", outcome.Marker)
			}
		})
	}
}

func TestAMissingAnswerStopsWarming(t *testing.T) {
	shrink(t)
	_, unread, _ := warmingClaude(t, shell(agentCall, "reply silent", "sleep 0.7", toolResult, resultLine))

	outcome := waitWithin(t, startSession(t, "x"))
	if outcome.Pings != 1 {
		t.Fatalf("%d pings, want 1", outcome.Pings)
	}
	if joined := strings.Join(outcome.Degraded, "\n"); !strings.Contains(joined, "cache warming stopped") ||
		!strings.Contains(joined, "no answer") {
		t.Fatalf("degraded %v", outcome.Degraded)
	}
	if left := lines(t, unread); len(left) != 0 {
		t.Fatalf("pinged again after stopping: %q", left)
	}
}

func TestTheSessionRunsWithTheShortCache(t *testing.T) {
	_, _, ttl := warmingClaude(t, shell(resultLine))
	waitWithin(t, startSession(t, "x"))
	raw, err := os.ReadFile(ttl)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "5m" {
		t.Fatalf("CLAUDE_CODE_PROMPT_CACHE_TTL=%q", raw)
	}
}

// The hour's cache needs no pings: a session that the default would ping is
// left alone.
func TestTheHourCacheSetsItAndSendsNoPing(t *testing.T) {
	shrink(t)
	requests, unread, ttl := warmingClaude(t, shell(bashCall, "sleep 0.5", toolResult, resultLine))
	h := New()
	h.Cache = CacheHour
	session, err := h.Start(context.Background(), harness.Spec{Dir: t.TempDir(), Prompt: "x", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	outcome := waitWithin(t, session)
	if outcome.Marker != harness.Done || len(outcome.Degraded) != 0 {
		t.Fatalf("marker %q degraded %v", outcome.Marker, outcome.Degraded)
	}
	if outcome.Pings != 0 || outcome.PingTokens != (harness.Tokens{}) {
		t.Fatalf("pings %d tokens %+v", outcome.Pings, outcome.PingTokens)
	}
	if raw, err := os.ReadFile(ttl); err != nil || string(raw) != "1h" {
		t.Fatalf("CLAUDE_CODE_PROMPT_CACHE_TTL=%q (%v)", raw, err)
	}
	if sent := lines(t, requests); len(sent) != 0 {
		t.Fatalf("requests %q", sent)
	}
	if left := lines(t, unread); len(left) != 0 {
		t.Fatalf("unread stdin %q", left)
	}
}

func decode(t *testing.T, line string) streamLine {
	t.Helper()
	var parsed streamLine
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed
}

// A subagent's lines are about its own requests: they neither renew the cache
// the main thread waits on nor count as its calls.
func TestASubagentsLinesDoNotTouchTheClock(t *testing.T) {
	s := &session{calls: map[string]call{}, events: make(chan harness.Event, 8)}
	s.warm = newWarmer(func([]byte) error { return nil })
	long := time.Now().Add(-time.Hour)
	s.warm.requested(long)
	s.warm.pings = 2

	s.assistant(decode(t, agentCall))
	s.assistant(decode(t, strings.Replace(agentCall, `"parent_tool_use_id":null`, `"parent_tool_use_id":"toolu_main"`, 1)))
	if len(s.warm.calls) != 1 {
		t.Fatalf("%d main-thread calls, want 1", len(s.warm.calls))
	}

	s.toolResults(decode(t, strings.Replace(toolResult, `"parent_tool_use_id":null`, `"parent_tool_use_id":"toolu_main"`, 1)))
	if !s.warm.clock.Equal(long) || s.warm.pings != 2 || len(s.warm.calls) != 1 {
		t.Fatalf("a subagent's result touched the main thread: clock %v pings %d calls %d",
			s.warm.clock, s.warm.pings, len(s.warm.calls))
	}

	s.toolResults(decode(t, toolResult))
	if !s.warm.clock.After(long) || s.warm.pings != 0 || len(s.warm.calls) != 0 {
		t.Fatalf("the main thread's result did not renew: clock %v pings %d calls %d",
			s.warm.clock, s.warm.pings, len(s.warm.calls))
	}
}

func TestADeadlineFollowsWhatTheToolCanTake(t *testing.T) {
	t.Setenv("BASH_DEFAULT_TIMEOUT_MS", "")
	t.Setenv("BASH_MAX_TIMEOUT_MS", "")
	seen := time.Now()
	w := newWarmer(nil)
	for _, c := range []struct {
		name, tool, input string
		want              time.Duration // 0: unknown
	}{
		{"default", "Bash", `{"command":"x"}`, defaultBashTimeout},
		{"asked", "Bash", `{"command":"x","timeout":30000}`, 30 * time.Second},
		{"capped", "Bash", `{"command":"x","timeout":3600000}`, maxBashTimeout},
		{"background", "Bash", `{"command":"x","run_in_background":true}`, 0},
		{"monitor default", "Monitor", `{"command":"x"}`, defaultMonitorTimeout},
		{"monitor capped", "Monitor", `{"command":"x","timeout_ms":99999999}`, maxMonitorTimeout},
		{"other", "Agent", `{"prompt":"x"}`, 0},
	} {
		got := w.deadline(c.tool, json.RawMessage(c.input), seen)
		if (c.want == 0) != got.IsZero() || (c.want != 0 && !got.Equal(seen.Add(c.want))) {
			t.Errorf("%s: deadline %v after seen, want %v", c.name, got.Sub(seen), c.want)
		}
	}

	t.Setenv("BASH_DEFAULT_TIMEOUT_MS", "5000")
	t.Setenv("BASH_MAX_TIMEOUT_MS", "20000")
	w = newWarmer(nil)
	if got := w.deadline("Bash", json.RawMessage(`{"command":"x"}`), seen); !got.Equal(seen.Add(5 * time.Second)) {
		t.Errorf("environment default: %v", got.Sub(seen))
	}
	if got := w.deadline("Bash", json.RawMessage(`{"command":"x","timeout":60000}`), seen); !got.Equal(seen.Add(20 * time.Second)) {
		t.Errorf("environment max: %v", got.Sub(seen))
	}
}
