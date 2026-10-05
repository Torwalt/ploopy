package opencode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
)

func fakeOpencode(t *testing.T, output string) string {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + args + "\ncat <<'OUT'\n" + output + "\nOUT\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return args
}

func start(t *testing.T, spec harness.Spec) harness.Outcome {
	t.Helper()
	outcome, _ := startWithEvents(t, spec)
	return outcome
}

func startWithEvents(t *testing.T, spec harness.Spec) (harness.Outcome, []harness.Event) {
	t.Helper()
	spec.Timeout = 30 * time.Second
	spec.Dir = t.TempDir()
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

// stream is what `opencode run --format json` printed for a session that ran
// one command and finished, trimmed to the fields that matter.
const stream = `{"type":"step_start","timestamp":1,"sessionID":"ses_1","part":{"type":"step-start"}}
{"type":"tool_use","timestamp":2,"sessionID":"ses_1","part":{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"go test ./..."},"output":"ok\n","time":{"start":1000,"end":3500}}}}
{"type":"step_finish","timestamp":3,"sessionID":"ses_1","part":{"type":"step-finish","reason":"tool-calls","tokens":{"total":9702,"input":8000,"output":38,"reasoning":10,"cache":{"write":0,"read":1664}},"cost":0.001}}
{"type":"step_start","timestamp":4,"sessionID":"ses_1","part":{"type":"step-start"}}
{"type":"text","timestamp":5,"sessionID":"ses_1","part":{"type":"text","text":"Implemented the unit.\n\nDONE"}}
{"type":"step_finish","timestamp":6,"sessionID":"ses_1","part":{"type":"step-finish","reason":"stop","tokens":{"total":9720,"input":245,"output":3,"reasoning":0,"cache":{"write":5,"read":9472}},"cost":0.0005}}`

func TestTheMarkerIsReadFromTheText(t *testing.T) {
	fakeOpencode(t, stream)

	if outcome := start(t, harness.Spec{Prompt: "x"}); outcome.Marker != harness.Done {
		t.Fatalf("marker %q", outcome.Marker)
	}
}

// Every step reports its own use, so the session's is their sum.
func TestTokensAndCostAreSummedOverSteps(t *testing.T) {
	fakeOpencode(t, stream)

	outcome, events := startWithEvents(t, harness.Spec{Prompt: "x"})
	want := harness.Tokens{Input: 8245, Output: 41, Reasoning: 10, CacheRead: 11136, CacheWrite: 5}
	if outcome.Tokens != want {
		t.Fatalf("tokens %+v, want %+v", outcome.Tokens, want)
	}
	if outcome.CostUSD < 0.00149 || outcome.CostUSD > 0.00151 || outcome.Turns != 2 {
		t.Fatalf("cost %v turns %d", outcome.CostUSD, outcome.Turns)
	}
	if outcome.SessionID != "ses_1" {
		t.Fatalf("session id %q", outcome.SessionID)
	}

	var tools int
	var took time.Duration
	for _, event := range events {
		switch event.Kind {
		case harness.EventToolUse:
			tools++
			if event.Tool != "bash" || event.Text != "go test ./..." {
				t.Fatalf("tool event %+v", event)
			}
		case harness.EventToolDone:
			took = event.Took
		}
	}
	if tools != 1 || took != 2500*time.Millisecond {
		t.Fatalf("%d tool events, the call took %v", tools, took)
	}
}

// Whatever opencode prints outside its event stream is still read.
func TestOutputOutsideTheEventStreamIsStillRead(t *testing.T) {
	fakeOpencode(t, "working on it\nall tests pass\nDONE")

	if outcome := start(t, harness.Spec{Prompt: "x"}); outcome.Marker != harness.Done {
		t.Fatalf("marker %q", outcome.Marker)
	}
}

func TestAPromptTooLargeForOneArgumentIsRefusedBeforeExec(t *testing.T) {
	fakeOpencode(t, "DONE")

	_, err := New().Start(context.Background(), harness.Spec{
		Prompt: strings.Repeat("x", MaxArgumentBytes+1),
		Dir:    t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "one argument") {
		t.Fatalf("error %v", err)
	}
}

func TestTheSessionIsStartedWithThePlanUnitAgent(t *testing.T) {
	argsFile := fakeOpencode(t, "DONE")
	start(t, harness.Spec{Prompt: "do the unit", Model: "deepseek/deepseek-v4-pro", Effort: "high"})

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(strings.Split(strings.TrimSpace(string(raw)), "\n"), " ")
	for _, want := range []string{"run --format json --auto", "--agent plan-unit", "--model deepseek/deepseek-v4-pro", "--variant high", "do the unit"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the session was started without %q:\n%s", want, joined)
		}
	}
}

func TestAUsageLimitInProseIsRecognised(t *testing.T) {
	fakeOpencode(t, `{"type":"error","sessionID":"ses_1","error":{"name":"APIError","data":{"message":"you have reached the rate limit for this model"}}}`)

	outcome := start(t, harness.Spec{Prompt: "x"})
	if outcome.Marker != harness.None {
		t.Fatalf("marker %q", outcome.Marker)
	}
	if outcome.RateLimit == nil {
		t.Fatal("the usage limit was not recognised")
	}
}

// DeepSeek bills double on weekday mornings, UTC.
func TestThePeakWindowsAreDeepseeksOwn(t *testing.T) {
	windows := New().PeakWindows()
	if len(windows) != 2 {
		t.Fatalf("%d windows", len(windows))
	}
	for _, at := range []time.Time{
		time.Date(2026, 10, 2, 2, 30, 0, 0, time.UTC), // Friday, first window
		time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC),  // Friday, second window
	} {
		if inPeak, _ := harness.Peak(windows, at); !inPeak {
			t.Fatalf("%s should be peak", at)
		}
	}
	for _, at := range []time.Time{
		time.Date(2026, 10, 2, 0, 30, 0, 0, time.UTC), // Friday, before the first
		time.Date(2026, 10, 2, 5, 0, 0, 0, time.UTC),  // Friday, between them
		time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC), // Friday evening
		time.Date(2026, 10, 3, 2, 30, 0, 0, time.UTC), // Saturday
		time.Date(2026, 10, 4, 7, 0, 0, 0, time.UTC),  // Sunday
	} {
		if inPeak, _ := harness.Peak(windows, at); inPeak {
			t.Fatalf("%s should be off-peak", at)
		}
	}
}
