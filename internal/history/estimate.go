package history

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Torwalt/ploopy/internal/harness"
)

// MinUnits is how many landed units an estimate needs before it says anything.
const MinUnits = 3

// Estimate is what landing one unit took an agent, every attempt included,
// from the units it landed before.
type Estimate struct {
	Agent   string
	Harness string
	Units   int  // landed units it is drawn from
	Clean   bool // drawn only from units run while the author was away

	// Shares of the usage windows per unit, from the units whose windows
	// were seen before and after every session.
	Windowed    int
	Session     float64 // median share of a five-hour window
	SessionHigh float64 // its 75th percentile
	Weekly      float64 // median share of the weekly window

	CostUSD float64 // median
	Minutes float64 // median
}

// Known reports whether there are enough units to go by.
func (e Estimate) Known() bool { return e.Units >= MinUnits }

// Metered reports whether the estimate knows what a unit takes of the usage
// windows.
func (e Estimate) Metered() bool { return e.Windowed >= MinUnits && e.Session > 0 }

// WeeklyPerWindow is the share of the week one spent five-hour window takes.
func (e Estimate) WeeklyPerWindow() float64 {
	if !e.Metered() {
		return 0
	}
	return e.Weekly / e.Session
}

// UnitsPerWindow is how many units one five-hour window lands.
func (e Estimate) UnitsPerWindow() float64 {
	if !e.Metered() {
		return 0
	}
	return 1 / e.Session
}

// Describe renders what a unit takes, for a person.
func (e Estimate) Describe() string {
	if !e.Known() {
		return fmt.Sprintf("no estimate yet: %d landed unit(s) on record, %d needed", e.Units, MinUnits)
	}
	var parts []string
	if e.Metered() {
		parts = append(parts, fmt.Sprintf("%.0f%% of a 5h window (%.0f%% at worst), %.1f%% of the week",
			e.Session*100, e.SessionHigh*100, e.Weekly*100))
	}
	if e.CostUSD > 0 {
		cost := fmt.Sprintf("$%.2f", e.CostUSD)
		if e.Harness == "claude" {
			cost = "≈" + cost + " list"
		}
		parts = append(parts, cost)
	}
	parts = append(parts, fmt.Sprintf("%.0f min", e.Minutes))
	from := fmt.Sprintf("%d units", e.Units)
	if e.Clean {
		from += " run while you were away"
	}
	return "per unit " + strings.Join(parts, " · ") + " — from " + from
}

// Plan renders what a number of open units would take.
func (e Estimate) Plan(units int) string {
	if !e.Known() || units == 0 {
		return ""
	}
	n := float64(units)
	if e.Metered() {
		return fmt.Sprintf("%d open units ≈ %.1f five-hour windows, %.0f%% of the week",
			units, n*e.Session, n*e.Weekly*100)
	}
	if e.CostUSD > 0 {
		return fmt.Sprintf("%d open units ≈ $%.2f", units, n*e.CostUSD)
	}
	return fmt.Sprintf("%d open units ≈ %.0f min", units, n*e.Minutes)
}

// sample is one unit's sessions, until it landed.
type sample struct {
	agent, harness string
	mixed          bool
	away           bool
	seconds, cost  float64
	session        float64
	weekly         float64
	windowed       bool
}

// Estimates is every agent's estimate, the one with most units first. A unit
// counts once it landed, and only when one agent ran all its sessions.
func Estimates(records []Record) []Estimate {
	// An import appends older sessions after newer ones.
	ran := append([]Record(nil), records...)
	sort.SliceStable(ran, func(i, j int) bool { return ran[i].Started.Before(ran[j].Started) })
	open := map[string]*sample{}
	byAgent := map[string][]*sample{}
	for _, r := range ran {
		if r.Role == "rescue" {
			continue
		}
		key := r.Plan + "\x00" + r.Branch + "\x00" + r.Unit
		s := open[key]
		if s == nil {
			s = &sample{agent: r.Agent, harness: r.Harness, away: true, windowed: true}
			open[key] = s
		}
		s.mixed = s.mixed || s.agent != r.Agent
		s.away = s.away && r.Away
		s.seconds += r.Seconds
		s.cost += r.CostUSD
		session, sessionSeen := share(r.Before, r.After, func(b *harness.Budget) harness.Allowance { return b.Session })
		weekly, weeklySeen := share(r.Before, r.After, func(b *harness.Budget) harness.Allowance { return b.Weekly })
		s.session += session
		s.weekly += weekly
		s.windowed = s.windowed && sessionSeen && weeklySeen

		switch r.Verdict {
		case "landed":
			delete(open, key)
			if !s.mixed {
				byAgent[s.agent] = append(byAgent[s.agent], s)
			}
		case "blocked":
			delete(open, key)
		}
	}

	var out []Estimate
	for agent, samples := range byAgent {
		out = append(out, estimate(agent, samples))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Units != out[j].Units {
			return out[i].Units > out[j].Units
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

// For is one agent's estimate.
func For(records []Record, agent string) Estimate {
	for _, e := range Estimates(records) {
		if e.Agent == agent {
			return e
		}
	}
	return Estimate{Agent: agent}
}

// estimate draws on the units run while the author was away when there are
// enough of them: the author's own use moves the windows during the day.
func estimate(agent string, samples []*sample) Estimate {
	var away []*sample
	for _, s := range samples {
		if s.away {
			away = append(away, s)
		}
	}
	e := Estimate{Agent: agent, Harness: samples[0].harness}
	if len(away) >= MinUnits {
		samples, e.Clean = away, true
	}
	e.Units = len(samples)

	var minutes, costs, sessions, weeklies []float64
	for _, s := range samples {
		minutes = append(minutes, s.seconds/60)
		costs = append(costs, s.cost)
		if s.windowed {
			sessions = append(sessions, s.session)
			weeklies = append(weeklies, s.weekly)
		}
	}
	e.Minutes, e.CostUSD = percentile(minutes, 0.5), percentile(costs, 0.5)
	e.Windowed = len(sessions)
	e.Session, e.SessionHigh = percentile(sessions, 0.5), percentile(sessions, 0.75)
	e.Weekly = percentile(weeklies, 0.5)
	return e
}

// share is how much of one window a session took. It is unknown when either
// end went unseen or the window reset in between.
func share(before, after *harness.Budget, window func(*harness.Budget) harness.Allowance) (float64, bool) {
	if before == nil || after == nil {
		return 0, false
	}
	from, to := window(before), window(after)
	if from.ResetAt.IsZero() || !from.ResetAt.Equal(to.ResetAt) {
		return 0, false
	}
	return math.Max(to.Used-from.Used, 0), true
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := p * float64(len(sorted)-1)
	low := int(math.Floor(rank))
	high := int(math.Ceil(rank))
	return sorted[low] + (sorted[high]-sorted[low])*(rank-float64(low))
}
