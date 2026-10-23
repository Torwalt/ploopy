package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/ui"
)

// defaultTriage is who weighs the findings when nothing else says: the
// strongest model, since the triage is the one session that decides.
var defaultTriage = review.Triage{Agent: "claude", Model: "opus", Effort: "high"}

// budgetChoices are the caps offered for the triage session, in dollars.
var budgetChoices = []string{"0", "1", "2", "5", "10", "20"}

// chooseTriage settles who weighs the findings. Flags win; then the review's
// own triage when it is written already, else the file's, the last review's
// choice, else the default. The board shows the result and lets the author
// change it.
func chooseTriage(ctx context.Context, cmd *cobra.Command, e *env, f *reviewFlags, current *review.Triage) (review.Triage, error) {
	t, _, err := chooseTriageWithSources(ctx, cmd, e, f, current)
	return t, err
}

// chooseTriageWithSources also says which values came from .ploopy.toml, so
// the board can show them as prefilled rather than locked.
func chooseTriageWithSources(ctx context.Context, cmd *cobra.Command, e *env, f *reviewFlags, current *review.Triage) (review.Triage, map[string]string, error) {
	t := defaultTriage
	sources := map[string]string{}
	switch {
	case current != nil:
		t = *current
	default:
		if last := loadLastTriage(ctx, e.root); last != nil && e.harnesses.Get(last.Agent) != nil {
			t = *last
		}
		if file := e.cfg.Review; file.TriageAgent != "" || file.TriageModel != "" ||
			file.TriageEffort != "" || file.TriageBudget != nil {
			if file.TriageAgent != "" {
				if file.TriageAgent != t.Agent {
					h := e.harnesses.Get(file.TriageAgent)
					t = review.Triage{Agent: file.TriageAgent, Budget: t.Budget}
					if h != nil {
						t.Model, t.Effort = h.Models()[0], preferredEffort(h)
					}
				}
				sources["agent"] = file.TriageAgent
			}
			if file.TriageModel != "" {
				t.Model = file.TriageModel
				sources["model"] = file.TriageModel
			}
			if file.TriageEffort != "" {
				t.Effort = file.TriageEffort
				sources["effort"] = file.TriageEffort
			}
			if file.TriageBudget != nil {
				t.Budget = *file.TriageBudget
				sources["budget"] = strconv.FormatFloat(*file.TriageBudget, 'f', -1, 64)
			}
		}
	}

	given := triageGivens(cmd)
	if given["agent"] && f.triageAgent != t.Agent {
		t = review.Triage{Agent: f.triageAgent, Budget: t.Budget}
		if h := e.harnesses.Get(f.triageAgent); h != nil {
			t.Model, t.Effort = h.Models()[0], preferredEffort(h)
		}
		if !given["model"] {
			delete(sources, "model")
		}
		if !given["effort"] {
			delete(sources, "effort")
		}
	}
	if given["model"] {
		t.Model = f.triageModel
	}
	if given["effort"] {
		t.Effort = f.triageEffort
	}
	if given["budget"] {
		t.Budget = f.triageBudget
	}
	for key, answered := range given {
		if answered {
			delete(sources, key)
		}
	}
	if sources["effort"] != "" {
		if h := e.harnesses.Get(t.Agent); h != nil && !contains(h.Efforts(), t.Effort) {
			return t, sources, fmt.Errorf("[review] triage_effort for %s must be one of %s, not %s",
				h.Name(), strings.Join(h.Efforts(), ", "), t.Effort)
		}
	}
	if err := validateTriage(e, t); err != nil {
		return t, sources, err
	}
	saveLastTriage(ctx, e.root, t)
	return t, sources, nil
}

// triageGivens is which triage settings the flags answered.
func triageGivens(cmd *cobra.Command) map[string]bool {
	flags := cmd.Flags()
	return map[string]bool{
		"agent":  flags.Changed("triage-agent"),
		"model":  flags.Changed("triage-model"),
		"effort": flags.Changed("triage-effort"),
		"budget": flags.Changed("triage-budget"),
	}
}

