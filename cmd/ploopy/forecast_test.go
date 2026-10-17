package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/claude"
	"github.com/Torwalt/ploopy/internal/harness/codex"
	"github.com/Torwalt/ploopy/internal/history"
	"github.com/Torwalt/ploopy/internal/loop"
)

func forecastEstimate(agent string, session, weekly, cost, minutes float64) history.Estimate {
	return history.Estimate{
		Agent: agent, Units: history.MinUnits, Windowed: history.MinUnits,
		Session: session, Weekly: weekly, CostUSD: cost, Minutes: minutes,
	}
}

func forecastLease(h harness.Harness, kind loop.LeaseKind, until time.Time) loop.Lease {
	return loop.Lease{
		Agent: loop.Agent{Harness: h, Model: "sonnet", Effort: "high"},
		Kind:  kind, Until: until,
	}
}

func TestLentSubscriptionsShareTheOpenUnitsInLendOrder(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	active := forecastLease(codex.New(), loop.LeaseUntil, now.Add(6*time.Hour))
	activeBudget := &harness.Budget{
		At:      now,
		Session: harness.Allowance{Used: 0.60, ResetAt: now.Add(2 * time.Hour), Length: harness.SessionWindow},
		Weekly:  harness.Allowance{Used: 0.10, ResetAt: now.Add(harness.WeeklyWindow), Length: harness.WeeklyWindow},
	}
	fresh := forecastLease(claude.New(), loop.LeaseUntil, now.Add(10*time.Hour))
	freshBudget := &harness.Budget{
		At:     now,
		Weekly: harness.Allowance{Used: 0.10, ResetAt: now.Add(harness.WeeklyWindow), Length: harness.WeeklyWindow},
	}
	result := forecastRun([]forecastPart{
		{Name: "claude", Lease: &fresh, Budget: freshBudget,
			Estimate: forecastEstimate("claude sonnet high", 0.10, 0.005, 0, 0)},
		{Name: "codex", Lease: &active, Budget: activeBudget,
			Estimate: forecastEstimate("codex gpt-6.1-sol high", 0.10, 0.005, 0, 0)},
	}, "opencode", history.Estimate{}, 20, now, false)

	if len(result.Subs) != 2 {
		t.Fatalf("got %d subscription lines, want 2: %+v", len(result.Subs), result.Subs)
	}
	if result.Subs[0].Name != "codex" || result.Subs[0].Units != 4 {
		t.Fatalf("first line %+v, want the active codex window with 4 units", result.Subs[0])
	}
	if result.Subs[1].Name != "claude" || result.Subs[1].Units != 16 {
		t.Fatalf("second line %+v, want claude with the 16 units left", result.Subs[1])
	}
	if result.Bedrock.Units != 0 {
		t.Fatalf("bedrock took %d units, want 0", result.Bedrock.Units)
	}
	if got, want := result.String(), "≈ codex 4 · claude 16 · opencode 0 units"; got != want {
		t.Fatalf("footer %q, want %q", got, want)
	}
}

func TestAnOpenLeaseWithNoWeeklyEstimateTakesEveryOpenUnit(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	lease := forecastLease(claude.New(), loop.LeaseOpen, time.Time{})
	budget := &harness.Budget{
		At:     now,
		Weekly: harness.Allowance{Used: 0.10, ResetAt: now.Add(harness.WeeklyWindow), Length: harness.WeeklyWindow},
	}
	result := forecastRun([]forecastPart{{
		Name: "claude", Lease: &lease, Budget: budget,
		Estimate: forecastEstimate("claude sonnet high", 0.10, 0, 0, 0),
	}}, "opencode", history.Estimate{}, 12, now, false)

	if len(result.Subs) != 1 || result.Subs[0].Units != 12 {
		t.Fatalf("open lease got %+v, want all 12 units", result.Subs)
	}
	if result.Bedrock.Units != 0 {
		t.Fatalf("bedrock took %d units, want 0", result.Bedrock.Units)
	}
	if got, want := result.String(), "≈ claude 12 · opencode 0 units"; got != want {
		t.Fatalf("footer %q, want %q", got, want)
	}
}

