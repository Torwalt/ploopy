package loop

import (
	"context"
	"fmt"
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

// Night is how the run uses the author's subscriptions while the author is
// away: its agents take sessions in turn while their usage windows have room,
// and the run's own agent takes the rest.
type Night struct {
	Away    Away
	Agents  []NightAgent // tried in order
	Reserve float64      // share of every weekly window left to the author
}

// NightAgent is a subscription's agent and how many fresh five-hour windows
// the run may open in one away span. A window the author opened is used up
// for free when it resets before the span ends.
type NightAgent struct {
	Agent
	Windows int
}

// Room says whether an agent may take a session at an instant of an away span
// ending at end, given what its windows last showed and the windows it has
// opened in this span. why says what decided it, for a person.
func (n Night) Room(agent NightAgent, b *harness.Budget, opened int, limited, now, end time.Time) (ok, fresh bool, why string) {
	name := agent.Harness.Name()
	if limited.After(now) {
		return false, false, fmt.Sprintf("%s is at its usage limit until %s", name, clock(limited, now))
	}
	if n.Reserve >= 1 {
		return false, false, fmt.Sprintf("%s's whole week stays yours", name)
	}
	if b != nil && b.Weekly.UsedAt(now) >= 1-n.Reserve {
		return false, false, fmt.Sprintf("%s has spent %.0f%% of its week; %.0f%% stays yours",
			name, b.Weekly.Used*100, n.Reserve*100)
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
		case b.Session.ResetAt.After(end):
			return false, false, fmt.Sprintf("%s's window runs until %s, past %s", name, reset, clock(end, now))
		case b.Session.Used == 0:
			// A window opened by a session that reported none.
			return true, false, fmt.Sprintf("%s's window resets at %s, before you are back", name, reset)
		}
		return true, false, fmt.Sprintf("%s's window is %.0f%% spent and resets at %s, before you are back",
			name, b.Session.Used*100, reset)
	}
	if now.Add(length).After(end) {
		return false, false, fmt.Sprintf("a fresh %s window would run past %s", name, clock(end, now))
	}
	if opened >= agent.Windows {
		return false, false, fmt.Sprintf("%s has opened the %d fresh window(s) it may", name, agent.Windows)
	}
	return true, true, fmt.Sprintf("%s opens fresh window %d of %d, until %s",
		name, opened+1, agent.Windows, clock(now.Add(length), now))
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

	span   time.Time          // the away span the opened windows count against
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

// enter counts against the away span starting at start. A window still running
// into it stays counted, so a span moved mid-run opens no window twice.
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
// run's when it began in the span and was not running before.
func (m *meter) saw(b *harness.Budget) {
	if b == nil {
		return
	}
	before := m.budget
	m.budget = b
	seen := b.Session
	if m.span.IsZero() || seen.ResetAt.IsZero() {
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
	if mine || (!known && !seen.Started().Before(m.span.Add(-windowSlack))) {
		m.opened[seen.ResetAt] = true
	}
}

// night is how the author wants the subscriptions used now, or nil.
func (l *Loop) night() *Night {
	if l.opts.Steering == nil {
		return l.opts.Night
	}
	return l.steer().Night
}

// nightPick is the night agent with room for the next session, or nil. why
// says what decided it.
func (l *Loop) nightPick() (agent *NightAgent, fresh bool, why string) {
	n := l.night()
	if n == nil || len(n.Agents) == 0 {
		return nil, false, ""
	}
	now := l.opts.Now()
	start, end, inside := n.Away.Span(now)
	if !inside {
		return nil, false, "you are away from " + clock(start, now)
	}
	var refused []string
	for i := range n.Agents {
		a := n.Agents[i]
		m := l.meterOf(a.Harness.Name())
		m.enter(start)
		if m.resting.After(now) {
			refused = append(refused, fmt.Sprintf("%s %s; it sits out until %s", a.Harness.Name(), m.rested, clock(m.resting, now)))
			continue
		}
		b, until := m.windows(now), end
		// A window the run opened is its own to use up, wherever its reset fell.
		if b != nil && m.opened[b.Session.ResetAt] && b.Session.ResetAt.After(end) {
			until = b.Session.ResetAt
		}
		ok, fresh, why := n.Room(a, b, len(m.opened), m.limited, now, until)
		if ok {
			return &a, fresh, why
		}
		refused = append(refused, why)
	}
	return nil, false, "no subscription has room: " + strings.Join(refused, "; ")
}

// readMeters asks every night agent's harness that can tell for its windows,
// at most every quarter hour each, and falls back to the repository's history.
// Outside the away span nothing asks.
func (l *Loop) readMeters(ctx context.Context) {
	n := l.night()
	if n == nil {
		return
	}
	now := l.opts.Now()
	start, _, inside := n.Away.Span(now)
	if !inside {
		return
	}
	var records []history.Record
	if l.opts.History != "" {
		records, _ = history.Load(l.opts.History)
	}
	for _, a := range n.Agents {
		m := l.meterOf(a.Harness.Name())
		m.enter(start)
		if m.budget == nil {
			m.budget = history.Latest(records, a.Harness.Name())
		}
		metered, ok := a.Harness.(harness.Metered)
		if !ok || (m.budget != nil && now.Sub(m.budget.At) < 15*time.Minute) {
			continue
		}
		if budget, err := metered.Budget(ctx); err == nil {
			m.saw(budget)
		}
	}
}

// opening books the fresh window a night session that started at an instant
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

// benched takes a night agent that could not run out of turn, as a limit
// would: logged out or missing, it would fail every unit it is handed.
func (l *Loop) benched(agent Agent, why string) {
	m := l.meterOf(agent.Harness.Name())
	m.resting, m.rested = l.opts.Now().Add(l.opts.LimitWait), why
	l.record("night", agent.Label()+" "+why+"; the next session goes to who has room")
}

// nightWake is the next instant a night agent may get room, or zero: the
// span beginning, a usage limit lifting, a spent window resetting. A unit's
// own agent never hands over, so nothing wakes it.
func (l *Loop) nightWake() time.Time {
	n := l.night()
	if n == nil || len(n.Agents) == 0 || l.own != nil {
		return time.Time{}
	}
	now := l.opts.Now()
	start, end, inside := n.Away.Span(now)
	if !inside {
		return start
	}
	var wake time.Time
	consider := func(at time.Time) {
		if at.After(now) && at.Before(end) && (wake.IsZero() || at.Before(wake)) {
			wake = at
		}
	}
	for _, a := range n.Agents {
		m := l.meterOf(a.Harness.Name())
		consider(m.limited)
		consider(m.resting)
		if m.budget != nil && m.budget.Session.Used >= 1 {
			consider(m.budget.Session.ResetAt.Add(2 * time.Minute))
		}
	}
	return wake
}
