package loop

import (
	"context"
	"errors"
	"fmt"
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

func TestANightAgentHasRoomOnlyWhileItsWindowsDo(t *testing.T) {
	agent := NightAgent{Agent: Agent{Harness: &fake.Harness{Named: "claude"}}, Windows: 2}
	night := Night{Reserve: 0.2}
	now, end := at(15, 23, 0), at(16, 9, 0)
	for _, c := range []struct {
		name    string
		budget  *harness.Budget
		opened  int
		limited time.Time
		now     time.Time
		ok      bool
		fresh   bool
	}{
		{"unknown windows open a fresh one", nil, 0, time.Time{}, now, true, true},
		{"the author's window resets before the end", windows(now, 0.5, at(16, 1, 0), 0.5), 0, time.Time{}, now, true, false},
		{"a spent window waits", windows(now, 1, at(16, 1, 0), 0.5), 0, time.Time{}, now, false, false},
		{"a window past the end is the author's", windows(now, 0.1, at(16, 10, 0), 0.5), 0, time.Time{}, at(16, 6, 0), false, false},
		{"a limit holds", nil, 0, at(16, 1, 0), now, false, false},
		{"the weekly reserve holds", windows(now, 0.5, at(16, 1, 0), 0.8), 0, time.Time{}, now, false, false},
		{"a fresh window must end in time", windows(now, 0.4, at(15, 22, 0), 0.5), 0, time.Time{}, at(16, 4, 30), false, false},
		{"the approved windows are spent", nil, 2, time.Time{}, now, false, false},
	} {
		ok, fresh, why := night.Room(agent, c.budget, c.opened, c.limited, c.now, end)
		if ok != c.ok || fresh != c.fresh || why == "" {
			t.Errorf("%s: ok %v fresh %v (%s)", c.name, ok, fresh, why)
		}
	}
	// Keeping the whole week holds even before any window was seen.
	whole := Night{Reserve: 1}
	if ok, _, why := whole.Room(agent, nil, 0, time.Time{}, now, end); ok {
		t.Errorf("a reserve of the whole week let a session in: %s", why)
	}
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

// nightFixture runs two units with a primary that is not metered and a night
// agent, on a clock that moves with every session.
func nightFixture(t *testing.T, primary, sub []fake.Step, now time.Time, windows int) (*fixture, *fake.Harness, *time.Time) {
	t.Helper()
	return nightFixtureFor(t, twoUnits, primary, sub, now, windows)
}

func nightFixtureFor(t *testing.T, planText string, primary, sub []fake.Step, now time.Time, windows int) (*fixture, *fake.Harness, *time.Time) {
	t.Helper()
	f := setup(t, planText, primary...)
	f.h.Named = "opencode"
	claude := fake.New(sub...)
	claude.Named = "claude"
	away, _ := ParseAway("21:00-09:00")
	f.opts.Night = &Night{Away: away, Reserve: 0.2,
		Agents: []NightAgent{{Agent: Agent{Harness: claude, Model: "sonnet", Effort: "high"}, Windows: windows}}}
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
	}, start, 1)
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

func TestANightAgentAtItsLimitHandsOverWithoutWaiting(t *testing.T) {
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
		[]fake.Step{limit}, start, 2)
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
}

