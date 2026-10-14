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
// own triage when it is written already, else the last review's choice, else
// the default; and whatever no flag gave is asked on one page.
func chooseTriage(ctx context.Context, cmd *cobra.Command, e *env, f *reviewFlags, current *review.Triage) (review.Triage, error) {
	t := defaultTriage
	keep := "like last time"
	switch {
	case current != nil:
		t, keep = *current, "as this review has it"
	default:
		if last := loadLastTriage(ctx, e.root); last != nil && e.harnesses.Get(last.Agent) != nil {
			t = *last
		}
	}

	flags := cmd.Flags()
	given := map[string]bool{}
	for _, name := range []string{"triage-agent", "triage-model", "triage-effort", "triage-budget"} {
		given[name] = flags.Changed(name)
	}
	if given["triage-agent"] && f.triageAgent != t.Agent {
		t = review.Triage{Agent: f.triageAgent, Budget: t.Budget}
		if h := e.harnesses.Get(f.triageAgent); h != nil {
			t.Model, t.Effort = h.Models()[0], preferredEffort(h)
		}
	}
	if given["triage-model"] {
		t.Model = f.triageModel
	}
	if given["triage-effort"] {
		t.Effort = f.triageEffort
	}
	if given["triage-budget"] {
		t.Budget = f.triageBudget
	}

	all := given["triage-agent"] && given["triage-model"] && given["triage-effort"] && given["triage-budget"]
	if ui.Interactive() && !all {
		picked, err := ui.Pick("Who weighs the findings?", "triage-agent", []ui.Choice{
			{Label: keep + ": " + t.Label(), Value: "keep"},
			{Label: "change the triage", Value: "change"},
		})
		if err != nil {
			return t, err
		}
		if picked == "change" {
			if err := askTriage(e, &t, given); err != nil {
				return t, err
			}
		}
	}

	h := e.harnesses.Get(t.Agent)
	if h == nil {
		return t, fmt.Errorf("the triage agent must be one of %s, not %s", strings.Join(e.harnesses.Names(), ", "), t.Agent)
	}
	if t.Effort != "" && !contains(h.Efforts(), t.Effort) {
		return t, fmt.Errorf("the triage effort for %s must be one of %s", h.Name(), strings.Join(h.Efforts(), ", "))
	}
	if t.Budget < 0 {
		return t, fmt.Errorf("the triage budget must not be negative")
	}
	saveLastTriage(ctx, e.root, t)
	return t, nil
}

// askTriage puts everything the flags did not give on one page.
func askTriage(e *env, t *review.Triage, given map[string]bool) error {
	return askAgent(e, "Who weighs the findings?", "Spending cap for the triage session", t, map[string]bool{
		"agent": given["triage-agent"], "model": given["triage-model"],
		"effort": given["triage-effort"], "budget": given["triage-budget"],
	})
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
			return labelled(e.harnesses.Get(t.Agent).Efforts())
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
