package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/Torwalt/ploopy/internal/harness"
)

// limits is what the fake app server answers for the account's usage windows.
const limits = `{"method":"account/updated","params":{"authMode":"chatgpt","planType":"plus"}}
{"id":2,"result":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":60,"windowDurationMins":300,"resetsAt":1792087921},"secondary":{"usedPercent":9,"windowDurationMins":10080,"resetsAt":1792674721}}}}`

// fakeCodex puts a script named `codex` on PATH. As `codex exec` it records
// its arguments and prompt and prints output; as `codex app-server` it
// answers the initialize handshake and then whatever CODEX_TEST_LIMITS holds.
func fakeCodex(t *testing.T, output string, exit int) (string, string) {
	t.Helper()
	dir := t.TempDir()
	args, prompt := filepath.Join(dir, "args"), filepath.Join(dir, "prompt")
	t.Setenv("CODEX_TEST_ARGS", args)
	t.Setenv("CODEX_TEST_PROMPT", prompt)
	t.Setenv("CODEX_TEST_LIMITS", limits)
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = app-server ]; then\n" +
		"  [ -n \"$CODEX_TEST_STALL\" ] && sleep \"$CODEX_TEST_STALL\"\n" +
		"  read -r _; echo '{\"id\":1,\"result\":{}}'\n" +
		"  read -r _; read -r _; printf '%s\\n' \"$CODEX_TEST_LIMITS\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"printf '%s\\n' \"$@\" > \"$CODEX_TEST_ARGS\"\ncat > \"$CODEX_TEST_PROMPT\"\ncat <<'OUT'\n" + output + "\nOUT\nexit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return args, prompt
}

func start(t *testing.T, spec harness.Spec) (harness.Outcome, []harness.Event) {
	t.Helper()
	if spec.Dir == "" {
		spec.Dir = t.TempDir()
	}
	if spec.Timeout == 0 {
		spec.Timeout = 30 * time.Second
	}
	s, err := New().Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	var events []harness.Event
	for e := range s.Events() {
		events = append(events, e)
	}
	outcome, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return outcome, events
}

const stream = `{"type":"thread.started","thread_id":"actual-thread"}
{"type":"turn.started"}
{"type":"item.started","item":{"id":"command","type":"command_execution","command":"go test ./...","status":"in_progress"}}
{"type":"item.completed","item":{"id":"command","type":"command_execution","command":"go test ./...","aggregated_output":"DONE","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"message","type":"agent_message","text":"Finished.\nDONE"}}
{"type":"turn.completed","usage":{"input_tokens":1000,"cached_input_tokens":800,"output_tokens":100,"reasoning_output_tokens":30}}
{"type":"turn.completed","usage":{"input_tokens":500,"cached_input_tokens":300,"output_tokens":40}}`

func TestStructuredOutcomeAndToolEvents(t *testing.T) {
	fakeCodex(t, stream, 0)
	log := filepath.Join(t.TempDir(), "session.jsonl")
	out, events := start(t, harness.Spec{Prompt: "work", SessionID: "requested-id", LogPath: log})
	want := harness.Tokens{Input: 400, CacheRead: 1100, Output: 110, Reasoning: 30}
	if out.Marker != harness.Done || out.SessionID != "actual-thread" || out.Tokens != want || out.Turns != 2 || out.Exit != 0 || out.CostUSD != 0 {
		t.Fatalf("outcome %+v", out)
	}
	var tools, done, usage int
	for _, e := range events {
		switch e.Kind {
		case harness.EventToolUse:
			tools++
			if e.Tool != "Bash" || e.Text != "go test ./..." {
				t.Fatalf("tool %+v", e)
			}
		case harness.EventToolDone:
			done++
			if e.Took < 0 {
				t.Fatalf("duration %v", e.Took)
			}
		case harness.EventUsage:
			usage = e.Tokens
		}
	}
	if tools != 1 || done != 1 || usage != want.Total() {
		t.Fatalf("tools %d done %d tokens %d", tools, done, usage)
	}
	raw, err := os.ReadFile(log)
	if err != nil || string(raw) != stream+"\n" {
		t.Fatalf("transcript %q, error %v", raw, err)
	}
}

