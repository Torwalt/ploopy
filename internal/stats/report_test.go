package stats

import (
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/state"
)

const twoStages = `# Pass

## Stage 1 — Kernel

### 1.1 The refill kernel

**Goal:** g.

**Done when:** d.

### 1.2 Circles refill in fifths

**Goal:** g.

**Done when:** d.

## Stage 2 — Callers

### 2.1 The Director buys Drafts

**Goal:** g.

**Done when:** d.

### 2.2 Not yet

**Goal:** g.

**Done when:** d.
`

func fixture() (*plan.Plan, *state.State) {
	p := plan.Parse(twoStages, "docs/plans/PASS.md")
	s := &state.State{Units: map[string]*state.Entry{
		"1.1": {Status: state.Landed, Outcome: "done", Attempts: 1, ElapsedS: 300, SessionS: 240, VerifyS: 7, TestS: 60,
			CostUSD: 0.03, Harness: "opencode", Agent: "opencode deepseek/deepseek-flash high",
			Tokens: &harness.Tokens{Input: 70_000, CacheRead: 1_700_000, Output: 6_000, Reasoning: 19_000}},
		"1.2": {Status: state.Landed, Outcome: "done", Attempts: 2, ElapsedS: 1500, SessionS: 1300, VerifyS: 14, TestS: 150,
			CostUSD: 0.17, Harness: "opencode", Agent: "opencode deepseek/deepseek-flash high",
			Notes: []string{"`just verify` failed once and passed on a rerun"}},
		"2.1": {Status: state.Blocked, Outcome: "blocked", Attempts: 1, ElapsedS: 600, SessionS: 580, CostUSD: 0.4,
			Harness: "claude", Agent: "claude sonnet high", Reason: "the Director has no budget to buy with"},
	}}
	return p, s
}

func TestTheReportSaysWhatThePlanTook(t *testing.T) {
	p, s := fixture()
	at := time.Date(2026, 10, 4, 22, 0, 0, 0, time.UTC)
	records := []Record{
		{Kind: "wait", Reason: "peak", Seconds: 5400},
		{Kind: "session", Unit: "1.1", Agent: "opencode deepseek/deepseek-flash high", Verdict: "landed", CostUSD: 0.03},
		{Kind: "check", Unit: "1.1", Reason: "verify", Seconds: 7},
		{Kind: "check", Unit: "1.1", Reason: "test", Seconds: 60},
		{Kind: "tool", Unit: "1.1", Tool: "bash", Command: "just test", Seconds: 61},
		{Kind: "tool", Unit: "1.1", Tool: "read", Command: "src/a.odin", Seconds: 0.01},
		{Kind: "session", Unit: "1.2", Agent: "opencode deepseek/deepseek-flash high", Verdict: "failed",
			Reason: "the working tree is dirty after the session", CostUSD: 0.07},
		{Kind: "check", Unit: "1.2", Reason: "verify", Seconds: 7},
		{Kind: "session", Unit: "1.2", Agent: "opencode deepseek/deepseek-flash high", Verdict: "landed", CostUSD: 0.10},
		{Kind: "check", Unit: "1.2", Reason: "verify", Seconds: 7},
		{Kind: "check", Unit: "1.2", Reason: "test", Seconds: 70},
		{Kind: "check", Unit: "1.2", Reason: "test", Seconds: 80},
		{Kind: "tool", Unit: "1.2", Tool: "bash", Command: "just test", Seconds: 95},
		{Kind: "session", Unit: "2.1", Agent: "claude sonnet high", Verdict: "blocked", CostUSD: 0.4},
		{Kind: "check", Unit: "2.1", Reason: "test", Seconds: 90},
		{Kind: "run", Started: at, Seconds: 9000, Verdict: "failed"},
	}

	text := Report(p, s, records, nil)
	t.Log("\n" + text)
	for _, want := range []string{
		"ploopy report: PASS",
		"2 of 4 landed · 1 blocked · 1 pending · 1 run(s) · 2h30m wall clock · ≈$0.60",
		"Waits     peak 1h30m",
		"Sessions  4 · 2 landed · 1 failed · 1 blocked",
		"opencode deepseek/deepseek-flash high  3 session(s)  $0.20",
		"claude sonnet high  1 session(s)  ≈$0.40",
		"1.8M read, 96% from cache · 25k written",
		"1 Kernel", "2 Callers",
		"1.2 Circles refill in fifths",
		"failed once and passed on a rerun in 1.2",
		"slowed down 50%",
		"just test", "1m35s",
		"1.2 failed: the working tree is dirty after the session",
		"2.1 blocked: the Director has no budget to buy with",
		"API-price equivalent",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the report lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "src/a.odin") {
		t.Fatal("a call that took no time is listed among the slowest")
	}
	if strings.Contains(text, "No stats log") {
		t.Fatal("the report says the log is gone while it has one")
	}
}

// Once the checkout's .ploopy/ is gone, the state file still tells most of it.
func TestWithoutTheLogTheStateFileTellsIt(t *testing.T) {
	p, s := fixture()

	text := Report(p, s, nil, nil)
	t.Log("\n" + text)
	for _, want := range []string{
		"2 of 4 landed", "≈$0.60", "Time      35m20s in sessions · 3m51s in checks",
		"opencode deepseek/deepseek-flash high  2 unit(s)  $0.20", "Slowest units", "No stats log",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the report lacks %q:\n%s", want, text)
		}
	}
}

func TestATrendNeedsEnoughRuns(t *testing.T) {
	for values, want := range map[*[]float64]string{
		{7, 7, 7, 7, 7, 7}:       "steady",
		{60, 60, 80, 90, 95, 99}: "slowed down 62%",
		{90, 90, 60, 50, 45, 45}: "sped up 50%",
		{7, 9}:                   "",
	} {
		if got := trend(*values); got != want {
			t.Fatalf("trend(%v) = %q, want %q", *values, got, want)
		}
	}
	if line := sparkline([]float64{1, 2, 3, 4, 5, 6, 7, 8}); line != "▁▂▃▄▅▆▇█" {
		t.Fatalf("sparkline %q", line)
	}
}
