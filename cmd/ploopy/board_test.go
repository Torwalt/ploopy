package main

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/claude"
	"github.com/Torwalt/ploopy/internal/harness/opencode"
	"github.com/Torwalt/ploopy/internal/lease"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/ui"
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

// liveRun announces a run for the adjust board.
func liveRun(t *testing.T, e *env, settings control.Settings) control.Live {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if settings.Finish == "" {
		settings.Finish = "none"
	}
	if settings.Peak == "" {
		settings.Peak = "run"
	}
	if _, err := control.Register(control.Status{
		Root: e.root, Plan: "docs/plans/PASS.md", Branch: "pass",
		Agent: "opencode deepseek/deepseek-flash high", Settings: settings,
	}); err != nil {
		t.Fatal(err)
	}
	lives, err := control.List()
	if err != nil || len(lives) != 1 {
		t.Fatalf("listed %v, %v", lives, err)
	}
	return lives[0]
}

func key(b *ui.Board, r rune) {
	b.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

func TestTheAdjustBoardLocksTheBedrockAndOffersStop(t *testing.T) {
	e := boardEnv(t)
	live := liveRun(t, e, control.Settings{})
	board := newAdjustBoard(context.Background(), e, live)

	locked := map[rune]bool{'a': false, 'm': false, 'e': false}
	for _, field := range board.board.Columns[0].Fields {
		if _, ok := locked[field.Key]; !ok {
			continue
		}
		if field.Locked != adjustLock {
			t.Fatalf("%c is locked with %q", field.Key, field.Locked)
		}
		locked[field.Key] = true
	}
	for key, seen := range locked {
		if !seen {
			t.Fatalf("the bedrock has no %c field", key)
		}
	}

	run := board.board.Columns[len(board.board.Columns)-1]
	if run.Title != "Run" {
		t.Fatalf("the last column is %q", run.Title)
	}
	for _, field := range run.Fields {
		if field.Key == 'x' {
			return
		}
	}
	t.Fatal("the adjust board offers no stop field")
}

func TestTheAdjustBoardAppliesItsSettingsAndLeases(t *testing.T) {
	e := boardEnv(t)
	live := liveRun(t, e, control.Settings{Subscriptions: "all"})
	board := newAdjustBoard(context.Background(), e, live)

	// Column 3 is Run; column 2 is the claude subscription.
	key(board.board, '3')
	key(board.board, 's')
	key(board.board, 'f')
	key(board.board, 'x')
	key(board.board, 'w')
	key(board.board, '2')
	key(board.board, 'u')

	now := time.Now()
	board.read()
	if err := board.applyAdjust(live, now); err != nil {
		t.Fatal(err)
	}

	lives, err := control.List()
	if err != nil || len(lives) != 1 {
		t.Fatalf("listed %v, %v", lives, err)
	}
	wanted := lives[0].Wanted
	if wanted.Finish != "suspend" || wanted.Subscriptions != "claude" || !wanted.Stops() {
		t.Fatalf("applied %+v", wanted)
	}
	if wanted.SpendAll == nil || !*wanted.SpendAll {
		t.Fatalf("spend all applied as %v", wanted.SpendAll)
	}

	file, err := lease.Load()
	if err != nil {
		t.Fatal(err)
	}
	active := file.Active(now)
	if len(active) != 1 || active[0].Harness != "claude" || active[0].Kind != lease.Until {
		t.Fatalf("leases are %+v", active)
	}
}