func TestAnOpenLeaseIsLimitedByTheWeek(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	lease := forecastLease(claude.New(), loop.LeaseOpen, time.Time{})
	budget := &harness.Budget{
		At:     now,
		Weekly: harness.Allowance{Used: 0.60, ResetAt: now.Add(harness.WeeklyWindow), Length: harness.WeeklyWindow},
	}
	result := forecastRun([]forecastPart{{
		Name: "claude", Lease: &lease, Budget: budget,
		Estimate: forecastEstimate("claude sonnet high", 0.10, 0.05, 0, 0),
	}}, "opencode", history.Estimate{}, 20, now, false)

	if len(result.Subs) != 1 || result.Subs[0].Units != 8 {
		t.Fatalf("open lease got %+v", result.Subs)
	}
	if result.Bedrock.Units != 12 {
		t.Fatalf("bedrock took %d units, want 12", result.Bedrock.Units)
	}
}

func TestAnUntilLeaseIsLimitedByTheFreshWindowsThatFit(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	lease := forecastLease(claude.New(), loop.LeaseUntil, now.Add(10*time.Hour))
	result := forecastRun([]forecastPart{{
		Name: "claude", Lease: &lease,
		Estimate: forecastEstimate("claude sonnet high", 0.10, 0.05, 0, 0),
	}}, "opencode", history.Estimate{}, 50, now, false)

	if len(result.Subs) != 1 || result.Subs[0].Units != 20 {
		t.Fatalf("until lease got %+v", result.Subs)
	}
	if result.Bedrock.Units != 30 {
		t.Fatalf("bedrock took %d units, want 30", result.Bedrock.Units)
	}
}

func TestANightlyLeaseOutsideItsSpanCountsTheNextSpan(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	span, err := loop.ParseAway("21:00-09:00")
	if err != nil {
		t.Fatal(err)
	}
	lease := forecastLease(claude.New(), loop.LeaseNightly, time.Time{})
	lease.Span = span
	result := forecastRun([]forecastPart{{
		Name: "claude", Lease: &lease,
		Estimate: forecastEstimate("claude sonnet high", 0.10, 0.05, 0, 0),
	}}, "opencode", history.Estimate{}, 50, now, false)

	if len(result.Subs) != 1 || result.Subs[0].Units != 20 {
		t.Fatalf("nightly lease got %+v", result.Subs)
	}
}

func TestASubscriptionWithoutAnEstimateShowsAQuestionMark(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	lease := forecastLease(codex.New(), loop.LeaseOpen, time.Time{})
	result := forecastRun([]forecastPart{{
		Name: "codex", Lease: &lease,
	}}, "opencode", history.Estimate{}, 6, now, false)

	if len(result.Subs) != 1 || !result.Subs[0].Unknown || result.Subs[0].Units != 0 {
		t.Fatalf("unknown subscription got %+v", result.Subs)
	}
	if !strings.Contains(result.String(), "codex ?") {
		t.Fatalf("footer %q lacks the question mark", result.String())
	}
	if result.Bedrock.Units != 6 {
		t.Fatalf("bedrock took %d units, want 6", result.Bedrock.Units)
	}
}

func TestTheBedrockTakesTheRestWithItsCost(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	lease := forecastLease(codex.New(), loop.LeaseOpen, time.Time{})
	result := forecastRun([]forecastPart{{
		Name: "codex", Lease: &lease,
	}}, "opencode", forecastEstimate("opencode deepseek-flash high", 0.2, 0.1, 0.75, 2), 7, now, false)

	if result.Bedrock.Units != 7 {
		t.Fatalf("bedrock took %d units, want 7", result.Bedrock.Units)
	}
	if !result.CostKnown || result.CostUSD != 7*0.75 {
		t.Fatalf("bedrock cost %+v", result)
	}
	if want := now.Add(14 * time.Minute); !result.Done.Equal(want) {
		t.Fatalf("done %s, want %s", result.Done, want)
	}
	if !strings.Contains(result.String(), "≈ $5.25") {
		t.Fatalf("footer %q lacks the bedrock cost", result.String())
	}
}
