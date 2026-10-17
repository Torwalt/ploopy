package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/history"
	"github.com/Torwalt/ploopy/internal/loop"
)

// forecastPart is one subscription's share of a run's forecast: its lease
// when it is lent now, the windows last seen, and what a unit took there.
type forecastPart struct {
	Name     string
	Lease    *loop.Lease
	Budget   *harness.Budget
	Estimate history.Estimate
}

// forecastLine is how many of the open units one agent is expected to land.
type forecastLine struct {
	Name    string
	Units   int
	Unknown bool // no lease, or no estimate of what a unit takes
}

// forecastResult is the board's footer forecast: the subscriptions in the
// order sessions would go, the bedrock taking what is left, and the cost.
type forecastResult struct {
	Subs      []forecastLine
	Bedrock   forecastLine
	CostUSD   float64
	CostKnown bool
	Done      time.Time
}

// forecastRun distributes the open units over the lent subscriptions and the
// bedrock. A subscription without a lease or a metered estimate takes none.
func forecastRun(parts []forecastPart, bedrock string, bedrockEst history.Estimate, open int, now time.Time, spendAll bool) forecastResult {
	var result forecastResult
	budgets := map[string]*harness.Budget{}
	lent := map[string]forecastPart{}
	var leases []loop.Lease
	for _, p := range parts {
		if p.Lease == nil {
			continue
		}
		if _, _, inside := loop.LeaseSpan(*p.Lease, now); !inside && p.Lease.Kind != loop.LeaseNightly {
			continue
		}
		name := p.Lease.Harness.Name()
		budgets[name] = p.Budget
		lent[name] = p
		leases = append(leases, *p.Lease)
	}
	loop.LendOrder(leases, budgets, now, spendAll)
	ordered := map[string]bool{}
	var minutes float64
	knownMinutes, total := true, 0
	for i := range leases {
		name := leases[i].Harness.Name()
		ordered[name] = true
		p := lent[name]
		line := forecastSubscription(p, open, now, spendAll)
		result.Subs = append(result.Subs, line)
		if !line.Unknown {
			open -= line.Units
		}
		if line.Units > 0 {
			total += line.Units
			if p.Estimate.Minutes <= 0 {
				knownMinutes = false
			} else {
				minutes += float64(line.Units) * p.Estimate.Minutes
			}
		}
	}
	for _, p := range parts {
		if ordered[p.Name] {
			continue
		}
		result.Subs = append(result.Subs, forecastLine{Name: p.Name, Unknown: true})
	}

	result.Bedrock = forecastLine{Name: bedrock, Units: open}
	if bedrockEst.CostUSD > 0 {
		result.CostKnown = true
		result.CostUSD = float64(open) * bedrockEst.CostUSD
	}
	if open > 0 {
		total += open
		if bedrockEst.Minutes <= 0 {
			knownMinutes = false
		} else {
			minutes += float64(open) * bedrockEst.Minutes
		}
	}
	if total > 0 && knownMinutes {
		result.Done = now.Add(time.Duration(minutes * float64(time.Minute)))
	}
	return result
}

// forecastSubscription is one lent subscription's share: the rest of a window
// that resets inside the lease, plus the fresh windows that fit, capped by
// what the week leaves. units is bounded by the open units.
func forecastSubscription(p forecastPart, open int, now time.Time, spendAll bool) forecastLine {
	line := forecastLine{Name: p.Name}
	if p.Lease == nil || !p.Estimate.Metered() {
		line.Unknown = true
		return line
	}
	start, end, inside := loop.LeaseSpan(*p.Lease, now)
	if !inside && p.Lease.Kind != loop.LeaseNightly {
		line.Unknown = true
		return line
	}
	from := now
	if !inside && start.After(from) {
		from = start
	}
	b := p.Budget
	rest := 0.0
	if b != nil && b.Session.Active(from) && (end.IsZero() || b.Session.ResetAt.Before(end)) {
		rest = math.Max(1-b.Session.Used, 0)
	}
	fresh := math.Inf(1)
	if !end.IsZero() {
		next := from
		if b != nil && b.Session.Active(from) && b.Session.ResetAt.After(next) {
			next = b.Session.ResetAt
		}
		if !next.Before(end) {
			fresh = 0
		} else {
			fresh = math.Floor(end.Sub(next).Hours() / harness.SessionWindow.Hours())
		}
	}
	weeklyUsed := 0.0
	if b != nil {
		weeklyUsed = b.Weekly.UsedAt(now)
	}
	room := 1 - loop.EffectiveKeep(*p.Lease, b, now, spendAll) - weeklyUsed
	byWeek := math.Inf(1)
	if per := p.Estimate.WeeklyPerWindow(); per > 0 {
		byWeek = room / per
	}
	capacity := math.Min(rest+fresh, byWeek)
	if capacity < 0 || math.IsNaN(capacity) {
		capacity = 0
	}
	units := capacity * p.Estimate.UnitsPerWindow()
	switch {
	case math.IsInf(units, 1) || units >= float64(open):
		line.Units = open
	case units <= 0:
		line.Units = 0
	default:
		line.Units = int(math.Round(units))
	}
	if line.Units < 0 {
		line.Units = 0
	}
	return line
}

// String renders the footer as one line.
func (r forecastResult) String() string {
	pieces := make([]string, 0, len(r.Subs)+1)
	for _, line := range r.Subs {
		if line.Unknown {
			pieces = append(pieces, line.Name+" ?")
			continue
		}
		pieces = append(pieces, line.Name+" "+strconv.Itoa(line.Units))
	}
	pieces = append(pieces, r.Bedrock.Name+" "+strconv.Itoa(r.Bedrock.Units)+" units")
	out := "≈ " + strings.Join(pieces, " · ")
	if r.CostKnown {
		out += fmt.Sprintf(" · ≈ $%.2f", r.CostUSD)
	}
	if !r.Done.IsZero() {
		out += " · done ≈ " + r.Done.Format("15:04")
	}
	return out
}
