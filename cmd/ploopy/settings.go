package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/ui"
)

// runSettings is what a run starts with beyond the plan and where it runs.
// The last run's are remembered, so the next can start the same way.
type runSettings struct {
	Harness       string         `json:"harness"`
	Model         string         `json:"model,omitempty"`
	Effort        string         `json:"effort,omitempty"`
	Secondary     *control.Agent `json:"secondary,omitempty"`
	Peak          string         `json:"peak,omitempty"` // wait or run
	Subscriptions string         `json:"subscriptions,omitempty"`
	SpendAll      bool           `json:"spendAll,omitempty"`
	Push          bool           `json:"push,omitempty"`
	Finish        string         `json:"finish,omitempty"`
}

// given is which settings the author already gave, as flags or in the plan's
// front matter. Those are never asked.
type given struct {
	harness, model, effort, secondary, peak, subscriptions, spendAll, push, finish bool
}

func (g given) all() bool {
	return g.harness && g.model && g.effort && g.secondary && g.peak &&
		g.subscriptions && g.spendAll && g.push && g.finish
}

// chosenRun is the settings, resolved.
type chosenRun struct {
	agent         loop.Agent
	secondary     *loop.Agent
	waitOffPeak   bool
	subscriptions string
	spendAll      bool
	push          bool
	finish        ui.Finish
}

// chooseSettings takes what the flags and the plan's front matter say, then
// asks for the rest on one page, prefilled from the last run. When there was
// a last run, starting like it is one keypress.
func chooseSettings(ctx context.Context, cmd *cobra.Command, e *env, p *plan.Plan, f *runFlags, branch string, open int) (chosenRun, error) {
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		return chosenRun{}, err
	}
	if !ui.Interactive() {
		return unattended(e, s)
	}
	// A branch that cannot be pushed is not asked about.
	if e.repo().CanPush(ctx, branch) != nil {
		g.push = true
	}

	last := loadLast(ctx, e.root)
	fill(e, &s, g, last)
	if !g.all() {
		printEstimates(loadHistory(ctx, e.root), open)
		change := true
		if last != nil {
			picked, err := ui.Pick("How should it run?", "agent", []ui.Choice{
				{Label: "like last time: " + describe(e, s), Value: "last"},
				{Label: "change the settings", Value: "change"},
			})
			if err != nil {
				return chosenRun{}, err
			}
			change = picked == "change"
		}
		if change {
			if err := askSettings(ctx, e, &s, g); err != nil {
				return chosenRun{}, err
			}
		}
	}

	chosen, err := resolveSettings(e, s)
	if err != nil {
		return chosenRun{}, err
	}
	saveLast(ctx, e.root, s)
	return chosen, nil
}

// givenSettings is what the flags say, then what the plan's front matter says.
// Front matter only answers for the harness it names.
func givenSettings(cmd *cobra.Command, e *env, p *plan.Plan, f *runFlags) (runSettings, given, error) {
	var s runSettings
	var g given

	s.Harness = f.agent
	if s.Harness == "" {
		s.Harness = p.Settings["agent"]
	}
	if s.Harness != "" && e.harnesses.Get(s.Harness) == nil {
		return s, g, fmt.Errorf("--agent must be one of %s, not %s",
			strings.Join(e.harnesses.Names(), ", "), s.Harness)
	}
	fromPlan := p.Settings["agent"] != "" && p.Settings["agent"] == s.Harness
	s.Model, s.Effort = f.model, f.effort
	if s.Model == "" && fromPlan {
		s.Model = p.Settings["model"]
	}
	if s.Effort == "" && fromPlan {
		s.Effort = p.Settings["effort"]
	}
	g.harness, g.model, g.effort = s.Harness != "", s.Model != "", s.Effort != ""

	if cmd.Flags().Changed("secondary") {
		g.secondary = true
		if f.secondary != "none" {
			agent, err := resolveAgent(e.harnesses, "--secondary", f.secondary, f.secondaryMod, f.secondaryEff, s.Effort)
			if err != nil {
				return s, g, err
			}
			s.Secondary = toControlAgent(agent)
		}
	}
	if f.peak != "" {
		if f.peak != "wait" && f.peak != "run" {
			return s, g, fmt.Errorf("--peak must be wait or run, not %s", f.peak)
		}
		s.Peak, g.peak = f.peak, true
	}
	if f.subscriptions != "" {
		set, err := subscriptionSet(e.harnesses, f.subscriptions)
		if err != nil {
			return s, g, err
		}
		s.Subscriptions, g.subscriptions = set, true
	} else if p.Settings["subscriptions"] != "" {
		set, err := subscriptionSet(e.harnesses, p.Settings["subscriptions"])
		if err != nil {
			return s, g, err
		}
		s.Subscriptions, g.subscriptions = set, true
	}
	s.SpendAll = f.spendAll
	if cmd.Flags().Changed("spend-all") {
		g.spendAll = true
	}
	s.Push = e.cfg.Push
	if cmd.Flags().Changed("push") {
		s.Push, g.push = f.push, true
	}
	if f.finish != "" {
		if !ui.Finish(f.finish).Valid() {
			return s, g, fmt.Errorf("--finish must be none, suspend or poweroff, not %s", f.finish)
		}
		s.Finish, g.finish = f.finish, true
	}
	return s, g, nil
}