func validateTriage(e *env, t review.Triage) error {
	h := e.harnesses.Get(t.Agent)
	if h == nil {
		return fmt.Errorf("the triage agent must be one of %s, not %s", strings.Join(e.harnesses.Names(), ", "), t.Agent)
	}
	if t.Effort != "" && !contains(h.Efforts(), t.Effort) {
		return fmt.Errorf("the triage effort for %s must be one of %s", h.Name(), strings.Join(h.Efforts(), ", "))
	}
	if t.Budget < 0 {
		return fmt.Errorf("the triage budget must not be negative")
	}
	return nil
}

// askAgent asks for the agent, model, effort and spending cap of one session
// that decides, leaving out what given already holds.
func askAgent(e *env, title, budgetTitle string, t *review.Triage, given map[string]bool) error {
	budget := strconv.FormatFloat(t.Budget, 'f', -1, 64)
	firstAgent, firstModel := t.Agent, t.Model
	var fields []ui.Field
	if !given["agent"] {
		fields = append(fields, ui.Field{Title: "Agent", Value: &t.Agent, Choices: func() []ui.Choice {
			return labelled(e.harnesses.Names())
		}})
	}
	if !given["model"] {
		fields = append(fields, ui.Field{Title: "Model", Value: &t.Model, Binding: &t.Agent, Choices: func() []ui.Choice {
			models := triageModels(e, t.Agent)
			if t.Agent == firstAgent && firstModel != "" && !contains(models, firstModel) {
				models = append([]string{firstModel}, models...)
			}
			return labelled(models)
		}})
	}
	if !given["effort"] {
		fields = append(fields, ui.Field{Title: "Effort", Value: &t.Effort, Binding: &t.Agent, Choices: func() []ui.Choice {
			return labelled(effortChoices(e.harnesses.Get(t.Agent), t.Effort))
		}})
	}
	if !given["budget"] {
		fields = append(fields, ui.Field{Title: budgetTitle, Value: &budget,
			Choices: func() []ui.Choice { return budgetOptions(budget) }})
	}
	if err := ui.Form(title, fields); err != nil {
		return err
	}
	if !given["budget"] {
		value, err := strconv.ParseFloat(budget, 64)
		if err != nil {
			return fmt.Errorf("the budget %q is not dollars", budget)
		}
		t.Budget = value
	}
	return nil
}

// triageModels offers a harness's models with the strongest first, since a
// triage is worth it.
func triageModels(e *env, agent string) []string {
	models := e.harnesses.Get(agent).Models()
	if agent == defaultTriage.Agent && contains(models, defaultTriage.Model) {
		out := []string{defaultTriage.Model}
		for _, model := range models {
			if model != defaultTriage.Model {
				out = append(out, model)
			}
		}
		return out
	}
	return models
}

func budgetOptions(current string) []ui.Choice {
	values := budgetChoices
	if !contains(values, current) {
		values = append([]string{current}, values...)
	}
	choices := make([]ui.Choice, 0, len(values))
	for _, value := range values {
		label := "$" + value
		if value == "0" {
			label = "no cap"
		}
		choices = append(choices, ui.Choice{Label: label, Value: value})
	}
	return choices
}

// lastTriagePath sits beside the last run's settings, in the git directory
// every worktree shares.
func lastTriagePath(ctx context.Context, root string) string {
	if run := lastRunPath(ctx, root); run != "" {
		return filepath.Join(filepath.Dir(run), "last-triage.json")
	}
	return ""
}

func loadLastTriage(ctx context.Context, root string) *review.Triage {
	return loadAgentChoice(lastTriagePath(ctx, root))
}

func saveLastTriage(ctx context.Context, root string, t review.Triage) {
	saveAgentChoice(lastTriagePath(ctx, root), t)
}
