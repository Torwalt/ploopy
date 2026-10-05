package loop

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/fake"
)

func (f *fixture) records() []record {
	f.t.Helper()
	file, err := os.Open(filepath.Join(f.root, ".ploopy", "pass", "stats.jsonl"))
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	var out []record
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var r record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func spending(cost float64, input, output int) harness.Outcome {
	return harness.Outcome{Marker: harness.Done, CostUSD: cost, Tokens: harness.Tokens{Input: input, Output: output}}
}

// A unit's cost is every session it took, the failed ones too.
func TestAUnitRecordsWhatEveryAttemptCost(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: spending(0.5, 100, 10)}, // DONE without a commit fails
		fake.Step{Do: commits("one.go", "first"), Outcome: spending(0.25, 50, 5)},
		fake.Step{Do: commits("two.go", "second"), Outcome: spending(0.125, 20, 2)},
	)

	result := f.run()
	if result.Status != "done" {
		t.Fatalf("run %+v", result)
	}

	entry := f.state().Entry("1.1")
	if entry.CostUSD != 0.75 || entry.Attempts != 2 {
		t.Fatalf("unit 1.1 cost %v over %d attempts", entry.CostUSD, entry.Attempts)
	}
	if entry.Tokens == nil || *entry.Tokens != (harness.Tokens{Input: 150, Output: 15}) {
		t.Fatalf("unit 1.1 tokens %+v", entry.Tokens)
	}

	stats := result.Stats
	if stats.Sessions != 3 || stats.CostUSD != 0.875 || stats.Tokens.Total() != 187 {
		t.Fatalf("run stats %+v", stats)
	}
	if stats.Ended.Before(stats.Started) {
		t.Fatalf("the run ended before it started: %+v", stats)
	}

	var verdicts []string
	runs := 0
	for _, r := range f.records() {
		switch r.Kind {
		case "session":
			verdicts = append(verdicts, r.Unit+" "+r.Verdict)
			if r.Agent != "fake fake-model high" {
				t.Fatalf("session record %+v", r)
			}
		case "run":
			runs++
			if r.Verdict != "done" || r.CostUSD != 0.875 {
				t.Fatalf("run record %+v", r)
			}
		}
	}
	want := []string{"1.1 failed", "1.1 landed", "1.2 landed"}
	if len(verdicts) != len(want) {
		t.Fatalf("session records %v, want %v", verdicts, want)
	}
	for i := range want {
		if verdicts[i] != want[i] {
			t.Fatalf("session records %v, want %v", verdicts, want)
		}
	}
	if runs != 1 {
		t.Fatalf("%d run records", runs)
	}
}

func TestChecksAndWaitsAreRecorded(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Outcome: harness.Outcome{RateLimit: &harness.RateLimit{Reason: "limit"}}},
		fake.Step{Do: commits("one.go", "first"), Outcome: done()},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	f.opts.VerifyCmd = "true"
	f.opts.LimitWait = time.Minute

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}

	checks := map[string]int{}
	waits := map[string]int{}
	sessions := map[string]int{}
	for _, r := range f.records() {
		switch r.Kind {
		case "check":
			checks[r.Reason]++
		case "wait":
			waits[r.Reason]++
		case "session":
			sessions[r.Verdict]++
		}
	}
	if checks["preflight"] != 1 || checks["verify"] != 2 {
		t.Fatalf("checks %v", checks)
	}
	if waits["limit"] != 1 || sessions["limit"] != 1 || sessions["landed"] != 2 {
		t.Fatalf("waits %v sessions %v", waits, sessions)
	}
	if entry := f.state().Entry("1.1"); entry.ElapsedS < 0 || entry.SessionS < 0 {
		t.Fatalf("unit 1.1 %+v", entry)
	}
}

func TestEveryToolCallIsTimed(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "first"), Outcome: done(), Events: []harness.Event{
			{Kind: harness.EventToolUse, Tool: "bash", Text: "just test"},
			{Kind: harness.EventToolDone, Tool: "bash", Text: "just test", Took: 90 * time.Second},
		}},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	var tools []record
	for _, r := range f.records() {
		if r.Kind == "tool" {
			tools = append(tools, r)
		}
	}
	if len(tools) != 1 || tools[0].Command != "just test" || tools[0].Seconds != 90 || tools[0].Unit != "1.1" {
		t.Fatalf("tool records %+v", tools)
	}
}