// unattended settles a run nobody is watching: it needs the harness and the
// effort named, takes the harness's default model, runs through peak hours
// and takes no end action.
func unattended(e *env, s runSettings) (chosenRun, error) {
	if s.Harness == "" {
		return chosenRun{}, ui.NoTerminal("agent")
	}
	if s.Model == "" {
		s.Model = e.harnesses.Get(s.Harness).Models()[0]
	}
	if s.Effort == "" {
		return chosenRun{}, ui.NoTerminal("effort")
	}
	if s.Peak == "" {
		s.Peak = "run"
	}
	return resolveSettings(e, s)
}

// fill prefills what the author did not give: from the last run where it still
// applies, else from the defaults.
func fill(e *env, s *runSettings, g given, last *runSettings) {
	if !g.harness {
		s.Harness = e.harnesses[0].Name()
		if last != nil && e.harnesses.Get(last.Harness) != nil {
			s.Harness = last.Harness
		}
	}
	h := e.harnesses.Get(s.Harness)
	same := last != nil && last.Harness == s.Harness
	if !g.model {
		s.Model = h.Models()[0]
		if same && last.Model != "" {
			s.Model = last.Model
		}
	}
	if !g.effort {
		s.Effort = preferredEffort(h)
		if same && contains(h.Efforts(), last.Effort) {
			s.Effort = last.Effort
		}
	}
	if !g.secondary && last != nil && validSecondary(e.harnesses, last.Secondary, s.Harness) {
		s.Secondary = last.Secondary
	}
	if !g.peak {
		s.Peak = "wait"
		if last != nil && last.Peak != "" {
			s.Peak = last.Peak
		}
	}
	if !g.subscriptions {
		s.Subscriptions = "all"
		if last != nil {
			if set, err := subscriptionSet(e.harnesses, last.Subscriptions); err == nil {
				s.Subscriptions = set
			}
		}
	}
	if !g.spendAll && last != nil {
		s.SpendAll = last.SpendAll
	}
	if !g.push && last != nil {
		s.Push = last.Push
	}
	if !g.finish {
		s.Finish = string(ui.FinishNone)
		if last != nil && ui.Finish(last.Finish).Valid() {
			s.Finish = last.Finish
		}
	}
}

func preferredEffort(h harness.Harness) string {
	if contains(h.Efforts(), "high") {
		return "high"
	}
	return h.Efforts()[0]
}

func validSecondary(harnesses harness.Set, agent *control.Agent, primary string) bool {
	if agent == nil || agent.Harness == primary {
		return false
	}
	h := harnesses.Get(agent.Harness)
	return h != nil && contains(h.Efforts(), agent.Effort)
}

