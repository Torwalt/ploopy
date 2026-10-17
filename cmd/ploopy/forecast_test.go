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