func TestArgumentsAndStdin(t *testing.T) {
	args, prompt := fakeCodex(t, stream, 0)
	long := strings.Repeat("a large plan\n", 20000)
	start(t, harness.Spec{Prompt: long, Model: "custom-model", Effort: "xhigh", Extra: []string{"--profile", "review"}})
	raw, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Split(strings.TrimSpace(string(raw)), "\n")
	joined := strings.Join(argv, " ")
	for _, want := range []string{"exec --json --color never", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust", "--config features.hooks=true", "--config web_search=\"disabled\"", "--model custom-model", "model_reasoning_effort=\"xhigh\"", "--profile review -"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
	var settings struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string
				Hooks   []struct {
					Type, Command string
					Timeout       int
				}
			}
		}
	}
	for _, arg := range argv {
		if strings.HasPrefix(arg, "hooks.PreToolUse=") {
			if _, err := toml.Decode(arg, &settings); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(settings.Hooks.PreToolUse) != 1 {
		t.Fatalf("guard config %+v", settings)
	}
	group := settings.Hooks.PreToolUse[0]
	if group.Matcher != "^Bash$" || len(group.Hooks) != 1 || !strings.HasSuffix(group.Hooks[0].Command, "' guard") || group.Hooks[0].Type != "command" {
		t.Fatalf("guard config %+v", group)
	}
	raw, err = os.ReadFile(prompt)
	if err != nil || string(raw) != long {
		t.Fatalf("prompt length %d, error %v", len(raw), err)
	}
}

func TestDiagnosticsAndToolOutputCannotCompleteAUnit(t *testing.T) {
	fakeCodex(t, `DONE
{"type":"item.completed","item":{"id":"cmd","type":"command_execution","command":"echo DONE","aggregated_output":"DONE"}}
{"type":"item.completed","item":{"id":"thought","type":"reasoning","text":"DONE"}}
{"type":"new.event","text":"DONE"}`, 0)
	out, _ := start(t, harness.Spec{Prompt: "x"})
	if out.Marker != harness.None {
		t.Fatalf("marker %q", out.Marker)
	}
}

func TestUsageLimitsAndFailures(t *testing.T) {
	for _, output := range []string{
		`{"type":"error","message":"You've hit your usage limit. Try again at 10pm (UTC)."}`,
		`{"type":"turn.failed","error":{"message":"rate_limit_exceeded"}}`,
		"Error: usage limit exceeded",
	} {
		t.Run(output, func(t *testing.T) {
			fakeCodex(t, output, 1)
			out, _ := start(t, harness.Spec{Prompt: "x"})
			if out.RateLimit == nil || out.Exit != 1 || out.Marker != harness.None {
				t.Fatalf("outcome %+v", out)
			}
		})
	}
	t.Run("ordinary failure", func(t *testing.T) {
		fakeCodex(t, `{"type":"turn.failed","error":{"message":"authentication failed"}}`, 1)
		out, events := start(t, harness.Spec{Prompt: "x"})
		if out.RateLimit != nil || out.Exit != 1 || len(events) != 1 || events[0].Kind != harness.EventNotice {
			t.Fatalf("outcome %+v, events %+v", out, events)
		}
	})
}

func TestCompletionOnlyToolsAndMarkers(t *testing.T) {
	fakeCodex(t, `{"type":"item.completed","item":{"id":"edit","type":"file_change","changes":[{"path":"main.go","kind":"update"}]}}
{"type":"item.started","item":{"id":"mcp","type":"mcp_tool_call","server":"docs","tool":"read","arguments":{"path":"README.md"}}}
{"type":"item.completed","item":{"id":"mcp","type":"mcp_tool_call","server":"docs","tool":"read","arguments":{"path":"README.md"}}}
{"type":"item.completed","item":{"id":"answer","type":"agent_message","text":"BLOCKED missing fixture"}}`, 0)
	out, events := start(t, harness.Spec{Prompt: "x"})
	if out.Marker != harness.Blocked || out.Reason != "missing fixture" {
		t.Fatalf("outcome %+v", out)
	}
	var tools []string
	for _, e := range events {
		if e.Kind == harness.EventToolUse {
			tools = append(tools, e.Tool)
		}
	}
	if !reflect.DeepEqual(tools, []string{"apply_patch", "mcp__docs__read"}) {
		t.Fatalf("tools %v", tools)
	}
}

func TestRecoveredLimitDoesNotTriggerFallback(t *testing.T) {
	fakeCodex(t, "{\"type\":\"error\",\"message\":\"rate limit exceeded; retrying\"}\n"+stream, 0)
	out, _ := start(t, harness.Spec{Prompt: "x"})
	if out.Marker != harness.Done || out.RateLimit != nil {
		t.Fatalf("outcome %+v", out)
	}
}

func TestLimitReportedByTheAgentStillTriggersFallback(t *testing.T) {
	fakeCodex(t, `{"type":"item.completed","item":{"id":"answer","type":"agent_message","text":"BLOCKED usage limit exceeded"}}`, 0)
	out, _ := start(t, harness.Spec{Prompt: "x"})
	if out.RateLimit == nil {
		t.Fatalf("outcome %+v", out)
	}
}

func TestBudgetIsRefusedBeforeStarting(t *testing.T) {
	args, _ := fakeCodex(t, "", 0)
	_, err := New().Start(context.Background(), harness.Spec{MaxBudgetUSD: 1})
	if err == nil || !strings.Contains(err.Error(), "spending cap") {
		t.Fatalf("error %v", err)
	}
	if _, err := os.Stat(args); !os.IsNotExist(err) {
		t.Fatalf("process started: %v", err)
	}
}

func TestTimeoutAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "cancel"}[cancel], func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\n[ \"$1\" = app-server ] && exit 1\ncat >/dev/null\nexec sleep 30\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			spec := harness.Spec{Prompt: "x", Timeout: 100 * time.Millisecond}
			if cancel {
				spec.Timeout = 30 * time.Second
			}
			s, err := New().Start(ctx, spec)
			if err != nil {
				t.Fatal(err)
			}
			if cancel {
				stop()
			}
			for range s.Events() {
			}
			out, err := s.Wait()
			if err != nil || out.Exit == 0 || out.TimedOut == cancel {
				t.Fatalf("outcome %+v, error %v", out, err)
			}
		})
	}
}

