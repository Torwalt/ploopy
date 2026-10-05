package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/state"
)

// total is what a group of units took.
type total struct {
	units           int
	elapsed, checks int
	attempts        int
	tokens          harness.Tokens
	cost            float64
	listPrice       bool // some of the cost is Claude's API-price equivalent
}

func (t *total) add(e *state.Entry) {
	t.units++
	t.elapsed += e.ElapsedS
	t.checks += e.VerifyS + e.TestS
	t.attempts += e.Attempts
	if e.Tokens != nil {
		t.tokens = t.tokens.Add(*e.Tokens)
	}
	t.cost += e.CostUSD
	t.listPrice = t.listPrice || (e.Harness == "claude" && e.CostUSD > 0)
}

// Summary renders what a plan took, unit by unit and stage by stage, from its
// state file. Given a run's stats, it closes with what that run took.
func (r *Reporter) Summary(p *plan.Plan, s *state.State, run *loop.Stats) {
	r.mu.Lock()
	defer r.mu.Unlock()

	done := 0
	for _, u := range p.Units {
		if status := s.Status(u.ID); status == state.Landed || status == state.Skipped {
			done++
		}
	}
	fmt.Fprintf(r.out, "\n%s %s %d/%d units\n", r.tag.Render("[ploopy]"), p.Name(), done, len(p.Units))
	fmt.Fprintln(r.out, r.dim.Render(row("unit", "title", "time", "checks", "tries", "tokens in/out", "cost")))

	stages := 0
	for i, u := range p.Units {
		if i == 0 || u.Stage != p.Units[i-1].Stage {
			stages++
		}
	}

	var whole, stage total
	for i, u := range p.Units {
		if stages > 1 && (i == 0 || u.Stage != p.Units[i-1].Stage) {
			heading := "Stage " + u.Stage
			if u.StageTitle != "" {
				heading += " — " + u.StageTitle
			}
			fmt.Fprintf(r.out, "  %s\n", r.unit.Render(heading))
			stage = total{}
		}

		entry := s.Entry(u.ID)
		if entry == nil || entry.Outcome == "marked" {
			status := s.Status(u.ID)
			fmt.Fprintln(r.out, r.dim.Render(row(u.ID, u.Title, status, "", "", "", "")))
		} else {
			whole.add(entry)
			stage.add(entry)
			var unit total
			unit.add(entry)
			fmt.Fprintln(r.out, unit.render(u.ID, u.Title))
			if entry.Status == state.Blocked {
				fmt.Fprintf(r.out, "        %s\n", r.bad.Render("blocked: "+entry.Reason))
			}
		}

		last := i == len(p.Units)-1 || p.Units[i+1].Stage != u.Stage
		if stages > 1 && last && stage.units > 0 {
			fmt.Fprintln(r.out, r.dim.Render(stage.render("", "stage")))
		}
	}
	fmt.Fprintln(r.out, r.tag.Render(whole.render("", "plan")))
	if prompt := whole.tokens.Prompt(); prompt > 0 && whole.tokens.CacheRead > 0 {
		fmt.Fprintln(r.out, r.dim.Render(fmt.Sprintf("  %d%% of the input was read from cache",
			whole.tokens.CacheRead*100/prompt)))
	}

	if run != nil {
		wall := run.Ended.Sub(run.Started)
		line := fmt.Sprintf("  this run: %s · %d session(s) in %s · checks %s · waiting %s",
			clock(wall), run.Sessions, clock(run.Session), clock(run.Checks), clock(run.Waited))
		if run.CostUSD > 0 {
			line += fmt.Sprintf(" · $%.2f", run.CostUSD)
		}
		fmt.Fprintln(r.out, line)
	}
	if whole.listPrice {
		fmt.Fprintln(r.out, r.dim.Render("  ≈ Claude's cost is the API-price equivalent, not what a subscription charges"))
	}
}

func (t total) render(id, title string) string {
	tries := ""
	if t.attempts > 0 {
		tries = fmt.Sprint(t.attempts)
	}
	tokens := ""
	if t.tokens != (harness.Tokens{}) {
		tokens = count(t.tokens.Prompt()) + " / " + count(t.tokens.Generated())
	}
	cost := ""
	if t.cost > 0 {
		cost = fmt.Sprintf("$%.2f", t.cost)
		if t.listPrice {
			cost = "≈" + cost
		}
	}
	return row(id, title, duration(t.elapsed), duration(t.checks), tries, tokens, cost)
}

func row(id, title, elapsed, checks, tries, tokens, cost string) string {
	return fmt.Sprintf("  %-5s %-34s %8s %7s %5s %15s %8s",
		id, truncate(title, 34), elapsed, checks, tries, tokens, cost)
}

// duration renders seconds the way a person reads a stopwatch.
func duration(seconds int) string {
	d := time.Duration(seconds) * time.Second
	switch {
	case seconds <= 0:
		return ""
	case d < time.Minute:
		return fmt.Sprintf("%ds", seconds)
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
	default:
		return fmt.Sprintf("%dh%02dm", seconds/3600, seconds%3600/60)
	}
}

// clock is a duration that shows zero as zero.
func clock(d time.Duration) string {
	if text := duration(int(d.Seconds())); text != "" {
		return text
	}
	return "0s"
}

// count renders a token count in thousands or millions.
func count(n int) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 1000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	default:
		return fmt.Sprint(n)
	}
}

// Totals is a plan's one-line account, for a listing of every plan.
func Totals(s *state.State) string {
	var whole total
	for _, entry := range s.Units {
		if entry.Outcome != "marked" {
			whole.add(entry)
		}
	}
	var parts []string
	if d := duration(whole.elapsed); d != "" {
		parts = append(parts, d)
	}
	if whole.cost > 0 {
		cost := fmt.Sprintf("$%.2f", whole.cost)
		if whole.listPrice {
			cost = "≈" + cost
		}
		parts = append(parts, cost)
	}
	return strings.Join(parts, " ")
}
