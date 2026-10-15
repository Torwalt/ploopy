package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/history"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/ui"
)

// awayPresets are the spans offered on the settings page; --away takes any.
var awayPresets = []string{"21:00-09:00", "22:00-08:00", "20:00-08:00", "23:00-07:00", "18:00-09:00"}

// defaultReserve is the share of every week, in percent, a run leaves to the
// author unless told otherwise.
const defaultReserve = 20

func awayChoices(current string) []ui.Choice {
	choices := []ui.Choice{{Label: "not away: the subscriptions are mine", Value: "none"}}
	spans := awayPresets
	if current != "" && current != "none" && !contains(spans, current) {
		spans = append([]string{current}, spans...)
	}
	for _, span := range spans {
		choices = append(choices, ui.Choice{Label: "away " + pretty(span) + ": they run units then", Value: span})
	}
	return choices
}

func pretty(span string) string { return strings.Replace(span, "-", "–", 1) }

// nightHarnesses are the subscriptions that may take sessions while the
// author is away: every metered harness but the run's own.
func nightHarnesses(harnesses harness.Set, primary string) []harness.Harness {
	var out []harness.Harness
	for _, h := range harnesses {
		if _, ok := h.(harness.Metered); ok && h.Name() != primary {
			out = append(out, h)
		}
	}
	return out
}

// parseAwayFlag accepts a span such as 21:00-09:00, or none.
func parseAwayFlag(flag, text string) (string, error) {
	if text == "none" {
		return text, nil
	}
	span, err := loop.ParseAway(text)
	if err != nil {
		return "", fmt.Errorf("%s: %w", flag, err)
	}
	return span.String(), nil
}

// parseNight reads agents such as claude=2,codex/gpt-6.1-sol/high=1: a
// harness, optionally its model and effort, and the fresh windows it may open.
func parseNight(harnesses harness.Set, flag, text string) ([]control.NightAgent, error) {
	var out []control.NightAgent
	for _, part := range strings.Split(text, ",") {
		spec, count, ok := strings.Cut(strings.TrimSpace(part), "=")
		windows, err := strconv.Atoi(count)
		if !ok || err != nil || windows < 0 {
			return nil, fmt.Errorf("%s takes harness[/model[/effort]]=windows, such as claude=2,codex=1; not %q", flag, part)
		}
		fields := strings.SplitN(spec, "/", 3)
		for len(fields) < 3 {
			fields = append(fields, "")
		}
		agent, err := resolveAgent(harnesses, flag, fields[0], fields[1], fields[2], "high")
		if err != nil {
			return nil, err
		}
		if _, metered := agent.Harness.(harness.Metered); !metered {
			return nil, fmt.Errorf("%s: %s has no usage windows to use while you are away", flag, fields[0])
		}
		out = append(out, control.NightAgent{Agent: *toControlAgent(agent), Windows: windows})
	}
	return out, nil
}

func reservePercent(reserve *int) int {
	if reserve == nil {
		return defaultReserve
	}
	return *reserve
}

// toLoopNight resolves how a run uses the subscriptions while the author is
// away, or nil when the author is not.
func toLoopNight(harnesses harness.Set, away string, agents []control.NightAgent, reserve *int) (*loop.Night, error) {
	if away == "" || away == "none" {
		return nil, nil
	}
	span, err := loop.ParseAway(away)
	if err != nil {
		return nil, err
	}
	night := &loop.Night{Away: span, Reserve: float64(reservePercent(reserve)) / 100}
	for _, a := range agents {
		if agent := toLoopAgent(harnesses, &a.Agent); agent != nil {
			night.Agents = append(night.Agents, loop.NightAgent{Agent: *agent, Windows: a.Windows})
		}
	}
	return night, nil
}

// historyPath is the repository's history, in its git directory, which its
// worktrees share and nothing commits.
func historyPath(ctx context.Context, root string) string {
	dir, err := repo.New(root, nil).CommonDir(ctx)
	if err != nil {
		return ""
	}
	return history.Path(dir)
}

func loadHistory(ctx context.Context, root string) []history.Record {
	path := historyPath(ctx, root)
	if path == "" {
		return nil
	}
	records, _ := history.Load(path)
	return records
}

