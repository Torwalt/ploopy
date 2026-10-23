package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/stats"
)

const twoStages = `# Pass

## Stage 1 — Kernel

### 1.1 One

**Goal:** g.

**Done when:** d.

### 1.2 Two

**Goal:** g.

**Done when:** d.

## Stage 2 — Callers

### 2.1 Three

**Goal:** g.

**Done when:** d.
`

func TestTheSummaryTotalsUnitsStagesAndThePlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "PASS.md")
	if err := os.WriteFile(path, []byte(twoStages), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := plan.Load(path, "docs/plans/PASS.md")
	if err != nil {
		t.Fatal(err)
	}
	s := &state.State{Units: map[string]*state.Entry{
		"1.1": {Status: state.Landed, Outcome: "done", Attempts: 1, ElapsedS: 243, VerifyS: 7, TestS: 40, CostUSD: 0.25,
			Harness: "opencode", Tokens: &harness.Tokens{Input: 70_000, CacheRead: 1_700_000, Output: 26_000}},
		"1.2": {Status: state.Blocked, Outcome: "blocked", Attempts: 2, ElapsedS: 1294, CostUSD: 0.5,
			Harness: "claude", Reason: "the premise is wrong"},
	}}
	stats := &stats.Run{Started: time.Unix(0, 0), Ended: time.Unix(3600+600, 0), Sessions: 3, CostUSD: 0.75}

	var out bytes.Buffer
	NewReporter(&out, &out).Summary(p, s, stats)
	text := out.String()
	t.Log("\n" + text)

	for _, want := range []string{
		"PASS 1/3 units", "Stage 1 — Kernel", "4m03s", "47s", "1.8M / 26k", "$0.25",
		"≈$0.50", "blocked: the premise is wrong", "stage", "25m37s", "pending",
		"plan", "≈$0.75", "this run: 1h10m · 3 session(s) in 0s", "API-price equivalent",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the summary lacks %q:\n%s", want, text)
		}
	}
}

func TestTheSummaryClosesWithEveryRescue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "PASS.md")
	if err := os.WriteFile(path, []byte(twoStages), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := plan.Load(path, "docs/plans/PASS.md")
	if err != nil {
		t.Fatal(err)
	}
	s := &state.State{Units: map[string]*state.Entry{
		"1.1": {Status: state.Landed, Outcome: "done", Rescues: []state.Rescue{{
			Trigger: "failed", Verdict: "retry", Reason: "the provider stalled",
		}}},
		"1.2": {Status: state.Landed, Outcome: "done", Rescues: []state.Rescue{{
			Trigger: "blocked", Verdict: "decided", Reason: "use the existing clock",
			Report: "## Decisions\n\nUse the repository's existing clock.\n" +
				"Record it in the handover.\n",
		}}},
	}}

	var out bytes.Buffer
	NewReporter(&out, &out).Summary(p, s, nil)
	text := out.String()
	t.Log("\n" + text)
	for _, want := range []string{
		"Rescued",
		"1.1  failed → retry: the provider stalled",
		"1.2  blocked → decided: use the existing clock",
		"Use the repository's existing clock.",
		"Record it in the handover.",
		"review these: ploopy report PASS",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the summary lacks %q:\n%s", want, text)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(text), "review these: ploopy report PASS") {
		t.Fatalf("the summary does not end with the report pointer:\n%s", text)
	}
}

func TestTheSummaryShowsStoredDecisionsPastTheCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "PASS.md")
	if err := os.WriteFile(path, []byte(twoStages), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := plan.Load(path, "docs/plans/PASS.md")
	if err != nil {
		t.Fatal(err)
	}
	decisions := "Use the repository's existing clock.\nRecord it in the handover."
	s := &state.State{Units: map[string]*state.Entry{
		"1.2": {Status: state.Landed, Outcome: "done", Rescues: []state.Rescue{{
			Trigger: "blocked", Verdict: "decided", Reason: "use the existing clock",
			Report:    ("## Diagnosis\n\n" + strings.Repeat("diagnosis line\n", 400))[:4000],
			Decisions: decisions,
		}}},
	}}

	var out bytes.Buffer
	NewReporter(&out, &out).Summary(p, s, nil)
	text := out.String()
	t.Log("\n" + text)
	for _, want := range []string{
		"1.2  blocked → decided: use the existing clock",
		"Use the repository's existing clock.",
		"Record it in the handover.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the summary lacks %q:\n%s", want, text)
		}
	}
}
