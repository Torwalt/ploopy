package history_test

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/history"
)

var reset = time.Date(2026, 10, 15, 12, 10, 0, 0, time.UTC)

func budget(at time.Time, session, weekly float64) *harness.Budget {
	return &harness.Budget{
		At:      at,
		Session: harness.Allowance{Used: session, ResetAt: reset, Length: harness.SessionWindow},
		Weekly:  harness.Allowance{Used: weekly, ResetAt: reset.Add(5 * 24 * time.Hour), Length: harness.WeeklyWindow},
	}
}

// claude is one session on Claude that took the windows from one share to the
// next.
func claude(unit, verdict string, from, to, weeklyFrom, weeklyTo float64, away bool) history.Record {
	at := reset.Add(-3 * time.Hour)
	return history.Record{
		Plan: "MAP_EDITOR", Unit: unit, Agent: "claude sonnet high", Harness: "claude", Verdict: verdict,
		Seconds: 1200, CostUSD: 2, Before: budget(at, from, weeklyFrom), After: budget(at.Add(20*time.Minute), to, weeklyTo),
		Away: away,
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestAnEstimateIsWhatALandedUnitTook(t *testing.T) {
	records := []history.Record{
		claude("1.1", "landed", 0.49, 0.59, 0.39, 0.40, false),
		claude("1.2", "failed", 0.59, 0.65, 0.40, 0.40, false),
		claude("1.2", "landed", 0.65, 0.72, 0.40, 0.41, false),
		claude("2.1", "landed", 0.72, 0.87, 0.41, 0.42, false),
	}
	e := history.For(records, "claude sonnet high")
	if !e.Known() || !e.Metered() || e.Units != 3 || e.Windowed != 3 {
		t.Fatalf("estimate %+v", e)
	}
	// Both attempts at 1.2 count toward it.
	if !near(e.Session, 0.13) || !near(e.Weekly, 0.01) || !near(e.CostUSD, 2) || !near(e.Minutes, 20) {
		t.Fatalf("estimate %+v", e)
	}
	if !near(e.SessionHigh, 0.14) {
		t.Fatalf("75th percentile %v", e.SessionHigh)
	}
}

// A unit another agent finished says nothing about either agent.
func TestAUnitTwoAgentsShareIsLeftOut(t *testing.T) {
	limited := claude("2.2", "limit", 0.88, 1, 0.42, 0.43, false)
	finished := history.Record{Plan: "MAP_EDITOR", Unit: "2.2", Agent: "opencode deepseek/deepseek-flash high",
		Harness: "opencode", Verdict: "landed", Seconds: 600, CostUSD: 0.03}
	estimates := history.Estimates([]history.Record{limited, finished})
	if len(estimates) != 0 {
		t.Fatalf("estimates %+v", estimates)
	}
}

// A rescue's session is not part of the unit it diagnosed: the unit keeps the
// time and cost of its own agent's attempts only.
func TestARescueSessionIsLeftOutOfItsUnitsEstimate(t *testing.T) {
	records := []history.Record{
		{Plan: "MAP_EDITOR", Unit: "1.1", Agent: "claude sonnet high", Harness: "claude", Verdict: "failed", Seconds: 600, CostUSD: 1},
		{Plan: "MAP_EDITOR", Unit: "1.1", Role: "rescue", Agent: "claude opus high", Harness: "claude", Verdict: "retry", Seconds: 9000, CostUSD: 9},
		{Plan: "MAP_EDITOR", Unit: "1.1", Agent: "claude sonnet high", Harness: "claude", Verdict: "landed", Seconds: 600, CostUSD: 1},
	}
	e := history.For(records, "claude sonnet high")
	if e.Units != 1 || !near(e.Minutes, 20) || !near(e.CostUSD, 2) {
		t.Fatalf("estimate %+v", e)
	}
	if other := history.For(records, "claude opus high"); other.Units != 0 {
		t.Fatalf("the rescue's agent has an estimate: %+v", other)
	}
}

func TestUnitsRunWhileAwayWinOnceThereAreEnough(t *testing.T) {
	var records []history.Record
	for _, unit := range []string{"1", "2", "3"} {
		records = append(records, claude("day."+unit, "landed", 0.1, 0.4, 0.1, 0.12, false))
		records = append(records, claude("night."+unit, "landed", 0.1, 0.2, 0.1, 0.11, true))
	}
	e := history.For(records, "claude sonnet high")
	if !e.Clean || e.Units != 3 || !near(e.Session, 0.1) {
		t.Fatalf("estimate %+v", e)
	}
}

// A window that reset during a session leaves its share unknown.
func TestAResetWindowIsNotAShare(t *testing.T) {
	var records []history.Record
	for _, unit := range []string{"1", "2", "3"} {
		r := claude(unit, "landed", 0.9, 0.1, 0.4, 0.42, false)
		r.After.Session.ResetAt = reset.Add(5 * time.Hour)
		records = append(records, r)
	}
	e := history.For(records, "claude sonnet high")
	if !e.Known() || e.Metered() || e.Windowed != 0 {
		t.Fatalf("estimate %+v", e)
	}
}

func TestTheLogSurvivesARoundTrip(t *testing.T) {
	path := history.Path(t.TempDir())
	first := claude("1.1", "landed", 0.49, 0.59, 0.39, 0.40, false)
	second := claude("1.2", "landed", 0.59, 0.72, 0.40, 0.41, false)
	second.After.At = second.After.At.Add(time.Hour)
	for _, r := range []history.Record{first, second} {
		if err := history.Append(path, r); err != nil {
			t.Fatal(err)
		}
	}
	if filepath.Base(filepath.Dir(path)) != "ploopy" {
		t.Fatalf("path %s", path)
	}
	records, err := history.Load(path)
	if err != nil || len(records) != 2 {
		t.Fatalf("records %+v, error %v", records, err)
	}
	latest := history.Latest(records, "claude")
	if latest == nil || latest.Session.Used != 0.72 {
		t.Fatalf("latest %+v", latest)
	}
	if history.Latest(records, "codex") != nil {
		t.Fatal("a harness never seen has no windows")
	}
}

// An import appends a unit's earlier attempts after the session that landed
// it; they still count toward it.
func TestAttemptsCountInTheOrderTheyRan(t *testing.T) {
	var records []history.Record
	for i, unit := range []string{"1", "2", "3"} {
		failed := claude(unit, "failed", 0.1, 0.2, 0.1, 0.11, false)
		landed := claude(unit, "landed", 0.2, 0.3, 0.11, 0.12, false)
		failed.Started = reset.Add(time.Duration(i) * time.Hour)
		landed.Started = failed.Started.Add(30 * time.Minute)
		records = append(records, landed, failed)
	}
	e := history.For(records, "claude sonnet high")
	if !e.Known() || !near(e.CostUSD, 4) || !near(e.Minutes, 40) || !near(e.Session, 0.2) {
		t.Fatalf("estimate %+v", e)
	}
}
