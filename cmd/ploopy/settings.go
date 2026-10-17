package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	Then          string         `json:"then,omitempty"`
}

// given is which settings the author already gave, as flags or in the plan's
// front matter. The board shows those locked and says where they came from.
type given struct {
	harness, model, effort, secondary, peak, subscriptions, spendAll, push, finish bool
	then                                                                           bool
	sources                                                                        map[string]string
}

// why is where a given setting came from, for the board's locked field.
func (g given) why(key string) string {
	if g.sources == nil {
		return ""
	}
	return g.sources[key]
}

func (g *given) from(key, source string) {
	if g.sources == nil {
		g.sources = map[string]string{}
	}
	g.sources[key] = source
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
	then          string
}

// chooseSettings puts everything on one board, prefilled from the last run
// and the leases. Enter starts; the settings the flags and the plan gave are
// locked and say where they came from.
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
	board := newSettingsBoard(ctx, e, p, g, &s, branch, open)
	return board.start(func() error {
		_, err := board.board.Run()
		return err
	})
}

// givenSettings is what the flags say, then what the plan's front matter says.
// Front matter only answers for the harness it names.
func givenSettings(cmd *cobra.Command, e *env, p *plan.Plan, f *runFlags) (runSettings, given, error) {
	var s runSettings
	var g given

	s.Harness = f.agent
	if s.Harness != "" {
		g.from("harness", "from a flag")
	} else {
		s.Harness = p.Settings["agent"]
		if s.Harness != "" {
			g.from("harness", "from the plan")
		}
	}
	if s.Harness != "" && e.harnesses.Get(s.Harness) == nil {
		return s, g, fmt.Errorf("--agent must be one of %s, not %s",
			strings.Join(e.harnesses.Names(), ", "), s.Harness)
	}
	fromPlan := p.Settings["agent"] != "" && p.Settings["agent"] == s.Harness
	s.Model, s.Effort = f.model, f.effort
	if s.Model != "" {
		g.from("model", "from a flag")
	} else if fromPlan && p.Settings["model"] != "" {
		s.Model = p.Settings["model"]
		g.from("model", "from the plan")
	}
	if s.Effort != "" {
		g.from("effort", "from a flag")
	} else if fromPlan && p.Settings["effort"] != "" {
		s.Effort = p.Settings["effort"]
		g.from("effort", "from the plan")
	}
	g.harness, g.model, g.effort = s.Harness != "", s.Model != "", s.Effort != ""

	if cmd.Flags().Changed("secondary") {
		g.secondary = true
		g.from("secondary", "from a flag")
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
		g.from("peak", "from a flag")
	}
	if cmd.Flags().Changed("subscriptions") {
		set, err := subscriptionSet(e.harnesses, f.subscriptions)
		if err != nil {
			return s, g, err
		}
		s.Subscriptions, g.subscriptions = set, true
		g.from("subscriptions", "from a flag")
	} else if p.Settings["subscriptions"] != "" {
		set, err := subscriptionSet(e.harnesses, p.Settings["subscriptions"])
		if err != nil {
			return s, g, err
		}
		s.Subscriptions, g.subscriptions = set, true
		g.from("subscriptions", "from the plan")
	}
	s.SpendAll = f.spendAll
	if cmd.Flags().Changed("spend-all") {
		g.spendAll = true
		g.from("spendAll", "from a flag")
	}
	s.Push = e.cfg.Push
	if cmd.Flags().Changed("push") {
		s.Push, g.push = f.push, true
		g.from("push", "from a flag")
	}
	if f.finish != "" {
		if !ui.Finish(f.finish).Valid() {
			return s, g, fmt.Errorf("--finish must be none, suspend or poweroff, not %s", f.finish)
		}
		s.Finish, g.finish = f.finish, true
		g.from("finish", "from a flag")
	}
	if f.then != "" {
		if !validThen(f.then) {
			return s, g, fmt.Errorf("--then must be stop or fixes, not %s", f.then)
		}
		s.Then, g.then = f.then, true
		g.from("then", "from a flag")
	}
	return s, g, nil
}

// validThen reports whether v names what follows a review.
func validThen(v string) bool { return v == "stop" || v == "fixes" }

// thenValue settles an unset Then as the default: stop.
func thenValue(v string) string {
	if v == "fixes" {
		return "fixes"
	}
	return "stop"
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
	if !g.then {
		s.Then = "stop"
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
	then := s.Then
	if then == "" {
		then = "stop"
	}
	if !validThen(then) {
		return chosenRun{}, fmt.Errorf("then must be stop or fixes, not %s", then)
	}
	return chosenRun{
		agent:         loop.Agent{Harness: h, Model: s.Model, Effort: s.Effort},
		secondary:     toLoopAgent(e.harnesses, s.Secondary),
		waitOffPeak:   s.Peak == "wait",
		subscriptions: subscriptions,
		spendAll:      s.SpendAll,
		push:          s.Push,
		finish:        finish,
		then:          then,
	}, nil
}

// overChosen lays what the author adjusted during a run over the settings it
// started with, so a chained run keeps those changes to the end.
func overChosen(e *env, c chosenRun, w control.Settings) chosenRun {
	if w.Finish != "" {
		c.finish = ui.Finish(w.Finish)
	}
	if w.Peak != "" {
		c.waitOffPeak = w.Peak == "wait"
	}
	if w.Push != nil {
		c.push = *w.Push
	}
	if w.Secondary != nil {
		c.secondary = toLoopAgent(e.harnesses, w.SecondaryAgent())
	}
	if w.Subscriptions != "" {
		c.subscriptions = w.Subscriptions
	}
	if w.SpendAll != nil {
		c.spendAll = *w.SpendAll
	}
	if w.Then != "" {
		c.then = w.Then
	}
	return c
}

// applyPlanSettings lays a plan's front matter over the settings a chained run
// inherited, so a fix plan may choose its own agent, model, effort or
// subscription set.
func applyPlanSettings(e *env, p *plan.Plan, c chosenRun) (chosenRun, error) {
	if name := p.Settings["agent"]; name != "" {
		h := e.harnesses.Get(name)
		if h == nil {
			return c, fmt.Errorf("%s asks for agent %s, which ploopy cannot drive here", p.Path, name)
		}
		if c.agent.Harness != h {
			c.agent = loop.Agent{Harness: h, Model: h.Models()[0], Effort: preferredEffort(h)}
		}
	}
	if model := p.Settings["model"]; model != "" {
		c.agent.Model = model
	}
	if effort := p.Settings["effort"]; effort != "" {
		if !contains(c.agent.Harness.Efforts(), effort) {
			return c, fmt.Errorf("the effort for %s must be one of %s, not %s",
				c.agent.Harness.Name(), strings.Join(c.agent.Harness.Efforts(), ", "), effort)
		}
		c.agent.Effort = effort
	}
	if set := p.Settings["subscriptions"]; set != "" {
		resolved, err := subscriptionSet(e.harnesses, set)
		if err != nil {
			return c, err
		}
		c.subscriptions = resolved
	}
	return c, nil
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
