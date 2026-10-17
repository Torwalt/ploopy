package loop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/fake"
	"github.com/Torwalt/ploopy/internal/history"
)

var berlin = time.FixedZone("CEST", 2*60*60)

func at(day, hour, minute int) time.Time { return time.Date(2026, 10, day, hour, minute, 0, 0, berlin) }

func TestAwayIsADailySpanThatMayWrapMidnight(t *testing.T) {
	away, err := ParseAway("21:00-09:00")
	if err != nil || away.String() != "21:00-09:00" {
		t.Fatalf("away %v, error %v", away, err)
	}
	for _, c := range []struct {
		now        time.Time
		start, end time.Time
		inside     bool
	}{
		{at(15, 18, 0), at(15, 21, 0), at(16, 9, 0), false},
		{at(15, 23, 0), at(15, 21, 0), at(16, 9, 0), true},
		{at(16, 3, 0), at(15, 21, 0), at(16, 9, 0), true},
		{at(16, 9, 0), at(16, 21, 0), at(17, 9, 0), false},
	} {
		start, end, inside := away.Span(c.now)
		if !start.Equal(c.start) || !end.Equal(c.end) || inside != c.inside {
			t.Errorf("at %v: %v–%v inside %v", c.now, start, end, inside)
		}
	}
	for _, bad := range []string{"21:00", "25:00-09:00", "09:00-09:00", "nine-ten"} {
		if _, err := ParseAway(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func windows(at time.Time, session float64, reset time.Time, weekly float64) *harness.Budget {
	return &harness.Budget{
		At:      at,
		Session: harness.Allowance{Used: session, ResetAt: reset, Length: harness.SessionWindow},
		Weekly:  harness.Allowance{Used: weekly, ResetAt: at.Add(4 * 24 * time.Hour), Length: harness.WeeklyWindow},
	}
}

// weeklyOnly is windows with no session window running, and the week resetting
// as asked.
func weeklyOnly(at time.Time, used float64, resetsIn time.Duration) *harness.Budget {
	return &harness.Budget{
		At:     at,
		Weekly: harness.Allowance{Used: used, ResetAt: at.Add(resetsIn), Length: harness.WeeklyWindow},
	}
}

func TestALeaseHasRoomOnlyWhileItsWindowsDo(t *testing.T) {
	claude := &fake.Harness{Named: "claude"}
	nightly, _ := ParseAway("21:00-09:00")
	now := at(15, 23, 0)
	nightlyLease := Lease{Agent: Agent{Harness: claude}, Kind: LeaseNightly, Span: nightly, Keep: 0.2}
	until := Lease{Agent: Agent{Harness: claude}, Kind: LeaseUntil, Until: now.Add(time.Hour), Keep: 0.2}
	open := Lease{Agent: Agent{Harness: claude}, Kind: LeaseOpen, Keep: 0.2}
	for _, c := range []struct {
		name    string
		lease   Lease
		lending Lending
		budget  *harness.Budget
		own     bool
		limited time.Time
		now     time.Time
		ok      bool
		fresh   bool
	}{
		{"unknown windows open a fresh one", nightlyLease, Lending{}, nil, false, time.Time{}, now, true, true},
		{"the author's window resets before the end", nightlyLease, Lending{}, windows(now, 0.5, at(16, 1, 0), 0.5), false, time.Time{}, now, true, false},
		{"a spent window waits", nightlyLease, Lending{}, windows(now, 1, at(16, 1, 0), 0.5), false, time.Time{}, now, false, false},
		{"a window past the end is not the run's", nightlyLease, Lending{}, windows(now, 0.1, at(16, 10, 0), 0.5), false, time.Time{}, now, false, false},
		{"a window past the end is the run's own", nightlyLease, Lending{}, windows(now, 0.1, at(16, 10, 0), 0.5), true, time.Time{}, now, true, false},
		{"a limit holds", nightlyLease, Lending{}, nil, false, at(16, 1, 0), now, false, false},
		{"the weekly keep holds", nightlyLease, Lending{}, windows(now, 0.5, at(16, 1, 0), 0.9), false, time.Time{}, now, false, false},
		{"a fresh window must end in time", nightlyLease, Lending{}, windows(now, 0.4, at(15, 22, 0), 0.5), false, time.Time{}, at(16, 4, 30), false, false},
		{"keeping the whole week holds", Lease{Agent: Agent{Harness: claude}, Kind: LeaseOpen, Keep: 1}, Lending{}, nil, false, time.Time{}, now, false, false},
		{"an open lease has no end", open, Lending{}, nil, false, time.Time{}, now, true, true},
		{"spending all ignores the keep", Lease{Agent: Agent{Harness: claude}, Kind: LeaseOpen, Keep: 1}, Lending{SpendAll: true}, nil, false, time.Time{}, now, true, true},
		{"a passed until lease lends nothing", Lease{Agent: Agent{Harness: claude}, Kind: LeaseUntil, Until: now.Add(-time.Minute)}, Lending{}, nil, false, time.Time{}, now, false, false},
		{"an until lease whose window resets before its end", until, Lending{}, windows(now, 0.5, now.Add(30*time.Minute), 0.5), false, time.Time{}, now, true, false},
		{"an until lease whose window resets after its end", until, Lending{}, windows(now, 0.1, now.Add(2*time.Hour), 0.5), false, time.Time{}, now, false, false},
	} {
		ok, fresh, why := c.lending.Room(c.lease, c.budget, c.own, c.limited, c.now)
		if ok != c.ok || fresh != c.fresh || why == "" {
			t.Errorf("%s: ok %v fresh %v (%s)", c.name, ok, fresh, why)
		}
	}
}

func TestEffectiveKeepShrinksAsTheWeeklyResetNears(t *testing.T) {
	now := at(15, 12, 0)
	for _, c := range []struct {
		name     string
		used     float64
		resetsIn time.Duration
		spendAll bool
		want     float64
		room     bool
	}{
		{"51% with 120h left", 0.51, 120 * time.Hour, false, 0.2 * 120 / 168, true},
		{"85% with 120h left", 0.85, 120 * time.Hour, false, 0.2 * 120 / 168, true},
		{"86% with 120h left", 0.86, 120 * time.Hour, false, 0.2 * 120 / 168, false},
		{"93% with 36h left", 0.93, 36 * time.Hour, false, 0.2 * 36 / 168, true},
		{"93% with 36h left spending all", 0.93, 36 * time.Hour, true, 0, true},
		{"99% with 6h left", 0.99, 6 * time.Hour, false, 0.2 * 6 / 168, true},
		{"40% with an unknown reset", 0.40, 0, false, 0.2, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := weeklyOnly(now, c.used, c.resetsIn)
			lease := Lease{
				Agent: Agent{Harness: &fake.Harness{Named: "claude"}},
				Kind:  LeaseOpen,
				Keep:  0.2,
			}
			got := EffectiveKeep(lease, b, now, c.spendAll)
			if math.Abs(got-c.want) > 1e-9 {
				t.Fatalf("keep %.4f, want %.4f", got, c.want)
			}
			lending := Lending{SpendAll: c.spendAll}
			ok, _, why := lending.Room(lease, b, false, time.Time{}, now)
			if ok != c.room || why == "" {
				t.Fatalf("room %v (%s), want %v", ok, why, c.room)
			}
		})
	}
}

func TestAWholeWeekKeptNeverShrinks(t *testing.T) {
	now := at(15, 12, 0)
	lease := Lease{Agent: Agent{Harness: &fake.Harness{Named: "claude"}}, Kind: LeaseOpen, Keep: 1}
	b := weeklyOnly(now, 0.99, time.Hour)

	if got := EffectiveKeep(lease, b, now, false); got != 1 {
		t.Fatalf("keep %v, want 1", got)
	}
	ok, _, why := (Lending{}).Room(lease, b, false, time.Time{}, now)
	if ok || !strings.Contains(why, "whole week stays yours") {
		t.Fatalf("room %v (%s), want the whole week kept", ok, why)
	}
}

func TestTheMostOfItsWeekToLoseGoesFirst(t *testing.T) {
	now := at(15, 23, 0)
	l, _, codex := lendingLoop(t, now, 0.2, 0)
	claudeBudget := weeklyOnly(now, 0.40, 96*time.Hour)
	codexBudget := weeklyOnly(now, 0.10, 24*time.Hour)
	l.meterOf("claude").budget = claudeBudget
	l.meterOf("codex").budget = codexBudget

	if got := EffectiveKeep(l.opts.Lending.Leases[0], claudeBudget, now, false); math.Abs(got-0.2*96/168) > 1e-9 {
		t.Fatalf("claude keeps %v of its week", got)
	}
	if got := lendUrgency(l.opts.Lending.Leases[1], codexBudget, now, false); math.Abs(got-0.9/24) > 1e-9 {
		t.Fatalf("codex urgency %v%%/h, want 3.75", got*100)
	}

	agent, fresh, _ := l.lendPick()
	if agent == nil || agent.Harness.Name() != codex.Name() || !fresh {
		t.Fatalf("picked %+v fresh %v, want codex opening a fresh window", agent, fresh)
	}
}

func TestAnOpenWindowGoesBeforeAFreshOne(t *testing.T) {
	now := at(15, 23, 0)
	l, _, _ := lendingLoop(t, now, 0.2, 0)
	running := weeklyOnly(now, 0.40, 96*time.Hour)
	running.Session = harness.Allowance{Used: 0.5, ResetAt: now.Add(time.Hour), Length: harness.SessionWindow}
	l.meterOf("claude").budget = running
	l.meterOf("codex").budget = weeklyOnly(now, 0.10, 24*time.Hour)

	agent, fresh, _ := l.lendPick()
	if agent == nil || agent.Harness.Name() != "claude" || fresh {
		t.Fatalf("picked %+v fresh %v, want claude's open window", agent, fresh)
	}
}

func TestAnUnknownWeeklyBudgetGoesLastAmongFreshWindows(t *testing.T) {
	now := at(15, 23, 0)
	claude := &fake.Harness{Named: "claude"}
	codex := &fake.Harness{Named: "codex"}
	other := &fake.Harness{Named: "other"}
	lease := func(h *fake.Harness) Lease {
		return Lease{Agent: Agent{Harness: h}, Kind: LeaseOpen, Keep: 0.2}
	}
	choices := []lendChoice{
		{lease: lease(claude), b: nil, fresh: true},
		{lease: Lease{Agent: Agent{Harness: codex}, Kind: LeaseOpen, Keep: 0}, b: weeklyOnly(now, 0.10, 24*time.Hour), fresh: true},
		{lease: lease(other), b: weeklyOnly(now, 0.50, 100*time.Hour), fresh: true},
	}
	lendOrder(choices, now, false)

	var names []string
	for _, c := range choices {
		names = append(names, c.lease.Harness.Name())
	}
	if got := strings.Join(names, " "); got != "codex other claude" {
		t.Fatalf("order %s, want codex other claude", got)
	}
}

// Of two windows already open, the one that expires first leads.
func TestTheEarlierOpenWindowGoesFirst(t *testing.T) {
	now := at(15, 23, 0)
	claude := &fake.Harness{Named: "claude"}
	codex := &fake.Harness{Named: "codex"}
	lease := func(h *fake.Harness) Lease {
		return Lease{Agent: Agent{Harness: h}, Kind: LeaseOpen, Keep: 0}
	}
	leases := []Lease{lease(claude), lease(codex)}
	budgets := map[string]*harness.Budget{
		"claude": windows(now, 0.5, now.Add(2*time.Hour), 0.5),
		"codex":  windows(now, 0.5, now.Add(time.Hour), 0.5),
	}
	LendOrder(leases, budgets, now, false)

	if got := leases[0].Harness.Name(); got != "codex" {
		t.Fatalf("first lease %s, want codex's earlier reset", got)
	}
}

// Fresh leases with nothing to choose between them keep the lease file's order.
func TestFreshLeasesOfEqualUrgencyKeepLeaseOrder(t *testing.T) {
	now := at(15, 23, 0)
	claude := &fake.Harness{Named: "claude"}
	codex := &fake.Harness{Named: "codex"}
	lease := func(h *fake.Harness) Lease {
		return Lease{Agent: Agent{Harness: h}, Kind: LeaseOpen, Keep: 0.2}
	}
	budgets := map[string]*harness.Budget{
		"claude": weeklyOnly(now, 0.10, 24*time.Hour),
		"codex":  weeklyOnly(now, 0.10, 24*time.Hour),
	}
	for _, order := range [][]*fake.Harness{{codex, claude}, {claude, codex}} {
		leases := []Lease{lease(order[0]), lease(order[1])}
		LendOrder(leases, budgets, now, false)
		if got := leases[0].Harness.Name(); got != order[0].Name() {
			t.Fatalf("lease order %s %s became %s first", order[0].Name(), order[1].Name(), got)
		}
	}
}

// Without a weekly window there is nothing to shrink against.
func TestEffectiveKeepWithoutAWindowKeepsTheLeaseShare(t *testing.T) {
	now := at(15, 23, 0)
	lease := Lease{Agent: Agent{Harness: &fake.Harness{Named: "claude"}}, Kind: LeaseOpen, Keep: 0.2}

	if got := EffectiveKeep(lease, nil, now, false); math.Abs(got-lease.Keep) > 1e-9 {
		t.Fatalf("keep %v, want the lease's %v", got, lease.Keep)
	}
	ok, _, why := (Lending{}).Room(lease, nil, false, time.Time{}, now)
	if !ok || why == "" {
		t.Fatalf("room %v (%s), want the lease to have room", ok, why)
	}
}

// A weekly reset beyond the week does not shrink the reserve past the lease's
// share.
func TestAWeekResettingBeyondSevenDaysKeepsTheLeaseShare(t *testing.T) {
	now := at(15, 23, 0)
	lease := Lease{Agent: Agent{Harness: &fake.Harness{Named: "claude"}}, Kind: LeaseOpen, Keep: 0.2}
	b := weeklyOnly(now, 0.10, 200*time.Hour)

	if got := EffectiveKeep(lease, b, now, false); math.Abs(got-lease.Keep) > 1e-9 {
		t.Fatalf("keep %v, want the lease's %v", got, lease.Keep)
	}
}

// Spending all drops the reserve, so the lease with the most room leads instead
// of the one keeping the most back.
func TestSpendingAllGivesTheWeekToTheLeaseWithMoreRoom(t *testing.T) {
	now := at(15, 23, 0)
	claude := &fake.Harness{Named: "claude"}
	codex := &fake.Harness{Named: "codex"}
	leasesFor := func() []Lease {
		return []Lease{
			{Agent: Agent{Harness: claude}, Kind: LeaseOpen, Keep: 0.2},
			{Agent: Agent{Harness: codex}, Kind: LeaseOpen, Keep: 0},
		}
	}
	budgets := map[string]*harness.Budget{
		"claude": weeklyOnly(now, 0.50, 24*time.Hour),
		"codex":  weeklyOnly(now, 0.51, 24*time.Hour),
	}

	leases := leasesFor()
	LendOrder(leases, budgets, now, false)
	if got := leases[0].Harness.Name(); got != "codex" {
		t.Fatalf("keeping the reserve put %s first, want codex", got)
	}

	leases = leasesFor()
	LendOrder(leases, budgets, now, true)
	if got := leases[0].Harness.Name(); got != "claude" {
		t.Fatalf("spending all put %s first, want claude", got)
	}
}

// A weekly reset that already passed leaves nothing to lose by waiting.
func TestAWeekAlreadyResetHasNoUrgency(t *testing.T) {
	now := at(15, 23, 0)
	lease := Lease{Agent: Agent{Harness: &fake.Harness{Named: "claude"}}, Kind: LeaseOpen, Keep: 0.2}
	b := weeklyOnly(now, 0.10, -time.Hour)

	if got := lendUrgency(lease, b, now, false); got != 0 {
		t.Fatalf("urgency %v, want 0 for a week that already reset", got)
	}
}

// lendingLoop builds a loop with an open lease for claude then codex, keeping
// the shares asked for, and a fixed clock.
func lendingLoop(t *testing.T, now time.Time, claudeKeep, codexKeep float64) (*Loop, *fake.Harness, *fake.Harness) {
	t.Helper()
	f := setup(t, twoUnits)
	f.h.Named = "opencode"
	claude := &fake.Harness{Named: "claude"}
	codex := &fake.Harness{Named: "codex"}
	f.opts.Lending = &Lending{Leases: []Lease{
		{Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"}, Kind: LeaseOpen, Keep: claudeKeep},
		{Agent: Agent{Harness: codex, Model: "gpt-6.1-sol", Effort: "high"}, Kind: LeaseOpen, Keep: codexKeep},
	}}
	f.opts.Now = func() time.Time { return now }
	runner, err := New(f.root, f.opts, &silent{})
	if err != nil {
		t.Fatal(err)
	}
	return runner, claude, codex
}

func TestTheAwaySpanKeepsItsClockTimesAcrossDaylightSaving(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	local := func(month time.Month, day, hour, minute int) time.Time {
		return time.Date(2026, month, day, hour, minute, 0, 0, zone)
	}
	night, _ := ParseAway("21:00-09:00")
	early, _ := ParseAway("01:00-07:00")
	for _, c := range []struct {
		away       Away
		now        time.Time
		start, end time.Time
		inside     bool
	}{
		// Clocks go back an hour on 25 October and forward on 29 March.
		{night, local(10, 24, 23, 0), local(10, 24, 21, 0), local(10, 25, 9, 0), true},
		{night, local(10, 25, 20, 30), local(10, 25, 21, 0), local(10, 26, 9, 0), false},
		{night, local(3, 28, 23, 0), local(3, 28, 21, 0), local(3, 29, 9, 0), true},
		{night, local(3, 29, 20, 30), local(3, 29, 21, 0), local(3, 30, 9, 0), false},
		{early, local(3, 29, 0, 30), local(3, 29, 1, 0), local(3, 29, 7, 0), false},
		{early, local(10, 25, 6, 30), local(10, 25, 1, 0), local(10, 25, 7, 0), true},
	} {
		start, end, inside := c.away.Span(c.now)
		if !start.Equal(c.start) || !end.Equal(c.end) || inside != c.inside {
			t.Errorf("%s at %v: %v–%v inside %v", c.away, c.now, start, end, inside)
		}
	}
}

// nightFixture runs two units with a bedrock that is not metered and one
// nightly lease on claude, on a clock that moves with every session.
func nightFixture(t *testing.T, primary, sub []fake.Step, now time.Time) (*fixture, *fake.Harness, *time.Time) {
	t.Helper()
	return nightFixtureFor(t, twoUnits, primary, sub, now)
}

func nightFixtureFor(t *testing.T, planText string, primary, sub []fake.Step, now time.Time) (*fixture, *fake.Harness, *time.Time) {
	t.Helper()
	f := setup(t, planText, primary...)
	f.h.Named = "opencode"
	claude := fake.New(sub...)
	claude.Named = "claude"
	away, _ := ParseAway("21:00-09:00")
	f.opts.Lending = &Lending{Leases: []Lease{{
		Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"},
		Kind:  LeaseNightly, Span: away, Keep: 0.2,
	}}}
	f.opts.History = history.Path(t.TempDir())
	clock := now
	f.opts.Now = func() time.Time { return clock }
	f.opts.Sleep = func(_ context.Context, d time.Duration) error {
		f.slept = append(f.slept, d)
		clock = clock.Add(d)
		return nil
	}
	return f, claude, &clock
}

func spends(clock **time.Time, name, message string, before, after *harness.Budget) fake.Step {
	return fake.Step{
		Do: func(dir string) error {
			**clock = (**clock).Add(30 * time.Minute)
			return commits(name, message)(dir)
		},
		Outcome: harness.Outcome{Marker: harness.Done, BudgetBefore: before, Budget: after},
	}
}

func TestWhileAwayTheSubscriptionTakesTheUnits(t *testing.T) {
	start := at(15, 22, 0)
	var clock *time.Time
	fresh := start.Add(5 * time.Hour)
	f, claude, c := nightFixture(t, nil, []fake.Step{
		spends(&clock, "one.go", "first", windows(start, 0, fresh, 0.5), windows(start, 0.2, fresh, 0.52)),
		spends(&clock, "two.go", "second", windows(start, 0.2, fresh, 0.52), windows(start, 0.4, fresh, 0.54)),
	}, start)
	clock = c

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 2 || f.h.Started() != 0 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
	records, err := history.Load(f.opts.History)
	if err != nil || len(records) != 2 {
		t.Fatalf("history %+v, error %v", records, err)
	}
	first := records[0]
	if !first.Away || first.Harness != "claude" || first.Plan != "PASS" || first.After.Session.Used != 0.2 || first.WorkOrder == 0 {
		t.Fatalf("record %+v", first)
	}
}

func TestALentSubscriptionAtItsLimitHandsOverWithoutWaiting(t *testing.T) {
	start := at(15, 22, 0)
	var clock *time.Time
	reset := start.Add(3 * time.Hour)
	limit := fake.Step{Outcome: harness.Outcome{
		RateLimit: &harness.RateLimit{Reason: "session limit", ResetAt: reset},
		Budget:    windows(start, 1, reset, 0.5),
	}}
	f, claude, c := nightFixture(t,
		[]fake.Step{
			{Do: commits("one.go", "first"), Outcome: done()},
			{Do: commits("two.go", "second"), Outcome: done()},
		},
		[]fake.Step{limit}, start)
	clock = c
	_ = clock

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 0 {
		t.Fatalf("the run waited %v", f.slept)
	}
	if claude.Started() != 1 || f.h.Started() != 2 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
	records, err := history.Load(f.opts.History)
	if err != nil {
		t.Fatal(err)
	}
	var bedrock int
	for _, record := range records {
		wantAway := record.Harness == "claude"
		if record.Away != wantAway {
			t.Fatalf("record %+v: away %v, want %v", record, record.Away, wantAway)
		}
		if record.Harness == "opencode" {
			bedrock++
		}
	}
	if bedrock != 2 || len(records) != 3 {
		t.Fatalf("history %+v, want one lent and two bedrock sessions", records)
	}
}

func TestOutsideTheAwaySpanThePrimaryRuns(t *testing.T) {
	f, claude, _ := nightFixture(t, []fake.Step{
		{Do: commits("one.go", "first"), Outcome: done()},
		{Do: commits("two.go", "second"), Outcome: done()},
	}, nil, at(15, 14, 0))

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 0 || f.h.Started() != 2 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
	records, _ := history.Load(f.opts.History)
	if len(records) != 2 || records[0].Away {
		t.Fatalf("history %+v", records)
	}
}

// The week is the author's too: past the keep the subscription rests.
func TestTheWeeklyKeepKeepsTheSubscriptionOut(t *testing.T) {
	start := at(15, 22, 0)
	f, claude, _ := nightFixture(t, []fake.Step{
		{Do: commits("one.go", "first"), Outcome: done()},
		{Do: commits("two.go", "second"), Outcome: done()},
	}, nil, start)
	claude.Meter = windows(start, 0.1, start.Add(time.Hour), 0.9)

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 0 || f.h.Started() != 2 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

func TestADegradedAdapterIsBookedForTheEndOfTheRun(t *testing.T) {
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "first"), Outcome: harness.Outcome{
			Marker: harness.Done, Degraded: []string{"no result event: cost, tokens and turns are unknown"}}},
		fake.Step{Do: commits("two.go", "second"), Outcome: done()},
	)
	result := f.run()
	if result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	degraded := result.Stats.Degraded
	if len(degraded) != 1 || degraded[0].Sessions != 1 || degraded[0].Of != 2 ||
		!strings.Contains(degraded[0].Notes[0], "no result event") || filepath.Ext(degraded[0].Logs[0]) != ".log" {
		t.Fatalf("degraded %+v", degraded)
	}
	var marked int
	for _, r := range f.records() {
		if r.Kind == "session" && len(r.Degraded) > 0 {
			marked++
		}
	}
	if marked != 1 {
		t.Fatalf("%d session records marked degraded, want 1", marked)
	}
}

// threeUnits is twoUnits with a third unit, for leases that span windows.
const threeUnits = twoUnits + `
### 1.3 Three

**Goal:** the third thing.

**Done when:** it is done.
`

// A subscription whose sessions report no windows opened one window with its
// first session, not one per session.
func TestAWindowNoSessionReportedRunsUntilItWouldReset(t *testing.T) {
	start := at(15, 22, 0)
	var clock *time.Time
	f, claude, c := nightFixture(t, nil, []fake.Step{
		spends(&clock, "one.go", "first", nil, nil),
		spends(&clock, "two.go", "second", nil, nil),
	}, start)
	clock = c

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 2 || f.h.Started() != 0 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

// The window a later session reports is the one an earlier session opened
// without reporting it, not a second one.
func TestAWindowSeenLaterIsTheOneTheRunBooked(t *testing.T) {
	start := at(15, 21, 0)
	var clock *time.Time
	opened := start.Add(5*time.Hour + 30*time.Second)
	seen := spends(&clock, "two.go", "second", windows(start, 0.5, opened, 0.5), windows(start, 0.9, opened, 0.52))
	inner := seen.Do
	// The unit runs the window out, so the third needs a fresh one.
	seen.Do = func(dir string) error {
		*clock = opened.Add(time.Minute)
		return inner(dir)
	}
	f, claude, c := nightFixtureFor(t, threeUnits, nil, []fake.Step{
		spends(&clock, "one.go", "first", nil, nil),
		seen,
		spends(&clock, "three.go", "third", nil, nil),
	}, start)
	clock = c

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 3 || f.h.Started() != 0 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

// A window the run opened is its own to use up, even when it resets a moment
// after the author is back.
func TestAFreshWindowOpenedLastIsUsedUp(t *testing.T) {
	start := at(16, 4, 0)
	var clock *time.Time
	reset := start.Add(5*time.Hour + 30*time.Second)
	f, claude, c := nightFixture(t, nil, []fake.Step{
		spends(&clock, "one.go", "first", windows(start, 0, reset, 0.5), windows(start, 0.3, reset, 0.51)),
		spends(&clock, "two.go", "second", windows(start, 0.3, reset, 0.51), windows(start, 0.6, reset, 0.52)),
	}, start)
	clock = c

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 2 || f.h.Started() != 0 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

// The fresh window an until lease opened near its end is still the run's own
// when the harness reports a reset a few minutes off the booked one.
func TestAnUntilLeaseUsesAFreshWindowItOpenedNearItsEnd(t *testing.T) {
	start := at(15, 22, 0)
	booked := start.Add(harness.SessionWindow)
	reported := booked.Add(8 * time.Minute)
	f, claude, _ := nightFixture(t, nil, []fake.Step{
		{Do: commits("one.go", "first"), Outcome: harness.Outcome{
			Marker: harness.Done,
			Budget: windows(start, 0.2, reported, 0.5),
		}},
		{Do: commits("two.go", "second"), Outcome: done()},
	}, start)
	f.opts.Lending = &Lending{Leases: []Lease{{
		Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"},
		Kind:  LeaseUntil, Until: booked.Add(3 * time.Minute), Keep: 0,
	}}}

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 2 || f.h.Started() != 0 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

// A subscription that cannot run, logged out or not installed, hands the
// session over like one at its limit instead of failing the night.
func TestALentSubscriptionThatCannotRunHandsOver(t *testing.T) {
	for _, broken := range []fake.Step{
		{Outcome: harness.Outcome{Exit: 1}},
		{Err: errors.New(`exec: "claude": executable file not found in $PATH`)},
	} {
		f, claude, _ := nightFixture(t, []fake.Step{
			{Do: commits("one.go", "first"), Outcome: done()},
			{Do: commits("two.go", "second"), Outcome: done()},
		}, []fake.Step{broken}, at(15, 22, 0))

		if result := f.run(); result.Status != "done" {
			t.Fatalf("run %+v", result)
		}
		if len(f.slept) != 0 {
			t.Fatalf("the run waited %v", f.slept)
		}
		if claude.Started() != 1 || f.h.Started() != 2 {
			t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
		}
	}
}

// A lent agent that cannot start must not consume the red preflight's tail:
// the bedrock's first prompt is the first session that actually starts.
func TestAnInheritedRedPreflightSurvivesALentAgentThatCannotStart(t *testing.T) {
	f, claude, _ := nightFixture(t, []fake.Step{
		{Do: func(dir string) error {
			if err := os.Remove(filepath.Join(dir, "inherited.txt")); err != nil {
				return err
			}
			return commits("one.go", "first")(dir)
		}, Outcome: done()},
		{Do: commits("two.go", "second"), Outcome: done()},
	}, []fake.Step{{Err: errors.New(`exec: "claude": executable file not found in $PATH`)}}, at(15, 22, 0))
	writeFile(t, f.root, "inherited.txt", "uncommitted\n")
	f.opts.VerifyCmd = `test ! -f inherited.txt || { echo "inherited.txt is in the way"; exit 1; }`

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 1 {
		t.Fatalf("the lent agent was asked for %d sessions", claude.Started())
	}
	if f.h.Started() < 1 {
		t.Fatal("no bedrock session ran")
	}
	prompt := f.h.Prompts[0]
	for _, want := range []string{
		"## Uncommitted changes in the tree",
		"?? inherited.txt",
		"Verify is red with these changes:",
		"inherited.txt is in the way",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the first bedrock prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// The session a crashed run left unjudged is booked to the agent that ran it,
// whoever would take the next one.
func TestAnUnjudgedNightSessionIsBookedToItsAgent(t *testing.T) {
	f, _, _ := nightFixture(t, []fake.Step{{Do: commits("one.go", "first"), Outcome: done()}}, nil, at(15, 14, 0))
	f.opts.Until = "1.1"
	if result := f.run(); result.Status != "done" {
		t.Fatalf("first run %+v", result)
	}

	started := at(15, 23, 0)
	base := strings.TrimSpace(f.git("rev-parse", "HEAD~1"))
	writeFile(t, f.root, ".ploopy/pass/progress.json", fmt.Sprintf(
		`{"unit":"1.1","base":%q,"session":{"head":"x","outcome":{"Marker":"DONE"},"stem":"1.1-0","tail":"DONE",`+
			`"harness":"claude","model":"sonnet","effort":"high","info":{"started":%q,"took":600000000000,"away":true}}}`,
		base, started.Format(time.RFC3339)))
	s := f.state()
	s.Clear("1.1")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "reset progress for the test")

	f.opts.Until = ""
	f.h.Steps = append(f.h.Steps, fake.Step{Do: commits("two.go", "second"), Outcome: done()})
	if result := f.run(); result.Status != "done" {
		t.Fatalf("second run %+v", result)
	}
	if entry := f.state().Entry("1.1"); entry.Agent != "claude sonnet high" || entry.Harness != "claude" {
		t.Fatalf("unit 1.1 was recorded as %q on %q", entry.Agent, entry.Harness)
	}
	records, _ := history.Load(f.opts.History)
	var replayed *history.Record
	for i := range records {
		if records[i].Stem == "1.1-0" && records[i].Harness == "claude" {
			replayed = &records[i]
		}
	}
	if replayed == nil || !replayed.Started.Equal(started) || !replayed.Away || replayed.Seconds != 600 {
		t.Fatalf("history %+v", records)
	}
}

// A unit that names its own agent waits out that agent's limit: a lease
// getting room does not help it.
func TestAUnitsOwnAgentIsNotWokenByLending(t *testing.T) {
	plan := strings.Replace(twoUnits, "**Goal:** the first thing.", "Agent: own\n\n**Goal:** the first thing.", 1)
	start := at(15, 22, 0)
	f, claude, _ := nightFixtureFor(t, plan, nil, []fake.Step{{Do: commits("two.go", "second"), Outcome: done()}}, start)
	own := fake.New(limited(at(16, 1, 0)), fake.Step{Do: commits("one.go", "first"), Outcome: done()})
	own.Named = "own"
	f.opts.Harnesses = harness.Set{f.h, own}
	claude.Meter = windows(start, 1, at(15, 23, 0), 0.5)

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 1 || f.slept[0] != 3*time.Hour {
		t.Fatalf("slept %v, want one wait for the own agent's reset", f.slept)
	}
}

// The bedrock waiting out peak hours is woken when a nightly lease begins.
func TestABedrockInPeakHoursWakesWhenANightlyLeaseBegins(t *testing.T) {
	start := at(15, 20, 30) // 18:30 UTC, half an hour before 21:00 local
	f, claude, _ := nightFixture(t, nil, []fake.Step{
		{Do: commits("one.go", "first"), Outcome: done()},
		{Do: commits("two.go", "second"), Outcome: done()},
	}, start)
	f.h.Windows = []harness.Window{{Days: []time.Weekday{start.Weekday()}, Start: 18 * 60, End: 21 * 60}}
	f.opts.WaitOffPeak = true

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 1 || f.slept[0] != 30*time.Minute {
		t.Fatalf("slept %v, want one wait of 30m to the lease's start", f.slept)
	}
	if claude.Started() != 2 || f.h.Started() != 0 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

// The bedrock waiting out its own limit is woken when a lent subscription's
// limit lifts.
func TestABedrockLimitWaitWakesWhenALentLimitLifts(t *testing.T) {
	start := at(15, 21, 0)
	lifted := start.Add(time.Hour)
	f, claude, _ := nightFixtureFor(t, twoUnits, []fake.Step{
		{Outcome: harness.Outcome{RateLimit: &harness.RateLimit{Reason: "session limit", ResetAt: start.Add(2 * time.Hour)}}},
	}, []fake.Step{
		{Outcome: harness.Outcome{RateLimit: &harness.RateLimit{Reason: "session limit", ResetAt: lifted}}},
		{Do: commits("one.go", "first"), Outcome: done()},
	}, start)
	f.opts.Lending = &Lending{Leases: []Lease{{
		Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"},
		Kind:  LeaseOpen, Keep: 0,
	}}}
	f.opts.Until = "1.1"

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 1 || f.slept[0] != time.Hour {
		t.Fatalf("slept %v, want one wait of 1h to the lent limit's lift", f.slept)
	}
	if claude.Started() != 2 || f.h.Started() != 1 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

// The bedrock waiting out its own limit is woken when a spent window resets.
func TestABedrockLimitWaitWakesWhenASpentWindowResets(t *testing.T) {
	start := at(15, 21, 0)
	reset := start.Add(time.Hour)
	f, claude, _ := nightFixtureFor(t, twoUnits, []fake.Step{
		{Outcome: harness.Outcome{RateLimit: &harness.RateLimit{Reason: "session limit", ResetAt: start.Add(2 * time.Hour)}}},
	}, []fake.Step{
		{Do: commits("one.go", "first"), Outcome: done()},
	}, start)
	f.opts.Lending = &Lending{Leases: []Lease{{
		Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"},
		Kind:  LeaseOpen, Keep: 0,
	}}}
	claude.Meter = windows(start, 1, reset, 0.1)
	f.opts.Until = "1.1"

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 1 || f.slept[0] != time.Hour+2*time.Minute {
		t.Fatalf("slept %v, want one wait of 1h2m to the window's reset", f.slept)
	}
	if claude.Started() != 1 || f.h.Started() != 1 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

// A unit due in the primary's peak hours waits for off-peak when the lease's
// windows, read before the gate, leave it no room.
func TestThePeakGateGoesByTheWindowsReadNow(t *testing.T) {
	start := at(16, 3, 30) // Friday 01:30 UTC
	var clock *time.Time
	var first time.Time
	f, claude, c := nightFixture(t, []fake.Step{
		{Do: func(dir string) error {
			first = *clock
			return commits("one.go", "first")(dir)
		}, Outcome: done()},
		{Do: commits("two.go", "second"), Outcome: done()},
	}, nil, start)
	clock = c
	f.h.Windows = []harness.Window{{Days: []time.Weekday{time.Friday}, Start: 60, End: 4 * 60}}
	f.opts.WaitOffPeak = true
	claude.Meter = windows(start, 0.1, start.Add(time.Hour), 0.9)

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 0 || !first.Equal(at(16, 6, 0)) {
		t.Fatalf("claude ran %d sessions; 1.1 started at %v, want 04:00 UTC", claude.Started(), first)
	}
}

// An open lease opens a fresh window whenever none is running.
func TestAnOpenLeaseOpensConsecutiveFreshWindows(t *testing.T) {
	start := at(15, 22, 0)
	var clock *time.Time
	first := spends(&clock, "one.go", "first", nil, nil)
	inner := first.Do
	first.Do = func(dir string) error {
		*clock = start.Add(6 * time.Hour)
		return inner(dir)
	}
	f, claude, c := nightFixture(t, nil, []fake.Step{
		first,
		spends(&clock, "two.go", "second", nil, nil),
	}, start)
	clock = c
	f.opts.Lending = &Lending{Leases: []Lease{{
		Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"},
		Kind:  LeaseOpen, Keep: 0,
	}}}

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 2 || f.h.Started() != 0 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

// A fresh window has to end before an until lease does.
func TestAnUntilLeaseRefusesAFreshWindowThatWouldRunPastIt(t *testing.T) {
	start := at(15, 22, 0)
	f, claude, _ := nightFixture(t, []fake.Step{
		{Do: commits("one.go", "first"), Outcome: done()},
		{Do: commits("two.go", "second"), Outcome: done()},
	}, nil, start)
	f.opts.Lending = &Lending{Leases: []Lease{{
		Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"},
		Kind:  LeaseUntil, Until: start.Add(4 * time.Hour), Keep: 0,
	}}}

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 0 || claude.Started() != 0 || f.h.Started() != 2 {
		t.Fatalf("slept %v, claude ran %d sessions, the primary %d", f.slept, claude.Started(), f.h.Started())
	}
}

// A window the author opened and that resets before an until lease ends is
// used up without opening a fresh one.
func TestAnUntilLeaseUsesTheAuthorsWindowThatResetsBeforeItEnds(t *testing.T) {
	start := at(15, 22, 0)
	f, claude, _ := nightFixture(t, nil, []fake.Step{
		{Do: commits("one.go", "first"), Outcome: done()},
	}, start)
	f.opts.Until = "1.1"
	f.opts.Lending = &Lending{Leases: []Lease{{
		Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"},
		Kind:  LeaseUntil, Until: start.Add(11 * time.Hour), Keep: 0,
	}}}
	claude.Meter = windows(start, 0.5, start.Add(time.Hour), 0.1)

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 1 || f.h.Started() != 0 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

// A window the author opened that resets after an until lease ends is theirs.
func TestAnUntilLeaseRefusesTheAuthorsWindowThatResetsAfterItEnds(t *testing.T) {
	start := at(15, 22, 0)
	f, claude, _ := nightFixture(t, []fake.Step{
		{Do: commits("one.go", "first"), Outcome: done()},
		{Do: commits("two.go", "second"), Outcome: done()},
	}, nil, start)
	f.opts.Lending = &Lending{Leases: []Lease{{
		Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"},
		Kind:  LeaseUntil, Until: start.Add(2 * time.Hour), Keep: 0,
	}}}
	claude.Meter = windows(start, 0.1, start.Add(8*time.Hour), 0.1)

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 0 || claude.Started() != 0 || f.h.Started() != 2 {
		t.Fatalf("slept %v, claude ran %d sessions, the primary %d", f.slept, claude.Started(), f.h.Started())
	}
}

// A passed until lease lends nothing at all.
func TestAPassedUntilLeaseLendsNothing(t *testing.T) {
	start := at(15, 22, 0)
	f, claude, _ := nightFixture(t, []fake.Step{
		{Do: commits("one.go", "first"), Outcome: done()},
		{Do: commits("two.go", "second"), Outcome: done()},
	}, nil, start)
	f.opts.Lending = &Lending{Leases: []Lease{{
		Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"},
		Kind:  LeaseUntil, Until: start.Add(-time.Minute), Keep: 0,
	}}}

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 0 || claude.Started() != 0 || f.h.Started() != 2 {
		t.Fatalf("slept %v, claude ran %d sessions, the primary %d", f.slept, claude.Started(), f.h.Started())
	}
}

// With every lease out of room the bedrock takes the unit at once.
func TestWithEveryLeaseOutOfRoomTheBedrockRunsWithoutWaiting(t *testing.T) {
	start := at(15, 14, 0)
	f, claude, _ := nightFixture(t, []fake.Step{
		{Do: commits("one.go", "first"), Outcome: done()},
		{Do: commits("two.go", "second"), Outcome: done()},
	}, nil, start)
	codex := fake.New()
	codex.Named = "codex"
	codex.Meter = windows(start, 0.1, start.Add(time.Hour), 0.9)
	nightly, _ := ParseAway("21:00-09:00")
	f.opts.Lending = &Lending{Leases: []Lease{
		{Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"}, Kind: LeaseNightly, Span: nightly, Keep: 0.2},
		{Agent: Agent{Harness: codex, Model: "gpt-6.1-sol", Effort: "high"}, Kind: LeaseOpen, Keep: 0.2},
	}}

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if len(f.slept) != 0 || claude.Started() != 0 || codex.Started() != 0 || f.h.Started() != 2 {
		t.Fatalf("slept %v, claude ran %d sessions, codex %d, the primary %d",
			f.slept, claude.Started(), codex.Started(), f.h.Started())
	}
}

// The lease that would lose the most of its week takes the first unit; once
// its window is spent, the other takes the next.
func TestTheMostUrgentLeaseRunsUntilItsWindowIsSpent(t *testing.T) {
	start := at(15, 23, 0)
	f := setup(t, twoUnits)
	f.h.Named = "opencode"
	claude := fake.New(fake.Step{Do: commits("two.go", "second"), Outcome: done()})
	claude.Named = "claude"
	claude.Meter = weeklyOnly(start, 0.40, 96*time.Hour)
	codex := fake.New(fake.Step{
		Do: commits("one.go", "first"),
		Outcome: harness.Outcome{
			Marker: harness.Done,
			Budget: windows(start, 1, start.Add(harness.SessionWindow), 0.11),
		},
	})
	codex.Named = "codex"
	codex.Meter = weeklyOnly(start, 0.10, 24*time.Hour)
	f.opts.Lending = &Lending{Leases: []Lease{
		{Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"}, Kind: LeaseOpen, Keep: 0.2},
		{Agent: Agent{Harness: codex, Model: "gpt-6.1-sol", Effort: "high"}, Kind: LeaseOpen, Keep: 0},
	}}
	f.opts.Now = func() time.Time { return start }

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if codex.Started() != 1 || claude.Started() != 1 || f.h.Started() != 0 {
		t.Fatalf("codex ran %d sessions, claude %d, the primary %d",
			codex.Started(), claude.Started(), f.h.Started())
	}
	for id, want := range map[string]string{"1.1": "codex", "1.2": "claude"} {
		if entry := f.state().Entry(id); entry.Harness != want {
			t.Fatalf("unit %s ran on %q, want %q", id, entry.Harness, want)
		}
	}
}
