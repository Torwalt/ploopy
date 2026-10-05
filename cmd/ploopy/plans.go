package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Torwalt/ploopy/internal/catalog"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

// plans is every plan of the repository, each where its progress lives.
func (e *env) plans(ctx context.Context) ([]catalog.Entry, error) {
	return catalog.Find(ctx, e.root, e.cfg.Plans)
}

// find takes a plan by path, by name, or by stem under the plans directory,
// wherever it lives. A path outside the plans directory is read from here.
func (e *env) find(ctx context.Context, name string) (catalog.Copy, error) {
	entries, err := e.plans(ctx)
	if err != nil {
		return catalog.Copy{}, err
	}
	wanted := strings.ToLower(strings.TrimSuffix(filepath.Base(name), ".md"))
	for _, entry := range entries {
		path := entry.Path()
		if path == name || path == filepath.Join(e.cfg.Plans, name) || path == filepath.Join(e.cfg.Plans, name+".md") ||
			strings.ToLower(entry.Best.Plan.Name()) == wanted {
			return entry.Best, nil
		}
	}
	p, err := e.resolve(name)
	if err != nil {
		return catalog.Copy{}, err
	}
	s, err := e.state(p)
	if err != nil {
		return catalog.Copy{}, err
	}
	branch, _ := e.repo().Branch(ctx)
	return catalog.Copy{Plan: p, State: s, Branch: branch, Root: e.root, Here: true}, nil
}

// planArg is the plan a command was given, or the one the author picks.
func (e *env) planArg(ctx context.Context, args []string, title string, open bool) (catalog.Copy, error) {
	if len(args) > 0 {
		return e.find(ctx, args[0])
	}
	if !ui.Interactive() {
		return catalog.Copy{}, fmt.Errorf("name the plan")
	}
	return e.pick(ctx, title, open)
}

// pick asks for a plan. With open, finished plans are left out.
func (e *env) pick(ctx context.Context, title string, open bool) (catalog.Copy, error) {
	entries, err := e.plans(ctx)
	if err != nil {
		return catalog.Copy{}, err
	}
	var choices []ui.Choice
	byPath := map[string]catalog.Copy{}
	for _, entry := range entries {
		if done, total := entry.Best.Done(); open && done == total {
			continue
		}
		choices = append(choices, ui.Choice{Label: e.label(entry.Best), Value: entry.Path()})
		byPath[entry.Path()] = entry.Best
	}
	if len(choices) == 0 {
		if open {
			return catalog.Copy{}, fmt.Errorf("no open plan under %s on any branch; --all offers finished ones", e.cfg.Plans)
		}
		return catalog.Copy{}, fmt.Errorf("no plan under %s on any branch", e.cfg.Plans)
	}
	picked, err := ui.Pick(title, "plan", choices)
	if err != nil {
		return catalog.Copy{}, err
	}
	return byPath[picked], nil
}

// label is one line about a plan: progress, what is next, and where it lives
// when that is not here.
func (e *env) label(c catalog.Copy) string {
	done, total := c.Done()
	label := fmt.Sprintf("%-44s %2d/%-2d", c.Plan.Path, done, total)
	if next := state.Resume(c.Plan, c.State); next != nil {
		label += "  next " + next.ID + " " + next.Title
	} else {
		label += "  complete"
	}
	if totals := ui.Totals(c.State); totals != "" {
		label += "  (" + totals + ")"
	}
	if !c.Here {
		label += "  · " + c.Where(e.root)
	}
	if c.Live != nil {
		label += " · running"
		if c.Live.Unit != "" {
			label += " " + c.Live.Unit
		}
	}
	return label
}
