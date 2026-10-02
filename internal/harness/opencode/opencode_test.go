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
	spec.Timeout = 30 * time.Second
	spec.Dir = t.TempDir()
	session, err := New().Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	for range session.Events() {
	}
	outcome, err := session.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

func TestTheMarkerIsReadFromTheText(t *testing.T) {
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
	for _, want := range []string{"run --auto", "--agent plan-unit", "--model deepseek/deepseek-v4-pro", "--variant high", "do the unit"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the session was started without %q:\n%s", want, joined)
		}
	}
}

func TestAUsageLimitInProseIsRecognised(t *testing.T) {
	fakeOpencode(t, "you have reached the rate limit for this model")

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