// windowsNow is a subscription's usage windows: read live where the harness
// can tell, else the last a session reported. seen says which.
func windowsNow(ctx context.Context, h harness.Harness, records []history.Record) (*harness.Budget, string) {
	if metered, ok := h.(harness.Metered); ok {
		if budget, err := metered.Budget(ctx); err == nil && budget != nil {
			return budget, " · now"
		}
	}
	budget := history.Latest(records, h.Name())
	if budget == nil {
		return nil, ""
	}
	return budget, " · seen " + budget.At.Local().Format("Mon 15:04")
}

// proposal is what one subscription has left for an away span, and what the
// plan would take of it.
type proposal struct {
	fits    int  // fresh windows that fit before the author is back
	suggest int  // of those, the ones the weekly reserve leaves room for
	rest    bool // the window running now resets inside the span, free to use up
	lines   []string
}

func propose(budget *harness.Budget, seen string, label string, records []history.Record, away loop.Away, reserve float64, open int, now time.Time) proposal {
	start, end, inside := away.Span(now)
	from := start
	if inside {
		from = now
	}
	var p proposal
	first := from
	if budget != nil && budget.Session.Active(from) {
		first = budget.Session.ResetAt
		if first.After(end) {
			first = end
		} else {
			p.rest = budget.Session.Used < 1
		}
	}
	if gap := end.Sub(first); gap > 0 {
		p.fits = int(gap / harness.SessionWindow)
	}
	p.suggest = p.fits

	p.lines = append(p.lines, label+" · "+budget.Describe(now)+seen)
	fit := fmt.Sprintf("%d fresh window(s) fit before %s", p.fits, end.Format("15:04"))
	if p.rest {
		fit = fmt.Sprintf("the rest of its window is free until %s; then %s", first.Format("15:04"), fit)
	}
	p.lines = append(p.lines, "  "+fit)
	est := history.For(records, label)
	p.lines = append(p.lines, "  "+est.Describe())

	weekly := 0.0
	if budget != nil {
		weekly = budget.Weekly.UsedAt(from)
	}
	keep := int(math.Round(reserve * 100))
	if weekly >= 1-reserve {
		p.suggest = 0
		p.lines = append(p.lines, fmt.Sprintf("  the week is %.0f%% spent, past the %d%% kept for you: it rests", weekly*100, keep))
		return p
	}
	if !est.Metered() {
		return p
	}
	per := est.WeeklyPerWindow()
	share := 0.0 // of a window, the rest of the running one included
	if p.rest {
		share = 1 - budget.Session.Used
	}
	room := 1 - reserve - weekly - share*per
	// A window too small to move the week leaves every window that fits.
	if per > 0 {
		p.suggest = max(min(int(math.Floor(room/per)), p.fits), 0)
	}
	share += float64(p.suggest)
	units := int(math.Round(share * est.UnitsPerWindow()))
	p.lines = append(p.lines, fmt.Sprintf("  %d fresh window(s) ≈ %d of %d open units; the week ≈ %.0f%% spent after them, %d%% kept for you",
		p.suggest, min(units, open), open, (weekly+share*per)*100, keep))
	return p
}

// windowChoices offers leaving a subscription alone, using only what is left
// of a window running out while the author is away, and each count of fresh
// windows that fit.
func windowChoices(name string, fits int) []ui.Choice {
	choices := []ui.Choice{
		{Label: "leave " + name + " alone", Value: "-1"},
		{Label: "no fresh window, only one that resets before I am back", Value: "0"},
	}
	for n := 1; n <= fits; n++ {
		label := fmt.Sprintf("%d fresh window", n)
		if n > 1 {
			label += "s"
		}
		choices = append(choices, ui.Choice{Label: label, Value: strconv.Itoa(n)})
	}
	return choices
}

// nightAgentFor is the agent a harness ran as last time, with an effort it
// still offers.
func nightAgentFor(agents []control.NightAgent, h harness.Harness) control.NightAgent {
	for _, a := range agents {
		if a.Harness == h.Name() {
			if !contains(h.Efforts(), a.Effort) {
				a.Effort = preferredEffort(h)
			}
			return a
		}
	}
	return control.NightAgent{Agent: control.Agent{Harness: h.Name(), Model: h.Models()[0], Effort: preferredEffort(h)}}
}