func TestOutsideTheAwaySpanThePrimaryRuns(t *testing.T) {
	f, claude, _ := nightFixture(t, []fake.Step{
		{Do: commits("one.go", "first"), Outcome: done()},
		{Do: commits("two.go", "second"), Outcome: done()},
	}, nil, at(15, 14, 0), 2)

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

// The week is the author's too: past the reserve the subscription rests.
func TestTheWeeklyReserveKeepsTheSubscriptionOut(t *testing.T) {
	start := at(15, 22, 0)
	f, claude, _ := nightFixture(t, []fake.Step{
		{Do: commits("one.go", "first"), Outcome: done()},
		{Do: commits("two.go", "second"), Outcome: done()},
	}, nil, start, 2)
	claude.Meter = windows(start, 0.1, start.Add(time.Hour), 0.85)

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 0 || f.h.Started() != 2 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

// Once its approved fresh windows are opened, a subscription waits for the
// next night.
func TestOnlyTheApprovedFreshWindowsAreOpened(t *testing.T) {
	start := at(15, 22, 0)
	var clock *time.Time
	first := start.Add(5 * time.Hour)
	long := spends(&clock, "one.go", "first", windows(start, 0, first, 0.5), windows(start, 0.6, first, 0.55))
	inner := long.Do
	// The unit outlasts the window, so the next one would need a fresh window.
	long.Do = func(dir string) error {
		*clock = clock.Add(5 * time.Hour)
		return inner(dir)
	}
	f, claude, c := nightFixture(t,
		[]fake.Step{{Do: commits("two.go", "second"), Outcome: done()}},
		[]fake.Step{long}, start, 1)
	clock = c

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 1 || f.h.Started() != 1 {
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

// threeUnits is twoUnits with a third unit, for nights that span windows.
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
	}, start, 1)
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
	}, start, 2)
	clock = c

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 3 || f.h.Started() != 0 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

// A span moved mid-run keeps counting the windows already opened in it.
func TestMovingTheSpanDoesNotOpenTheApprovedWindowsAgain(t *testing.T) {
	start := at(15, 21, 0)
	var clock *time.Time
	reset := start.Add(5 * time.Hour)
	s := newSteering(Steer{})
	first := spends(&clock, "one.go", "first", windows(start, 0, reset, 0.5), windows(start, 0.95, reset, 0.55))
	inner := first.Do
	first.Do = func(dir string) error {
		later, _ := ParseAway("21:30-09:00")
		s.set(func(now *Steer) {
			moved := *now.Night
			moved.Away = later
			now.Night = &moved
		})
		*clock = reset.Add(time.Minute)
		return inner(dir)
	}
	f, claude, c := nightFixture(t, []fake.Step{{Do: commits("two.go", "second"), Outcome: done()}},
		[]fake.Step{first}, start, 1)
	clock = c
	s.now.Night = f.opts.Night
	f.opts.Steering = s

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 1 || f.h.Started() != 1 {
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
	}, start, 1)
	clock = c

	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if claude.Started() != 2 || f.h.Started() != 0 {
		t.Fatalf("claude ran %d sessions, the primary %d", claude.Started(), f.h.Started())
	}
}

// A subscription that cannot run, logged out or not installed, hands the
// session over like one at its limit instead of failing the night.
func TestANightAgentThatCannotRunHandsOver(t *testing.T) {
	for _, broken := range []fake.Step{
		{Outcome: harness.Outcome{Exit: 1}},
		{Err: errors.New(`exec: "claude": executable file not found in $PATH`)},
	} {
		f, claude, _ := nightFixture(t, []fake.Step{
			{Do: commits("one.go", "first"), Outcome: done()},
			{Do: commits("two.go", "second"), Outcome: done()},
		}, []fake.Step{broken}, at(15, 22, 0), 2)

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

// The session a crashed run left unjudged is booked to the agent that ran it,
// whoever would take the next one.
func TestAnUnjudgedNightSessionIsBookedToItsAgent(t *testing.T) {
	f, _, _ := nightFixture(t, []fake.Step{{Do: commits("one.go", "first"), Outcome: done()}}, nil, at(15, 14, 0), 2)
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

// A unit that names its own agent waits out that agent's limit: a night
// agent getting room does not help it.
func TestAUnitsOwnAgentIsNotWokenByTheNight(t *testing.T) {
	plan := strings.Replace(twoUnits, "**Goal:** the first thing.", "Agent: own\n\n**Goal:** the first thing.", 1)
	start := at(15, 22, 0)
	f, claude, _ := nightFixtureFor(t, plan, nil, []fake.Step{{Do: commits("two.go", "second"), Outcome: done()}}, start, 2)
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

// A unit due in the primary's peak hours waits for off-peak when the night
// agent's windows, read before the gate, leave it no room.
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
	}, nil, start, 2)
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