func TestTheUsageWindowsAreReadAroundTheSession(t *testing.T) {
	fakeCodex(t, stream, 0)
	out, _ := start(t, harness.Spec{Prompt: "x"})
	for _, budget := range []*harness.Budget{out.BudgetBefore, out.Budget} {
		if budget == nil || budget.Session.Used != 0.6 || budget.Weekly.Used != 0.09 ||
			budget.Session.Length != 5*time.Hour || !budget.Session.ResetAt.Equal(time.Unix(1792087921, 0)) {
			t.Fatalf("budget %+v", budget)
		}
	}
	if len(out.Degraded) != 0 {
		t.Fatalf("degraded %v", out.Degraded)
	}
}

func TestUnreadableWindowsAreDegraded(t *testing.T) {
	fakeCodex(t, stream, 0)
	t.Setenv("CODEX_TEST_LIMITS", `{"id":2,"error":{"message":"not logged in"}}`)
	out, _ := start(t, harness.Spec{Prompt: "x"})
	if out.Budget != nil || len(out.Degraded) != 2 || !strings.Contains(out.Degraded[0], "usage windows unreadable before") {
		t.Fatalf("budget %+v, degraded %v", out.Budget, out.Degraded)
	}
}

// A read the run's stop cuts off is the stop, not lost output, and it ends at
// once although the app server's own child still holds the pipe.
func TestAStoppedWindowReadIsNotDegraded(t *testing.T) {
	fakeCodex(t, stream, 0)
	t.Setenv("CODEX_TEST_STALL", "10")
	ctx, stop := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer stop()
	began := time.Now()
	s, err := New().Start(ctx, harness.Spec{Prompt: "x", Dir: t.TempDir(), Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Events() {
	}
	out, _ := s.Wait()
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("the stopped read took %v", took)
	}
	if out.BudgetBefore != nil || len(out.Degraded) != 0 {
		t.Fatalf("budget %+v, degraded %v", out.BudgetBefore, out.Degraded)
	}
}

// An account on an API key has no usage windows, and nothing is missing.
func TestAnAPIKeyAccountHasNoWindows(t *testing.T) {
	fakeCodex(t, stream, 0)
	t.Setenv("CODEX_TEST_LIMITS", `{"method":"account/updated","params":{"authMode":"apikey"}}
{"id":2,"error":{"message":"rate limits need a ChatGPT login"}}`)
	out, _ := start(t, harness.Spec{Prompt: "x"})
	if out.Budget != nil || len(out.Degraded) != 0 {
		t.Fatalf("budget %+v, degraded %v", out.Budget, out.Degraded)
	}
}

func TestAnEventThatNoLongerDecodesIsDegraded(t *testing.T) {
	fakeCodex(t, `{"type":"thread.started","thread_id":"t"}
{"type":"turn.completed","usage":{"input_tokens":"many"}}
{"type":"item.completed","item":{"id":"message","type":"agent_message","text":"DONE"}}`, 0)
	out, _ := start(t, harness.Spec{Prompt: "x"})
	joined := strings.Join(out.Degraded, "\n")
	if out.Marker != harness.Done || !strings.Contains(joined, "did not decode: turn.completed") ||
		!strings.Contains(joined, "no turn.completed") {
		t.Fatalf("outcome %+v", out)
	}
}

// A limit reported without a time resets when the spent window does.
func TestALimitResetsWithItsWindow(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	fakeCodex(t, `{"type":"turn.failed","error":{"message":"rate_limit_exceeded"}}`, 1)
	t.Setenv("CODEX_TEST_LIMITS", fmt.Sprintf(`{"id":2,"result":{"rateLimits":{"primary":`+
		`{"usedPercent":100,"windowDurationMins":300,"resetsAt":%d},"secondary":`+
		`{"usedPercent":40,"windowDurationMins":10080,"resetsAt":%d}}}}`, reset.Unix(), reset.Add(72*time.Hour).Unix()))
	out, _ := start(t, harness.Spec{Prompt: "x"})
	if out.RateLimit == nil || !out.RateLimit.ResetAt.Equal(reset.Add(2*time.Minute)) {
		t.Fatalf("limit %+v", out.RateLimit)
	}
}
