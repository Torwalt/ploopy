package loop

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/history"
)

// Away is when the author leaves the subscriptions to the run, every day, in
// local time. It may wrap midnight.
type Away struct {
	From, Until int // minutes after local midnight
}

// ParseAway reads a span such as 21:00-09:00.
func ParseAway(text string) (Away, error) {
	from, until, ok := strings.Cut(strings.ReplaceAll(text, "–", "-"), "-")
	if !ok {
		return Away{}, fmt.Errorf("away must look like 21:00-09:00, not %q", text)
	}
	a := Away{}
	var err error
	if a.From, err = minutes(from); err != nil {
		return Away{}, err
	}
	if a.Until, err = minutes(until); err != nil {
		return Away{}, err
	}
	if a.From == a.Until {
		return Away{}, fmt.Errorf("away %q is empty", text)
	}
	return a, nil
}

func minutes(clock string) (int, error) {
	hours, mins, ok := strings.Cut(strings.TrimSpace(clock), ":")
	h, errH := strconv.Atoi(hours)
	m, errM := strconv.Atoi(mins)
	if !ok || errH != nil || errM != nil || h < 0 || h > 24 || m < 0 || m > 59 || h*60+m > 24*60 {
		return 0, fmt.Errorf("%q is not a time of day like 21:00", clock)
	}
	return (h*60 + m) % (24 * 60), nil
}

// String renders the span the way ParseAway reads it.
func (a Away) String() string {
	return fmt.Sprintf("%02d:%02d-%02d:%02d", a.From/60, a.From%60, a.Until/60, a.Until%60)
}

// Span is the away span going on at an instant, else the next one, and
// whether it is going on. Its ends are clock times, so a night that changes
// to or from daylight saving is an hour shorter or longer.
func (a Away) Span(at time.Time) (start, end time.Time, inside bool) {
	wraps := 0
	if a.Until <= a.From {
		wraps = 1
	}
	for day := -1; day <= 1; day++ {
		start = clockOn(at, day, a.From)
		end = clockOn(at, day+wraps, a.Until)
		if at.Before(end) {
			return start, end, !at.Before(start)
		}
	}
	return start, end, false
}

// clockOn is a time of day, in minutes, on the day some days from an instant's.
func clockOn(at time.Time, days, minutes int) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day()+days, minutes/60, minutes%60, 0, 0, at.Location())
}

// LeaseKind is the term a subscription is lent on.
type LeaseKind string

const (
	LeaseUntil   LeaseKind = "until"
	LeaseNightly LeaseKind = "nightly"
	LeaseOpen    LeaseKind = "open"
)

// Lease is one subscription lent on some terms.
type Lease struct {
	Agent
	Kind  LeaseKind
	Until time.Time // kind until
	Span  Away      // kind nightly
	Keep  float64   // share of the week kept for the author, 0..1
}

// Lending is how the run may use the author's subscriptions now.
type Lending struct {
	Leases   []Lease // in lease-file order
	SpendAll bool    // the run keeps no reserve
}

// LeaseSpan is when a lease is lent at an instant, when that ends, and whether
// it is lent at all. start is the nightly span's beginning, and zero for a
// lease with no daily beginning.
func LeaseSpan(lease Lease, now time.Time) (start, end time.Time, inside bool) {
	return leaseSpan(lease, now)
}

func leaseSpan(lease Lease, now time.Time) (start, end time.Time, inside bool) {
	switch lease.Kind {
	case LeaseUntil:
		return time.Time{}, lease.Until, now.Before(lease.Until)
	case LeaseNightly:
		return lease.Span.Span(now)
	default:
		return time.Time{}, time.Time{}, true
	}
}

// EffectiveKeep is the share of a subscription's week kept for the author.
// The reserve shrinks as the weekly reset nears, so leftovers are used first.
// A lease keeps its own share; an explicit spend-all keeps none.
func EffectiveKeep(lease Lease, b *harness.Budget, now time.Time, spendAll bool) float64 {
	if spendAll {
		return 0
	}
	if lease.Keep >= 1 {
		return 1
	}
	if b == nil || b.Weekly.ResetAt.IsZero() || !b.Weekly.Active(now) {
		return lease.Keep
	}
	left := float64(b.Weekly.ResetAt.Sub(now)) / float64(harness.WeeklyWindow)
	return lease.Keep * clamp(left, 0, 1)
}

