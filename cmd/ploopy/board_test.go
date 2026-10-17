package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/claude"
	"github.com/Torwalt/ploopy/internal/harness/opencode"
	"github.com/Torwalt/ploopy/internal/lease"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/review"
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
	return liveRunPlan(t, e, settings, "docs/plans/PASS.md")
}

func liveRunPlan(t *testing.T, e *env, settings control.Settings, planPath string) control.Live {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if settings.Finish == "" {
		settings.Finish = "none"
	}
	if settings.Peak == "" {
		settings.Peak = "run"
	}
	if _, err := control.Register(control.Status{
		Root: e.root, Plan: planPath, Branch: "pass",
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
	var stop, then bool
	for _, field := range run.Fields {
		stop = stop || field.Key == 'x'
		then = then || field.Key == 'n'
	}
	if !stop {
		t.Fatal("the adjust board offers no stop field")
	}
	if then {
		t.Fatal("the adjust board offers then on a plan that implements")
	}
}

func TestTheAdjustBoardOffersThenOnAReviewRun(t *testing.T) {
	e := boardEnv(t)
	path := filepath.Join(e.root, "docs/plans/PASS_REVIEW.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nreviews: docs/plans/PASS.md\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	live := liveRunPlan(t, e, control.Settings{}, "docs/plans/PASS_REVIEW.md")
	board := newAdjustBoard(context.Background(), e, live)

	run := board.board.Columns[len(board.board.Columns)-1]
	if run.Title != "Run" {
		t.Fatalf("the last column is %q", run.Title)
	}
	fieldByKey(t, run, 'n')
}

func TestTheAdjustBoardSaysEnterApplies(t *testing.T) {
	e := boardEnv(t)
	live := liveRun(t, e, control.Settings{})
	board := newAdjustBoard(context.Background(), e, live)

	view := board.board.View()
	if !strings.Contains(view, "enter apply") {
		t.Fatalf("the adjust board does not say enter applies:\n%s", view)
	}
	if strings.Contains(view, "enter start") {
		t.Fatalf("the adjust board still says enter starts:\n%s", view)
	}
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

func TestThenIsAnAdjustFlag(t *testing.T) {
	e := boardEnv(t)
	f := &adjustFlags{}
	cmd := &cobra.Command{Use: "adjust"}
	cmd.Flags().StringVar(&f.then, "then", "", "")
	if err := cmd.ParseFlags([]string{"--then", "fixes"}); err != nil {
		t.Fatal(err)
	}
	change, err := changeFromFlags(cmd, e, f)
	if err != nil || change.Then != "fixes" {
		t.Fatalf("--then fixes changed %+v, error %v", change, err)
	}
}

func TestChangingTheAgentKeepsAGivenModelOrEffort(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		key  rune
		want string
	}{
		{"model", []string{"--model", "opus"}, 'm', "opus"},
		{"effort", []string{"--effort", "xhigh"}, 'e', "xhigh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := boardEnv(t)
			p := plan.Parse("", "docs/plans/PASS.md")
			cmd, f := settingsCommand(t, tc.args...)
			s, g, err := givenSettings(cmd, e, p, f)
			if err != nil {
				t.Fatal(err)
			}
			fill(e, &s, g, nil)
			board := newSettingsBoard(context.Background(), e, p, g, &s, "pass", 3)

			key(board.board, '1')
			key(board.board, 'a')
			if s.Harness != "opencode" {
				t.Fatalf("the agent changed to %q", s.Harness)
			}
			field := fieldByKey(t, board.board.Columns[0], tc.key)
			if got := *field.Value; got != tc.want {
				t.Fatalf("%c is %q, want the given %q", tc.key, got, tc.want)
			}
			if field.Locked != "from a flag" {
				t.Fatalf("%c is locked with %q", tc.key, field.Locked)
			}
		})
	}
}

func TestChangingTheTriageAgentKeepsAGivenModel(t *testing.T) {
	e := boardEnv(t)
	p := plan.Parse("---\nreviews: docs/plans/PASS.md\n---\n", "docs/plans/PASS_REVIEW.md")
	tri := review.Triage{Agent: "claude", Model: "sonnet", Effort: "high"}
	board := newReviewBoard(context.Background(), e, p, given{}, &runSettings{
		Harness: "claude", Model: "sonnet", Effort: "high",
	}, "pass", 3, tri, map[string]bool{"model": true}, "")

	at, _ := columnByTitle(t, board.board, "Triage")
	key(board.board, rune('1'+at))
	key(board.board, 'a')
	if board.triage.agent != "opencode" {
		t.Fatalf("the triage agent changed to %q", board.triage.agent)
	}
	_, column := columnByTitle(t, board.board, "Triage")
	field := fieldByKey(t, column, 'm')
	if got := *field.Value; got != "sonnet" {
		t.Fatalf("the triage model is %q, want the given sonnet", got)
	}
	if field.Locked != "from a flag" {
		t.Fatalf("the triage model is locked with %q", field.Locked)
	}
}

func TestTheRunBoardClearsASecondaryOnItsOwnBedrockBeforeTheFirstFrame(t *testing.T) {
	e := boardEnv(t)
	p := plan.Parse("---\nagent: claude\n---\n", "docs/plans/PASS.md")
	s := runSettings{
		Harness: "claude", Model: "sonnet", Effort: "medium",
		Secondary: &control.Agent{Harness: "claude", Model: "sonnet", Effort: "medium"},
	}
	board := newSettingsBoard(context.Background(), e, p, given{}, &s, "pass", 3)

	if board.secondary != "" || s.Secondary != nil {
		t.Fatalf("the secondary on the bedrock is %q, %+v", board.secondary, s.Secondary)
	}
	field := fieldByKey(t, board.board.Columns[0], 'l')
	if got := *field.Value; got != "" {
		t.Fatalf("the limit field reads %q, want wait it out", got)
	}
}

func TestEnterRefusesAGivenEffortTheChosenAgentDoesNotOffer(t *testing.T) {
	e := boardEnv(t)
	p := plan.Parse("", "docs/plans/PASS.md")
	cmd, f := settingsCommand(t, "--effort", "xhigh")
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	board := newSettingsBoard(context.Background(), e, p, g, &s, "pass", 3)

	key(board.board, '1')
	key(board.board, 'a')
	if s.Harness != "opencode" {
		t.Fatalf("the agent changed to %q", s.Harness)
	}
	board.board.Update(tea.KeyMsg{Type: tea.KeyEnter})
	board.read()
	if _, err := resolveSettings(e, *board.s); err == nil || !strings.Contains(err.Error(), "effort") {
		t.Fatalf("Enter with a given xhigh on opencode: %v", err)
	}
}