// askSettings puts everything not given on one page.
func askSettings(ctx context.Context, e *env, s *runSettings, g given) error {
	var fields []ui.Field
	if !g.harness {
		fields = append(fields, ui.Field{Title: "Agent", Value: &s.Harness, Choices: func() []ui.Choice {
			return labelled(e.harnesses.Names())
		}})
	}
	if !g.model {
		firstHarness, firstModel := s.Harness, s.Model
		fields = append(fields, ui.Field{Title: "Model", Value: &s.Model, Binding: &s.Harness, Choices: func() []ui.Choice {
			models := e.harnesses.Get(s.Harness).Models()
			// A model named by hand last time stays on offer.
			if s.Harness == firstHarness && firstModel != "" && !contains(models, firstModel) {
				models = append([]string{firstModel}, models...)
			}
			return labelled(models)
		}})
	}
	if !g.effort {
		fields = append(fields, ui.Field{Title: "Effort", Value: &s.Effort, Binding: &s.Harness, Choices: func() []ui.Choice {
			return labelled(e.harnesses.Get(s.Harness).Efforts())
		}})
	}
	secondary := encodeAgent(s.Secondary)
	if !g.secondary {
		fields = append(fields, ui.Field{Title: "On a usage limit", Value: &secondary, Binding: &s.Harness,
			Choices: func() []ui.Choice { return secondaryChoices(e.harnesses, s.Harness) }})
	}
	if peaked := peakedHarness(e.harnesses); !g.peak && peaked != nil {
		title := fmt.Sprintf("A unit due in %s's peak hours (%s)",
			peaked.Name(), peakHours(peaked.PeakWindows(), time.Now()))
		fields = append(fields, ui.Field{Title: title, Value: &s.Peak, Choices: func() []ui.Choice { return peakChoices }})
	}
	if !g.subscriptions {
		fields = append(fields, ui.Field{Title: "Subscriptions", Value: &s.Subscriptions, Binding: &s.Harness,
			Choices: func() []ui.Choice { return subscriptionChoices(e.harnesses, s.Harness) }})
	}
	spend := spendLabel(s.SpendAll)
	if !g.spendAll {
		fields = append(fields, ui.Field{Title: "Spending", Value: &spend,
			Choices: func() []ui.Choice { return spendChoices }})
	}
	push := yesNo(s.Push)
	if !g.push {
		fields = append(fields, ui.Field{Title: "Push the branch when the run ends", Value: &push,
			Choices: func() []ui.Choice { return yesNoChoices(true) }})
	}
	if !g.finish {
		fields = append(fields, ui.Field{Title: "When the run ends", Value: &s.Finish,
			Choices: func() []ui.Choice { return ui.Finishes }})
	}

	if err := ui.FormNote("How should it run?", leasesNote(ctx, e, time.Now()), fields); err != nil {
		return err
	}
	if !g.secondary {
		s.Secondary = decodeAgent(secondary)
	}
	if !g.spendAll {
		s.SpendAll = spend == "all"
	}
	if !g.push {
		s.Push = push == "yes"
	}
	return nil
}

var spendChoices = []ui.Choice{
	{Label: "keep reserves", Value: "keep"},
	{Label: "spend all", Value: "all"},
}

func spendLabel(spendAll bool) string {
	if spendAll {
		return "all"
	}
	return "keep"
}