// askNight puts what each subscription has left beside how many fresh windows
// it may open while the author is away, and asks.
func askNight(ctx context.Context, e *env, s *runSettings, g given, open int) error {
	away, err := loop.ParseAway(s.Away)
	if err != nil {
		return err
	}
	records := loadHistory(ctx, e.root)
	reserve := strconv.Itoa(reservePercent(s.Reserve))
	now := time.Now()

	type ask struct {
		agent   control.Agent
		windows string
	}
	var asks []*ask
	var notes []string
	var fields []ui.Field
	for _, h := range nightHarnesses(e.harnesses, s.Harness) {
		agent := nightAgentFor(s.Night, h)
		budget, seen := windowsNow(ctx, h, records)
		p := propose(budget, seen, agent.Label(), records, away, float64(reservePercent(s.Reserve))/100, open, now)
		notes = append(notes, p.lines...)
		a := &ask{agent: agent.Agent, windows: strconv.Itoa(p.suggest)}
		asks = append(asks, a)
		h, fits, model := h, p.fits, agent.Model
		fields = append(fields,
			ui.Field{Title: h.Name() + " while you are away", Value: &a.windows,
				Choices: func() []ui.Choice { return windowChoices(h.Name(), fits) }},
			ui.Field{Title: h.Name() + " model", Value: &a.agent.Model, Choices: func() []ui.Choice {
				models := h.Models()
				if !contains(models, model) {
					models = append([]string{model}, models...)
				}
				return labelled(models)
			}},
			ui.Field{Title: h.Name() + " effort", Value: &a.agent.Effort,
				Choices: func() []ui.Choice { return labelled(h.Efforts()) }},
		)
	}
	if len(asks) == 0 {
		return fmt.Errorf("no subscription can run while you are away: %s is the only one, and it is the run's own agent", s.Harness)
	}
	primary := control.Agent{Harness: s.Harness, Model: s.Model, Effort: s.Effort}.Label()
	notes = append(notes, primary+" takes the rest · "+history.For(records, primary).Describe(),
		fmt.Sprintf("plan: %d open unit(s)", open))
	if !g.reserve {
		fields = append(fields, ui.Field{Title: "Share of every week kept for you", Value: &reserve,
			Choices: func() []ui.Choice { return reserveChoices(reserve) }})
	}

	if err := ui.FormNote("While you are away, "+pretty(s.Away), strings.Join(notes, "\n"), fields); err != nil {
		return err
	}
	s.Night = nil
	for _, a := range asks {
		if n, _ := strconv.Atoi(a.windows); n >= 0 {
			s.Night = append(s.Night, control.NightAgent{Agent: a.agent, Windows: n})
		}
	}
	percent, _ := strconv.Atoi(reserve)
	s.Reserve = &percent
	return nil
}

// reserveChoices offers the usual reserves, and the current one when it is
// not among them: a select set to a value it does not offer takes its first.
func reserveChoices(current string) []ui.Choice {
	percents := []string{"0", "10", "20", "30", "40", "50"}
	if !contains(percents, current) {
		percents = append([]string{current}, percents...)
	}
	choices := make([]ui.Choice, 0, len(percents))
	for _, percent := range percents {
		choices = append(choices, ui.Choice{Label: percent + "%", Value: percent})
	}
	return choices
}

// printEstimates says what units took each agent in this repository before
// the author picks one.
func printEstimates(records []history.Record, open int) {
	var lines []string
	for _, est := range history.Estimates(records) {
		if !est.Known() {
			continue
		}
		line := fmt.Sprintf("  %-38s %s", est.Agent, est.Describe())
		if plan := est.Plan(open); plan != "" {
			line += "\n  " + strings.Repeat(" ", 38) + " " + plan
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return
	}
	fmt.Println("What a landed unit took here:")
	fmt.Println(strings.Join(lines, "\n"))
	fmt.Println()
}

func nightSuffix(away string, agents []control.NightAgent) string {
	if away == "" || away == "none" {
		return ""
	}
	var parts []string
	for _, a := range agents {
		parts = append(parts, fmt.Sprintf("%s ×%d", a.Harness, a.Windows))
	}
	if len(parts) == 0 {
		return ", away " + pretty(away)
	}
	return ", away " + pretty(away) + ": " + strings.Join(parts, ", ")
}
