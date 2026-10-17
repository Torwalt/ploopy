package main

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/claude"
	"github.com/Torwalt/ploopy/internal/harness/opencode"
	"github.com/Torwalt/ploopy/internal/plan"
)

func boardEnv(t *testing.T) *env {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return &env{
		root:      t.TempDir(),
		cfg:       config.Default(),
		harnesses: harness.Set{claude.New(), opencode.New()},
	}
}

func TestTheBoardLocksGivenSettingsWithTheirSource(t *testing.T) {
	e := boardEnv(t)
	p := plan.Parse("---\nagent: opencode\nmodel: deepseek/deepseek-flash\neffort: high\n---\n", "docs/plans/PASS.md")
	cmd, f := settingsCommand(t)
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	board := newSettingsBoard(context.Background(), e, p, g, &s, "pass", 3)

	fields := board.board.Columns[0].Fields
	for _, field := range fields {
		switch field.Key {
		case 'a', 'm', 'e':
			if field.Locked != "from the plan" {
				t.Fatalf("%c is locked with %q", field.Key, field.Locked)
			}
		}
	}
}

func TestASubscriptionOutsideTheSetIsDimmedAndSaysSo(t *testing.T) {
	e := boardEnv(t)
	p := plan.Parse("---\nagent: opencode\n---\n", "docs/plans/PASS.md")
	cmd, f := settingsCommand(t)
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	board := newSettingsBoard(context.Background(), e, p, g, &s, "pass", 3)

	focusRun := func() { board.board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}}) }
	cycleRun := func() { board.board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}) }
	focusRun()
	for i := 0; i < len(subscriptionChoices(e.harnesses, s.Harness)); i++ {
		cycleRun()
		column := board.board.Columns[1]
		if s.Subscriptions == "none" {
			if !column.Dim || !strings.Contains(column.Title, "not this run") {
				t.Fatalf("outside-set column is %+v", column)
			}
			return
		}
	}
	t.Fatal("the board never offered the bedrock-only set")
}