func clamp(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// Room says whether a lease may take a session at an instant, given what its
// windows last showed and whether the run opened the window going on. why says
// what decided it, for a person.
func (l Lending) Room(lease Lease, b *harness.Budget, ownWindow bool, limited, now time.Time) (ok, fresh bool, why string) {
	name := lease.Harness.Name()
	start, end, inside := leaseSpan(lease, now)
	if limited.After(now) {
		return false, false, fmt.Sprintf("%s is at its usage limit until %s", name, clock(limited, now))
	}
	if !inside {
		if lease.Kind == LeaseUntil {
			return false, false, fmt.Sprintf("%s is not lent", name)
		}
		return false, false, fmt.Sprintf("%s is lent from %s to %s",
			name, clock(start, now), clock(end, now))
	}
	keep := EffectiveKeep(lease, b, now, l.SpendAll)
	if keep >= 1 {
		return false, false, fmt.Sprintf("%s's whole week stays yours", name)
	}
	if b != nil && b.Weekly.UsedAt(now) >= 1-keep {
		return false, false, fmt.Sprintf("%s has spent %.0f%% of its week; %.0f%% stays yours",
			name, b.Weekly.Used*100, keep*100)
	}
	length := harness.SessionWindow
	if b != nil && b.Session.Length > 0 {
		length = b.Session.Length
	}
	if b != nil && b.Session.Active(now) {
		reset := clock(b.Session.ResetAt, now)
		switch {
		case b.Session.Used >= 1:
			return false, false, fmt.Sprintf("%s's window is spent until %s", name, reset)
		case !end.IsZero() && b.Session.ResetAt.After(end) && !ownWindow:
			return false, false, fmt.Sprintf("%s's window runs until %s, past %s",
				name, reset, clock(end, now))
		case b.Session.Used == 0:
			return true, false, fmt.Sprintf("%s's window resets at %s", name, reset)
		}
		return true, false, fmt.Sprintf("%s's window is %.0f%% spent and resets at %s",
			name, b.Session.Used*100, reset)
	}
	if !end.IsZero() && now.Add(length).After(end) {
		return false, false, fmt.Sprintf("a fresh %s window would run past %s", name, clock(end, now))
	}
	return true, true, fmt.Sprintf("%s opens a fresh window until %s",
		name, clock(now.Add(length), now))
}

// clock renders an instant in local time, with the day when it is not today.
func clock(at, now time.Time) string {
	local, today := at.In(now.Location()), now
	if local.YearDay() == today.YearDay() && local.Year() == today.Year() {
		return local.Format("15:04")
	}
	return local.Format("Mon 15:04")
}

// meter is what the run knows of one subscription's usage windows.
type meter struct {
	budget  *harness.Budget
	limited time.Time // a usage limit holds until then
	resting time.Time // a session that could not run benches the agent until then
	rested  string    // why

	span   time.Time          // the nightly span the opened windows count against
	opened map[time.Time]bool // windows the run opened, by when they reset; booked ones until seen
}

// windowSlack is how far a reported window may sit from where the run expects
// it: resets look rounded, and a session's first request comes after it starts.
const windowSlack = 10 * time.Minute

func (l *Loop) meterOf(harnessName string) *meter {
	if l.meters == nil {
		l.meters = map[string]*meter{}
	}
	m := l.meters[harnessName]
	if m == nil {
		m = &meter{opened: map[time.Time]bool{}}
		l.meters[harnessName] = m
	}
	return m
}

// enter counts against the span starting at start. A nightly lease's windows
// are cleared at the next night; an until or open lease keys on the zero time,
// so a run opens nothing twice.
func (m *meter) enter(start time.Time) {
	if m.span.Equal(start) {
		return
	}
	m.span = start
	for reset := range m.opened {
		if !reset.After(start) {
			delete(m.opened, reset)
		}
	}
}

// windows is what the agent's windows look like at an instant: the last it
// reported, or else the one a session opened without reporting it.
func (m *meter) windows(now time.Time) *harness.Budget {
	if m.budget != nil && m.budget.Session.Active(now) {
		return m.budget
	}
	var running time.Time
	for reset := range m.opened {
		if reset.After(now) && reset.After(running) {
			running = reset
		}
	}
	if running.IsZero() {
		return m.budget
	}
	b := harness.Budget{At: now, Session: harness.Allowance{ResetAt: running, Length: harness.SessionWindow}}
	if m.budget != nil {
		b.Weekly = m.budget.Weekly
	}
	return &b
}

// saw takes in windows the harness reported. One subscription runs one window
// at a time, so a reported window replaces every booked one it overlaps, and
// is the run's when it began no earlier than one of them. Any other is the
// run's when it began in the nightly span and was not running before.
func (m *meter) saw(b *harness.Budget) {
	if b == nil {
		return
	}
	before := m.budget
	m.budget = b
	seen := b.Session
	if seen.ResetAt.IsZero() {
		return
	}
	mine := false
	for reset := range m.opened {
		if seen.ResetAt.Sub(reset).Abs() < harness.SessionWindow-windowSlack {
			delete(m.opened, reset)
			mine = mine || !seen.ResetAt.Before(reset.Add(-windowSlack))
		}
	}
	known := before != nil && before.Session.ResetAt.Equal(seen.ResetAt)
	if mine || (!m.span.IsZero() && !known && !seen.Started().Before(m.span.Add(-windowSlack))) {
		m.opened[seen.ResetAt] = true
	}
}

// lending is how the author wants the subscriptions used now, or nil.
func (l *Loop) lending() *Lending {
	if l.opts.Steering == nil {
		return l.opts.Lending
	}
	return l.steer().Lending
}

// lendChoice is a lease with room, and what the pick knew about it.
type lendChoice struct {
	lease Lease
	b     *harness.Budget
	fresh bool
	why   string
}

// lendOrder puts the lease that should take the session first: a window
// already open expires, so it goes before a fresh one, the lease resetting
// first leading; a fresh window goes to the lease that would lose the most of
// its week by waiting. Ties keep lease-file order.
func lendOrder(choices []lendChoice, now time.Time, spendAll bool) {
	sort.SliceStable(choices, func(i, j int) bool {
		a, b := choices[i], choices[j]
		if a.fresh != b.fresh {
			return !a.fresh
		}
		if !a.fresh {
			return a.b.Session.ResetAt.Before(b.b.Session.ResetAt)
		}
		return lendUrgency(a.lease, a.b, now, spendAll) >
			lendUrgency(b.lease, b.b, now, spendAll)
	})
}

// LendOrder puts leases in the order sessions would go at an instant: one
// whose window is already open before one opening a fresh window, the one
// resetting first leading; fresh ones with the most week to lose first. The
// budgets are keyed by harness name, and a missing one counts as no window.
func LendOrder(leases []Lease, budgets map[string]*harness.Budget, now time.Time, spendAll bool) {
	choices := make([]lendChoice, 0, len(leases))
	for _, lease := range leases {
		b := budgets[lease.Harness.Name()]
		choices = append(choices, lendChoice{lease: lease, b: b, fresh: b == nil || !b.Session.Active(now)})
	}
	lendOrder(choices, now, spendAll)
	for i := range choices {
		leases[i] = choices[i].lease
	}
}

// lendUrgency is the share of the week a fresh window would lose by waiting:
// its room over the hours until the week resets. A week not seen, or one that
// already reset, is nothing to lose.
func lendUrgency(lease Lease, b *harness.Budget, now time.Time, spendAll bool) float64 {
	if b == nil || !b.Weekly.ResetAt.After(now) {
		return 0
	}
	hours := b.Weekly.ResetAt.Sub(now).Hours()
	return (1 - EffectiveKeep(lease, b, now, spendAll) - b.Weekly.UsedAt(now)) / hours
}

// lendPick is the lease with room for the next session, or nil. why says what
// decided it.
func (l *Loop) lendPick() (agent *Agent, fresh bool, why string) {
	lending := l.lending()
	if lending == nil || len(lending.Leases) == 0 {
		return nil, false, ""
	}
	now := l.opts.Now()
	var choices []lendChoice
	var refused []string
	for i := range lending.Leases {
		lease := lending.Leases[i]
		m := l.meterOf(lease.Harness.Name())
		start, _, _ := leaseSpan(lease, now)
		m.enter(start)
		if m.resting.After(now) {
			refused = append(refused, fmt.Sprintf("%s %s; it sits out until %s",
				lease.Harness.Name(), m.rested, clock(m.resting, now)))
			continue
		}
		b := m.windows(now)
		own := b != nil && m.opened[b.Session.ResetAt]
		ok, fresh, why := lending.Room(lease, b, own, m.limited, now)
		if !ok {
			refused = append(refused, why)
			continue
		}
		choices = append(choices, lendChoice{lease: lease, b: b, fresh: fresh, why: why})
	}
	if len(choices) == 0 {
		return nil, false, "no subscription has room: " + strings.Join(refused, "; ")
	}
	lendOrder(choices, now, lending.SpendAll)
	best := choices[0]
	chosen := best.lease.Agent
	return &chosen, best.fresh, best.why
}

// readMeters asks every lent harness that can tell for its windows, at most
// every quarter hour each, and falls back to the repository's history. Outside
// a nightly lease's span nothing asks.
func (l *Loop) readMeters(ctx context.Context) {
	lending := l.lending()
	if lending == nil {
		return
	}
	now := l.opts.Now()
	var records []history.Record
	if l.opts.History != "" {
		records, _ = history.Load(l.opts.History)
	}
	for _, lease := range lending.Leases {
		start, _, inside := leaseSpan(lease, now)
		if !inside {
			continue
		}
		m := l.meterOf(lease.Harness.Name())
		m.enter(start)
		if m.budget == nil {
			m.budget = history.Latest(records, lease.Harness.Name())
		}
		metered, ok := lease.Harness.(harness.Metered)
		if !ok || (m.budget != nil && now.Sub(m.budget.At) < 15*time.Minute) {
			continue
		}
		if budget, err := metered.Budget(ctx); err == nil {
			m.saw(budget)
		}
	}
}

// opening books the fresh window a lent session that started at an instant
// opened, so it counts and runs even when the session reports no windows.
func (l *Loop) opening(agent Agent, started time.Time) {
	m := l.meterOf(agent.Harness.Name())
	m.opened[started.Add(harness.SessionWindow)] = true
}

// metered takes in what a session showed of its subscription's windows.
func (l *Loop) metered(agent Agent, outcome harness.Outcome) {
	m := l.meterOf(agent.Harness.Name())
	m.saw(outcome.Budget)

	limit := outcome.RateLimit
	if limit == nil {
		return
	}
	now := l.opts.Now()
	switch {
	case limit.ResetAt.After(now):
		m.limited = limit.ResetAt
	case m.budget != nil && m.budget.Session.Active(now):
		m.limited = m.budget.Session.ResetAt.Add(2 * time.Minute)
	default:
		m.limited = now.Add(l.opts.LimitWait)
	}
}

// benched takes a lease that could not run out of turn, as a limit would:
// logged out or missing, it would fail every unit it is handed.
func (l *Loop) benched(agent Agent, why string) {
	m := l.meterOf(agent.Harness.Name())
	m.resting, m.rested = l.opts.Now().Add(l.opts.LimitWait), why
	l.record("night", agent.Label()+" "+why+"; the next session goes to who has room")
}

// lendWake is the next instant a lease may get room, or zero: a nightly span
// beginning, a usage limit lifting, a spent window resetting. A unit's own
// agent never hands over, so nothing wakes it.
func (l *Loop) lendWake() time.Time {
	lending := l.lending()
	if lending == nil || len(lending.Leases) == 0 || l.own != nil {
		return time.Time{}
	}
	now := l.opts.Now()
	var wake time.Time
	consider := func(at, end time.Time) {
		if !at.After(now) || (!end.IsZero() && !at.Before(end)) {
			return
		}
		if wake.IsZero() || at.Before(wake) {
			wake = at
		}
	}
	for _, lease := range lending.Leases {
		start, end, inside := leaseSpan(lease, now)
		if !inside {
			consider(start, end)
			continue
		}
		m := l.meterOf(lease.Harness.Name())
		consider(m.limited, end)
		consider(m.resting, end)
		if m.budget != nil && m.budget.Session.Used >= 1 {
			consider(m.budget.Session.ResetAt.Add(2*time.Minute), end)
		}
	}
	return wake
}
