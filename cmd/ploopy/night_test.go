package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/history"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/stats"
)

func TestTheNightFlagNamesSubscriptionsAndTheirWindows(t *testing.T) {
	harnesses := allHarnesses()
	agents, err := parseNight(harnesses, "--night", "claude=2, codex/gpt-6-luna/xhigh=0")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 || agents[0].Label() != "claude sonnet high" || agents[0].Windows != 2 ||
		agents[1].Label() != "codex gpt-6-luna xhigh" || agents[1].Windows != 0 {
		t.Fatalf("agents %+v", agents)
	}
	for _, bad := range []string{"claude", "claude=-1", "opencode=1", "nobody=1"} {
		if _, err := parseNight(harnesses, "--night", bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestAnUnattendedAwayRunNeedsItsSubscriptionsNamed(t *testing.T) {
	e := &env{harnesses: allHarnesses()}
	settings := runSettings{Harness: "opencode", Effort: "high", Away: "21:00-09:00"}
	if _, err := unattended(e, settings); err == nil {
		t.Fatal("an away run without --night started")
	}
	settings.Night, _ = parseNight(e.harnesses, "--night", "claude=2")
	chosen, err := unattended(e, settings)
	if err != nil {
		t.Fatal(err)
	}
	n := chosen.lending
	if n == nil || len(n.Leases) != 1 || n.Leases[0].Kind != loop.LeaseNightly ||
		n.Leases[0].Span.String() != "21:00-09:00" || n.Leases[0].Keep != 0.2 {
		t.Fatalf("lending %+v", n)
	}
	settings.Harness = "claude"
	if _, err := unattended(e, settings); err == nil {
		t.Fatal("the run's own agent was also a night agent")
	}
}

// claudeUnit is a landed unit that took a tenth of a five-hour window and a
// hundredth of the week.
func claudeUnit(unit string, reset time.Time) history.Record {
	window := func(session, weekly float64) *harness.Budget {
		return &harness.Budget{
			Session: harness.Allowance{Used: session, ResetAt: reset, Length: harness.SessionWindow},
			Weekly:  harness.Allowance{Used: weekly, ResetAt: reset.Add(96 * time.Hour), Length: harness.WeeklyWindow},
		}
	}
	return history.Record{Plan: "P", Unit: unit, Agent: "claude sonnet high", Harness: "claude", Verdict: "landed",
		Seconds: 900, CostUSD: 2, Before: window(0.2, 0.4), After: window(0.3, 0.41), Away: true}
}

func TestTheProposalFitsFreshWindowsAndTheWeeklyReserve(t *testing.T) {
	zone := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, 10, 15, 20, 30, 0, 0, zone)
	away, _ := loop.ParseAway("21:00-09:00")
	reset := time.Date(2026, 10, 15, 22, 10, 0, 0, zone)
	records := []history.Record{claudeUnit("1", reset), claudeUnit("2", reset), claudeUnit("3", reset)}
	budget := func(session, weekly float64) *harness.Budget {
		return &harness.Budget{
			At:      now,
			Session: harness.Allowance{Used: session, ResetAt: reset, Length: harness.SessionWindow},
			Weekly:  harness.Allowance{Used: weekly, ResetAt: now.Add(96 * time.Hour), Length: harness.WeeklyWindow},
		}
	}

	// The window running out at 22:10 is free; 22:10 to 09:00 fits two more.
	p := propose(budget(0.5, 0.51), "", "claude sonnet high", records, away, 0.2, 12, now)
	if !p.rest || p.fits != 2 || p.suggest != 2 {
		t.Fatalf("proposal %+v", p)
	}
	if text := strings.Join(p.lines, "\n"); !strings.Contains(text, "rest of its window is free until 22:10") ||
		!strings.Contains(text, "the week ≈ 76% spent") {
		t.Fatalf("lines:\n%s", text)
	}

	// Each window takes a tenth of the week: at 65% only one fits under 80%.
	if p := propose(budget(0.5, 0.65), "", "claude sonnet high", records, away, 0.2, 12, now); p.suggest != 1 {
		t.Fatalf("proposal %+v", p)
	}
	if p := propose(budget(0.5, 0.85), "", "claude sonnet high", records, away, 0.2, 12, now); p.suggest != 0 {
		t.Fatalf("proposal %+v", p)
	}
	// Unknown windows and no history: every window that fits from 21:00.
	if p := propose(nil, "", "codex gpt-6.1-sol high", nil, away, 0.2, 12, now); p.rest || p.fits != 2 || p.suggest != 2 {
		t.Fatalf("proposal %+v", p)
	}
}

func TestTheHistoryImportsWhatTheCheckoutsHold(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	dir := stats.Dir(tree, "PASS")
	started := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	for _, r := range []stats.Record{
		{Kind: "session", Run: "r1", Unit: "1.1", Stem: "1.1-0", Agent: "claude sonnet high", Started: started,
			Seconds: 600, CostUSD: 2, Verdict: "landed"},
		{Kind: "check", Run: "r1", Unit: "1.1", Started: started, Seconds: 7, Reason: "verify"},
		{Kind: "session", Run: "r1", Unit: "1.2", Stem: "1.2-0", Agent: "opencode deepseek/deepseek-flash high",
			Started: started, Seconds: 300, CostUSD: 0.05, Verdict: "landed"},
	} {
		if err := stats.Append(dir, r); err != nil {
			t.Fatal(err)
		}
	}
	event := func(used float64) string {
		line, _ := json.Marshal(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{
			"status": "allowed", "unifiedWindows": map[string]any{
				"five_hour": map[string]any{"utilization": used, "resetsAt": 1792066200},
				"seven_day": map[string]any{"utilization": 0.4, "resetsAt": 1792504800},
			}}})
		return string(line)
	}
	write(t, dir, "logs/1.1-0.log", event(0.49)+"\n"+`{"type":"assistant"}`+"\n"+event(0.59)+"\n")

	path := historyPath(context.Background(), e.root)
	for range 2 {
		if _, err := importHistory(context.Background(), e, path); err != nil {
			t.Fatal(err)
		}
	}
	records, err := history.Load(path)
	if err != nil || len(records) != 2 {
		t.Fatalf("records %+v, error %v", records, err)
	}
	first := records[0]
	if first.Plan != "PASS" || first.Branch != "pass" || first.Harness != "claude" ||
		first.Before == nil || first.Before.Session.Used != 0.49 || first.After.Session.Used != 0.59 ||
		!first.After.At.Equal(started.Add(10*time.Minute)) {
		t.Fatalf("record %+v", first)
	}
	if records[1].Harness != "opencode" || records[1].Before != nil {
		t.Fatalf("record %+v", records[1])
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "history.jsonl")); err != nil {
		t.Fatal(err)
	}
}

