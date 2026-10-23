package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
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

	run, _ := columnByTitle(t, board.board, "Run")
	focusRun := func() { board.board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune('1' + run)}}) }
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

// pushableRepo makes e.root a checkout whose pass branch has somewhere to go,
// so the adjust board offers its push field.
func pushableRepo(t *testing.T, e *env) {
	t.Helper()
	git(t, e.root, "init", "-q", "-b", "master")
	git(t, e.root, "config", "user.name", "Test")
	git(t, e.root, "config", "user.email", "test@example.com")
	write(t, e.root, "README.md", "start\n")
	git(t, e.root, "add", "-A")
	git(t, e.root, "commit", "-q", "-m", "first")
	git(t, e.root, "branch", "pass")
	remote := t.TempDir()
	git(t, remote, "init", "-q", "--bare")
	git(t, e.root, "remote", "add", "origin", remote)
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
	for _, want := range []struct {
		key   rune
		value string
	}{
		{'a', "opencode"},
		{'m', "deepseek/deepseek-flash"},
		{'e', "high"},
	} {
		if got := *fieldByKey(t, board.board.Columns[0], want.key).Value; got != want.value {
			t.Fatalf("the bedrock's %c is %q, want %q from the live run", want.key, got, want.value)
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

func TestTheAdjustBoardOffersFollowUpOnAReviewRun(t *testing.T) {
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

	if run := board.board.Columns[len(board.board.Columns)-1]; run.Title != "Run" {
		t.Fatalf("the last column is %q", run.Title)
	}
	at, automation := columnByTitle(t, board.board, "Automation")
	if got := *fieldByKey(t, automation, 'n').Value; got != "no" {
		t.Fatalf("follow-up starts as %q, want no", got)
	}

	if err := board.applyBoard(live, time.Now(), func() error {
		key(board.board, rune('1'+at))
		key(board.board, 'n')
		board.board.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	lives, err := control.List()
	if err != nil || len(lives) != 1 {
		t.Fatalf("listed %v, %v", lives, err)
	}
	if got := lives[0].Wanted.FollowUp; got == nil || !*got {
		t.Fatalf("follow-up applied as %v, want on", got)
	}
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
	pushableRepo(t, e)
	live := liveRun(t, e, control.Settings{Subscriptions: "all"})
	board := newAdjustBoard(context.Background(), e, live)

	now := time.Now()
	runAt, _ := columnByTitle(t, board.board, "Run")
	err := board.applyBoard(live, now, func() error {
		// Column 1 is the bedrock, the next claude, and Run after Automation.
		key(board.board, '1')
		key(board.board, 'l')
		key(board.board, rune('1'+runAt))
		key(board.board, 's')
		key(board.board, 'f')
		key(board.board, 'x')
		key(board.board, 'w')
		key(board.board, 'p')
		key(board.board, '2')
		key(board.board, 'u')
		board.board.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return nil
	})
	if err != nil {
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
	if !wanted.Pushes() {
		t.Fatalf("push applied as %v", wanted.Push)
	}
	if agent := wanted.SecondaryAgent(); agent == nil || agent.Harness != "claude" {
		t.Fatalf("the limit agent applied as %+v", wanted.Secondary)
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

func TestTheRunBoardWritesTheLeasesItChanged(t *testing.T) {
	e := boardEnv(t)
	t.Setenv("PATH", t.TempDir())
	e.harnesses = allHarnesses()
	unchanged := lease.Lease{Harness: "codex", Model: "gpt-6.1-sol", Effort: "high", Kind: lease.Open, Keep: 0}
	if err := lease.Save(lease.File{Leases: []lease.Lease{unchanged}}); err != nil {
		t.Fatal(err)
	}

	p := plan.Parse("", "docs/plans/PASS.md")
	cmd, f := settingsCommand(t, "--agent", "opencode", "--effort", "high", "--model", "deepseek/deepseek-flash")
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	board := newSettingsBoard(context.Background(), e, p, g, &s, "pass", 3)

	if _, err := board.start(func() error {
		// Column 2 is claude; a subscription's term, model and keep edit
		// its draft lease.
		key(board.board, '2')
		key(board.board, 'u')
		key(board.board, 'm')
		key(board.board, 'k')
		board.board.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	file, err := lease.Load()
	if err != nil {
		t.Fatal(err)
	}
	byHarness := map[string]lease.Lease{}
	for _, l := range file.Leases {
		byHarness[l.Harness] = l
	}
	claude, ok := byHarness["claude"]
	if !ok {
		t.Fatalf("the edited lease was not written: %+v", file.Leases)
	}
	if claude.Kind != lease.Until || claude.Model != "opus" || claude.Keep != 30 {
		t.Fatalf("the edited lease landed as %+v", claude)
	}
	if got := byHarness["codex"]; got != unchanged {
		t.Fatalf("the unchanged lease became %+v, want %+v", got, unchanged)
	}
}

func TestTheRunBoardWritesNoLeaseOnCancel(t *testing.T) {
	e := boardEnv(t)
	t.Setenv("PATH", t.TempDir())
	e.harnesses = allHarnesses()

	p := plan.Parse("", "docs/plans/PASS.md")
	cmd, f := settingsCommand(t, "--agent", "opencode", "--effort", "high", "--model", "deepseek/deepseek-flash")
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	board := newSettingsBoard(context.Background(), e, p, g, &s, "pass", 3)

	_, err = board.start(func() error {
		key(board.board, '2')
		key(board.board, 'u')
		board.board.Update(tea.KeyMsg{Type: tea.KeyEsc})
		return huh.ErrUserAborted
	})
	if !errors.Is(err, huh.ErrUserAborted) {
		t.Fatalf("cancel returned %v", err)
	}
	file, err := lease.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Leases) != 0 {
		t.Fatalf("cancel wrote leases %+v", file.Leases)
	}
}

func TestFollowUpIsAnAdjustFlagAndThenIsItsDeprecatedAlias(t *testing.T) {
	e := boardEnv(t)
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"--follow-up=false", []string{"--follow-up=false"}, false},
		{"--then fixes", []string{"--then", "fixes"}, true},
		{"--then stop", []string{"--then", "stop"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &adjustFlags{}
			cmd := &cobra.Command{Use: "adjust"}
			cmd.Flags().BoolVar(&f.followUp, "follow-up", false, "")
			cmd.Flags().StringVar(&f.then, "then", "", "")
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			change, err := changeFromFlags(cmd, e, f)
			if err != nil || change.FollowUp == nil || *change.FollowUp != tc.want {
				t.Fatalf("%s changed %+v, error %v", tc.name, change, err)
			}
		})
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
	}, "pass", 3, tri, map[string]bool{"model": true}, nil, "")

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