// subscriptionSet normalizes a subscription set. Empty means all; names may be
// separated by commas or spaces. A named harness must have a subscription.
func subscriptionSet(harnesses harness.Set, value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", " "))
	switch value {
	case "", "all":
		return "all", nil
	case "none":
		return "none", nil
	}
	seen := map[string]bool{}
	var names []string
	for _, name := range strings.Fields(value) {
		if seen[name] {
			continue
		}
		h := harnesses.Get(name)
		if h == nil {
			return "", fmt.Errorf("%s is not a harness; use all, none, or %s",
				name, strings.Join(harnesses.Names(), ", "))
		}
		if _, metered := h.(harness.Metered); !metered {
			return "", fmt.Errorf("%s has no subscription to lend", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	return strings.Join(names, " "), nil
}

// subscriptionChoices offers every set the page can choose: all lent, one
// subscription alone, or the bedrock only.
func subscriptionChoices(harnesses harness.Set, except string) []ui.Choice {
	choices := []ui.Choice{{Label: "all lent", Value: "all"}}
	for _, h := range harnesses {
		if h.Name() == except {
			continue
		}
		if _, metered := h.(harness.Metered); !metered {
			continue
		}
		choices = append(choices, ui.Choice{Label: h.Name() + " only", Value: h.Name()})
	}
	return append(choices, ui.Choice{Label: "bedrock only", Value: "none"})
}

// subscriptionText names a subscription set for a person.
func subscriptionText(set string) string {
	if set == "" || set == "all" {
		return "all lent"
	}
	if set == "none" {
		return "the bedrock only"
	}
	names := strings.Fields(set)
	if len(names) == 1 {
		return names[0] + " only"
	}
	return strings.Join(names, " and ") + " only"
}

// peakedHarness is a harness with peak hours, or nil.
func peakedHarness(harnesses harness.Set) harness.Harness {
	for _, h := range harnesses {
		if len(h.PeakWindows()) > 0 {
			return h
		}
	}
	return nil
}

// resolveSettings checks the settings and turns names into harnesses.
func resolveSettings(e *env, s runSettings) (chosenRun, error) {
	h := e.harnesses.Get(s.Harness)
	if h == nil {
		return chosenRun{}, fmt.Errorf("--agent must be one of %s, not %s",
			strings.Join(e.harnesses.Names(), ", "), s.Harness)
	}
	if !contains(h.Efforts(), s.Effort) {
		return chosenRun{}, fmt.Errorf("--effort for %s must be one of %s",
			h.Name(), strings.Join(h.Efforts(), ", "))
	}
	subscriptions, err := subscriptionSet(e.harnesses, s.Subscriptions)
	if err != nil {
		return chosenRun{}, err
	}
	finish := ui.Finish(s.Finish)
	if finish == "" {
		finish = ui.FinishNone
	}
	return chosenRun{
		agent:         loop.Agent{Harness: h, Model: s.Model, Effort: s.Effort},
		secondary:     toLoopAgent(e.harnesses, s.Secondary),
		waitOffPeak:   s.Peak == "wait",
		subscriptions: subscriptions,
		spendAll:      s.SpendAll,
		push:          s.Push,
		finish:        finish,
	}, nil
}

// describe renders settings on one line. Peak hours are left out when neither
// agent has any.
func describe(e *env, s runSettings) string {
	agent := control.Agent{Harness: s.Harness, Model: s.Model, Effort: s.Effort}
	settings := control.Settings{
		Finish: s.Finish, Peak: s.Peak, Push: &s.Push, Secondary: s.Secondary,
		Subscriptions: s.Subscriptions, SpendAll: &s.SpendAll,
	}
	peaked := len(e.harnesses.Get(s.Harness).PeakWindows()) > 0
	if secondary := e.harnesses.Get(agentHarness(s.Secondary)); secondary != nil {
		peaked = peaked || len(secondary.PeakWindows()) > 0
	}
	if !peaked {
		settings.Peak = ""
	}
	return agent.Label() + " · " + settings.Describe()
}

func agentHarness(agent *control.Agent) string {
	if agent == nil {
		return ""
	}
	return agent.Harness
}

// lastRunPath is in the repository's git directory, which its worktrees share
// and nothing commits.
func lastRunPath(ctx context.Context, root string) string {
	dir, err := repo.New(root, nil).CommonDir(ctx)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ploopy", "last-run.json")
}

func loadLast(ctx context.Context, root string) *runSettings {
	path := lastRunPath(ctx, root)
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s runSettings
	if json.Unmarshal(raw, &s) != nil || s.Harness == "" {
		return nil
	}
	return &s
}

func saveLast(ctx context.Context, root string, s runSettings) {
	path := lastRunPath(ctx, root)
	if path == "" {
		return
	}
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		_ = os.WriteFile(path, append(body, '\n'), 0o644)
	}
}