// A select keeps only a value it offers; anything else becomes the first
// choice, 0% for the reserve.
func TestARememberedReserveStaysOnOffer(t *testing.T) {
	for _, choice := range reserveChoices("25") {
		if choice.Value == "25" {
			return
		}
	}
	t.Fatal("a reserve of 25% was not offered")
}

func TestARememberedNightEffortTheHarnessDropped(t *testing.T) {
	h := allHarnesses().Get("claude")
	remembered := []control.NightAgent{{Agent: control.Agent{Harness: "claude", Model: "opus", Effort: "max"}, Windows: 2}}
	if agent := nightAgentFor(remembered, h); agent.Model != "opus" || agent.Effort != "high" {
		t.Fatalf("agent %+v", agent)
	}
}

// A night agent remembered from a run with another agent of its own does not
// stop a run that is not away, and never reaches it.
func TestTheRunsOwnAgentIsLeftOutOfARememberedNight(t *testing.T) {
	e := &env{harnesses: allHarnesses()}
	night, _ := parseNight(e.harnesses, "--night", "claude=2,codex=1")
	chosen, err := resolveSettings(e, runSettings{Harness: "claude", Effort: "high", Away: "none", Night: night})
	if err != nil {
		t.Fatal(err)
	}
	if chosen.lending != nil || len(chosen.night) != 1 || chosen.night[0].Harness != "codex" {
		t.Fatalf("lending %+v, remembered %+v", chosen.lending, chosen.night)
	}
}

func TestAdjustRefusesTheRunsOwnAgentAsANightAgent(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	night, _ := parseNight(allHarnesses(), "--night", "claude=1")
	live := control.Live{Status: control.Status{PID: os.Getpid(), Agent: "claude sonnet high"}}
	if err := apply(live, control.Settings{Night: night}); err == nil || !strings.Contains(err.Error(), "own agent") {
		t.Fatalf("error %v", err)
	}
}

// A run given every other setting starts without the settings page, as it did
// before the away setting was added; the span then comes from the last run.
func TestEveryOtherSettingGivenSkipsThePage(t *testing.T) {
	g := given{harness: true, model: true, effort: true, secondary: true, peak: true, push: true, finish: true}
	if !g.all() {
		t.Fatal("the settings page is asked for the away span alone")
	}
}

func TestAnUnattendedNightNeedsItsSpan(t *testing.T) {
	e := &env{harnesses: allHarnesses()}
	night, _ := parseNight(e.harnesses, "--night", "claude=2")
	if _, err := unattended(e, runSettings{Harness: "opencode", Effort: "high", Night: night}); err == nil {
		t.Fatal("--night without --away started a run that never uses it")
	}
}

// A unit that moves the week too little to see leaves every window that fits.
func TestAWeekThatBarelyMovesLeavesEveryWindow(t *testing.T) {
	zone := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, 10, 15, 20, 30, 0, 0, zone)
	away, _ := loop.ParseAway("21:00-09:00")
	reset := time.Date(2026, 10, 15, 22, 10, 0, 0, zone)
	var records []history.Record
	for _, unit := range []string{"1", "2", "3"} {
		r := claudeUnit(unit, reset)
		r.After.Weekly.Used = r.Before.Weekly.Used
		records = append(records, r)
	}
	if p := propose(nil, "", "claude sonnet high", records, away, 0.2, 12, now); p.fits != 2 || p.suggest != 2 {
		t.Fatalf("proposal %+v", p)
	}
}

// A plan closed since keeps the name the history knows it by, and two plans
// whose runs started in the same second keep their own sessions.
func TestTheImportKeepsPlansApart(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	path := historyPath(context.Background(), e.root)
	if err := history.Append(path, history.Record{Plan: "GONE", Run: "r0", Unit: "1.1", Stem: "1.1-0", Agent: "claude sonnet high", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"PASS", "GONE"} {
		if err := stats.Append(stats.Dir(tree, name), stats.Record{Kind: "session", Run: "r1", Unit: "1.1", Stem: "1.1-0",
			Agent: "opencode deepseek/deepseek-flash high", Seconds: 60, Verdict: "landed"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := importHistory(context.Background(), e, path); err != nil {
		t.Fatal(err)
	}
	records, _ := history.Load(path)
	plans := map[string]bool{}
	for _, r := range records[1:] {
		plans[r.Plan] = true
	}
	if len(records) != 3 || !plans["PASS"] || !plans["GONE"] {
		t.Fatalf("records %+v", records)
	}
}
