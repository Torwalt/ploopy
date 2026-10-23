package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/review"
)

func boolValue(value bool) *bool        { return &value }
func floatValue(value float64) *float64 { return &value }

func TestAFileValuePrefillsTheBoardWithoutLockingIt(t *testing.T) {
	e := boardEnv(t)
	e.harnesses = allHarnesses()
	e.cfg.Run = config.Run{
		Agent: "opencode", Model: "custom/model", Effort: "max", Peak: "run",
		Subscriptions: "claude", Finish: "suspend", SpendAll: boolValue(true),
	}
	cmd, f := settingsCommand(t)
	p := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	board := newSettingsBoard(context.Background(), e, p, g, &s, "pass", 0)

	for _, want := range []struct {
		column int
		key    rune
	}{
		{0, 'a'}, {0, 'm'}, {0, 'e'}, {0, 'o'},
		{len(board.board.Columns) - 1, 's'}, {len(board.board.Columns) - 1, 'w'},
		{len(board.board.Columns) - 1, 'f'},
	} {
		field := fieldByKey(t, board.board.Columns[want.column], want.key)
		if field.Locked != "" {
			t.Fatalf("%c is locked with %q; a file value must stay editable", want.key, field.Locked)
		}
		if field.Source != "from .ploopy.toml" {
			t.Fatalf("%c shows source %q, want from .ploopy.toml", want.key, field.Source)
		}
	}
}

func TestAFileValueBeatsTheLastRun(t *testing.T) {
	e := boardEnv(t)
	e.harnesses = allHarnesses()
	e.cfg.Run = config.Run{
		Agent: "opencode", Model: "custom/model", Effort: "max", Peak: "run",
		Subscriptions: "none", Finish: "suspend", SpendAll: boolValue(true),
	}
	e.cfg.Push, e.cfg.PushSet = true, true
	cmd, f := settingsCommand(t)
	p := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, &runSettings{
		Harness: "claude", Model: "opus", Effort: "xhigh", Peak: "wait",
		Subscriptions: "all", SpendAll: false, Push: false, Finish: "none",
	})
	if s.Harness != "opencode" || s.Model != "custom/model" || s.Effort != "max" {
		t.Fatalf("agent %s %s %s, want the file's opencode custom/model max", s.Harness, s.Model, s.Effort)
	}
	if s.Peak != "run" || s.Subscriptions != "none" || !s.SpendAll || !s.Push || s.Finish != "suspend" {
		t.Fatalf("rate settings %+v, want the file's", s)
	}
}

func TestAFlagBeatsAFileValue(t *testing.T) {
	e := boardEnv(t)
	e.harnesses = allHarnesses()
	e.cfg.Run = config.Run{Agent: "opencode", Model: "file/model", Effort: "max"}
	cmd, f := settingsCommand(t, "--agent", "claude", "--model", "opus", "--effort", "high")
	p := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	if s.Harness != "claude" || s.Model != "opus" || s.Effort != "high" {
		t.Fatalf("agent %s %s %s, want the flag's claude opus high", s.Harness, s.Model, s.Effort)
	}
	if g.prefilled("harness") != "" || g.prefilled("model") != "" || g.prefilled("effort") != "" {
		t.Fatal("a flag must hide the file's prefill")
	}
}

func TestAnUnattendedRunStartsFromTheRunTable(t *testing.T) {
	e := boardEnv(t)
	e.harnesses = allHarnesses()
	e.cfg.Run = config.Run{Agent: "opencode", Effort: "max"}
	cmd, f := settingsCommand(t)
	p := plan.Parse(finishedPlan, "docs/plans/PASS.md")

	chosen, err := chooseSettings(context.Background(), cmd, e, p, f, "pass", 0)
	if err != nil {
		t.Fatal(err)
	}
	if chosen.agent.Harness.Name() != "opencode" || chosen.agent.Effort != "max" {
		t.Fatalf("unattended started with %s at %s, want the file's opencode at max",
			chosen.agent.Harness.Name(), chosen.agent.Effort)
	}
	if chosen.agent.Model != e.harnesses.Get("opencode").Models()[0] {
		t.Fatalf("unattended model %q, want the harness default", chosen.agent.Model)
	}
}

func TestTheReviewTableBeatsTheLastTriageAndFlagsBeatTheReviewTable(t *testing.T) {
	e, _ := worktreeRepo(t, nil)
	saveLastTriage(context.Background(), e.root, review.Triage{
		Agent: "claude", Model: "opus", Effort: "high", Budget: 5,
	})
	e.cfg.Review = config.Review{
		TriageAgent: "opencode", TriageModel: "deepseek/deepseek-flash", TriageEffort: "max",
		TriageBudget: floatValue(2),
	}
	cmd, f := triageCommand(t)
	got, sources, err := chooseTriageWithSources(context.Background(), cmd, e, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := review.Triage{Agent: "opencode", Model: "deepseek/deepseek-flash", Effort: "max", Budget: 2}
	if got != want {
		t.Fatalf("triage %+v, want the file's %+v", got, want)
	}
	for key, value := range map[string]string{
		"agent": "opencode", "model": "deepseek/deepseek-flash", "effort": "max", "budget": "2",
	} {
		if sources[key] != value {
			t.Fatalf("%s source value %q, want the file's %q", key, sources[key], value)
		}
	}

	cmd, f = triageCommand(t, "--triage-agent", "claude", "--triage-model", "sonnet", "--triage-budget", "7")
	got, sources, err = chooseTriageWithSources(context.Background(), cmd, e, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = review.Triage{Agent: "claude", Model: "sonnet", Effort: "high", Budget: 7}
	if got != want {
		t.Fatalf("triage %+v, want the flags' %+v", got, want)
	}
	if len(sources) != 0 {
		t.Fatalf("flags left file sources %v", sources)
	}
}

func TestTheTriageBoardShowsAFileValueAsPrefilled(t *testing.T) {
	e, _ := worktreeRepo(t, nil)
	e.cfg.Review = config.Review{TriageAgent: "opencode", TriageEffort: "max"}
	cmd, f := triageCommand(t)
	tri, givens, sources, ran, err := reviewTriage(context.Background(), cmd, e, f, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("a new review's triage must not be locked")
	}
	p := plan.Parse("---\nreviews: docs/plans/PASS.md\n---\n", "docs/plans/PASS_REVIEW.md")
	board := reviewBoardFrom(t, e, p, tri, givens, sources, "")
	_, column := columnByTitle(t, board.board, "Triage")
	for _, key := range []rune{'a', 'e'} {
		field := fieldByKey(t, column, key)
		if field.Locked != "" || field.Source != "from .ploopy.toml" {
			t.Fatalf("%c is locked %q with source %q", key, field.Locked, field.Source)
		}
	}
}

func TestAFileSourceLeavesWhenTheRunFieldChanges(t *testing.T) {
	e := boardEnv(t)
	e.harnesses = allHarnesses()
	e.cfg.Run = config.Run{Peak: "run"}
	cmd, f := settingsCommand(t)
	p := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	board := newSettingsBoard(context.Background(), e, p, g, &s, "pass", 0)
	peakSource := func() string { return fieldByKey(t, board.board.Columns[0], 'o').Source }

	if got := peakSource(); got != "from .ploopy.toml" {
		t.Fatalf("the file's peak shows source %q", got)
	}
	key(board.board, '1')
	key(board.board, 'o')
	if got := peakSource(); got != "" {
		t.Fatalf("a changed peak still shows source %q", got)
	}
	key(board.board, 'o')
	if got := peakSource(); got != "from .ploopy.toml" {
		t.Fatalf("a peak back at the file's value shows source %q", got)
	}
}

func TestAFileSourceLeavesWhenTheRunColumnFieldChanges(t *testing.T) {
	e := boardEnv(t)
	e.harnesses = allHarnesses()
	e.cfg.Run = config.Run{Finish: "suspend"}
	cmd, f := settingsCommand(t)
	p := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	board := newSettingsBoard(context.Background(), e, p, g, &s, "pass", 0)
	runAt, _ := columnByTitle(t, board.board, "Run")
	finishSource := func() string {
		_, run := columnByTitle(t, board.board, "Run")
		return fieldByKey(t, run, 'f').Source
	}

	if got := finishSource(); got != "from .ploopy.toml" {
		t.Fatalf("the file's finish shows source %q", got)
	}
	key(board.board, rune('1'+runAt))
	for i := 0; i < 2; i++ {
		key(board.board, 'f')
		if got := finishSource(); got != "" {
			t.Fatalf("a changed finish still shows source %q", got)
		}
	}
	key(board.board, 'f')
	if got := finishSource(); got != "from .ploopy.toml" {
		t.Fatalf("a finish back at the file's value shows source %q", got)
	}
}

func TestAFileSourceLeavesWhenTheAutomationFieldChanges(t *testing.T) {
	e := boardEnv(t)
	e.harnesses = allHarnesses()
	e.cfg.Automation = config.Automation{FollowUp: boolValue(true)}
	cmd, f := settingsCommand(t)
	p := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	board := newSettingsBoard(context.Background(), e, p, g, &s, "pass", 0)
	at, _ := columnByTitle(t, board.board, "Automation")
	followSource := func() string {
		_, automation := columnByTitle(t, board.board, "Automation")
		return fieldByKey(t, automation, 'n').Source
	}

	if got := followSource(); got != "from .ploopy.toml" {
		t.Fatalf("the file's follow-up shows source %q", got)
	}
	key(board.board, rune('1'+at))
	key(board.board, 'n')
	if got := followSource(); got != "" {
		t.Fatalf("a changed follow-up still shows source %q", got)
	}
	key(board.board, 'n')
	if got := followSource(); got != "from .ploopy.toml" {
		t.Fatalf("follow-up back at the file's value shows source %q", got)
	}
}

func TestAFileSourceLeavesWhenTheTriageAgentChanges(t *testing.T) {
	e, _ := worktreeRepo(t, nil)
	e.cfg.Review = config.Review{
		TriageAgent: "opencode", TriageModel: "deepseek/deepseek-flash", TriageEffort: "max",
	}
	cmd, f := triageCommand(t)
	tri, givens, sources, ran, err := reviewTriage(context.Background(), cmd, e, f, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("a new review's triage must not be locked")
	}
	p := plan.Parse("---\nreviews: docs/plans/PASS.md\n---\n", "docs/plans/PASS_REVIEW.md")
	board := reviewBoardFrom(t, e, p, tri, givens, sources, "")
	at, _ := columnByTitle(t, board.board, "Triage")
	source := func(key rune) string {
		_, column := columnByTitle(t, board.board, "Triage")
		return fieldByKey(t, column, key).Source
	}

	if got := source('a'); got != "from .ploopy.toml" {
		t.Fatalf("the file's triage agent shows source %q", got)
	}
	key(board.board, rune('1'+at))
	key(board.board, 'A')
	if got := source('a'); got != "" {
		t.Fatalf("a changed triage agent still shows source %q", got)
	}
	if got := source('m'); got != "" {
		t.Fatalf("a normalized triage model still shows source %q", got)
	}
	if got := source('e'); got != "" {
		t.Fatalf("a normalized triage effort still shows source %q", got)
	}
	key(board.board, 'a')
	if got := source('a'); got != "from .ploopy.toml" {
		t.Fatalf("a triage agent back at the file's value shows source %q", got)
	}
}

func TestTheAutomationTableSetsFollowUpWithoutLockingIt(t *testing.T) {
	e := boardEnv(t)
	e.harnesses = allHarnesses()
	e.cfg.Automation = config.Automation{FollowUp: boolValue(true)}
	cmd, f := settingsCommand(t)
	p := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	if !s.FollowUp {
		t.Fatal("the file's follow_up did not reach the run")
	}
	if g.why("followUp") != "" || g.prefilled("followUp") != "from .ploopy.toml" {
		t.Fatalf("follow-up is locked with %q and prefilled from %q", g.why("followUp"), g.prefilled("followUp"))
	}
	fill(e, &s, g, &runSettings{Harness: "claude", FollowUp: false})
	if !s.FollowUp {
		t.Fatal("the last run beat the file's follow_up")
	}
	board := newSettingsBoard(context.Background(), e, p, g, &s, "pass", 0)
	_, automation := columnByTitle(t, board.board, "Automation")
	field := fieldByKey(t, automation, 'n')
	if got := *field.Value; got != "yes" {
		t.Fatalf("the board shows follow-up %q, want yes", got)
	}
	if field.Locked != "" || field.Source != "from .ploopy.toml" {
		t.Fatalf("follow-up is locked %q with source %q", field.Locked, field.Source)
	}

	cmd, f = settingsCommand(t, "--follow-up=false")
	s, g, err = givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	if s.FollowUp {
		t.Fatal("the flag did not beat the file's follow_up")
	}
	if g.why("followUp") != "from a flag" {
		t.Fatalf("the flag source is %q", g.why("followUp"))
	}
}

func TestTheRescueComesFromTheFileAFlagOrTheLastRun(t *testing.T) {
	e := boardEnv(t)
	e.harnesses = allHarnesses()
	p := plan.Parse(finishedPlan, "docs/plans/PASS.md")

	e.cfg.Automation = config.Automation{Rescue: "claude", RescueModel: "opus", RescueEffort: "high"}
	cmd, f := settingsCommand(t)
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	chosen, err := resolveSettings(e, s)
	if err != nil {
		t.Fatal(err)
	}
	if chosen.rescue == nil || chosen.rescue.Label() != "claude opus high" {
		t.Fatalf("the file's rescue reached the run as %+v", chosen.rescue)
	}
	if g.why("rescue") != "" || g.prefilled("rescue") != "from .ploopy.toml" {
		t.Fatalf("the file's rescue is locked with %q and prefilled from %q", g.why("rescue"), g.prefilled("rescue"))
	}

	cmd, f = settingsCommand(t, "--rescue", "opencode", "--rescue-model", "deepseek/deepseek-flash", "--rescue-effort", "max")
	s, g, err = givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	chosen, err = resolveSettings(e, s)
	if err != nil {
		t.Fatal(err)
	}
	if chosen.rescue == nil || chosen.rescue.Label() != "opencode deepseek/deepseek-flash max" {
		t.Fatalf("the flag's rescue reached the run as %+v", chosen.rescue)
	}
	if g.why("rescue") != "from a flag" {
		t.Fatalf("the rescue flag source is %q", g.why("rescue"))
	}

	e.cfg.Automation = config.Automation{}
	cmd, f = settingsCommand(t)
	s, g, err = givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, &runSettings{
		Harness: "claude",
		Rescue:  &control.Agent{Harness: "codex", Model: "gpt-6.1-sol", Effort: "xhigh"},
	})
	chosen, err = resolveSettings(e, s)
	if err != nil {
		t.Fatal(err)
	}
	if chosen.rescue == nil || chosen.rescue.Label() != "codex gpt-6.1-sol xhigh" {
		t.Fatalf("the last run's rescue reached the run as %+v", chosen.rescue)
	}
}

func TestTheChosenRescueReachesTheLoopOptions(t *testing.T) {
	e := boardEnv(t)
	e.harnesses = allHarnesses()
	p := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	_, f := settingsCommand(t)

	rescue := &loop.Agent{Harness: e.harnesses.Get("claude"), Model: "opus", Effort: "high"}
	opts := runOptions(context.Background(), e, f, p, e.cfg, skills{}, chosenRun{rescue: rescue}, "", "")
	if opts.Rescue != rescue {
		t.Fatalf("the run's options carry rescue %+v, want %+v", opts.Rescue, rescue)
	}

	opts = runOptions(context.Background(), e, f, p, e.cfg, skills{}, chosenRun{}, "", "")
	if opts.Rescue != nil {
		t.Fatalf("a run without a rescue carries %+v", opts.Rescue)
	}
}

func TestAChainedPlanKeepsTheRescue(t *testing.T) {
	e := boardEnv(t)
	e.harnesses = allHarnesses()
	rescue := &loop.Agent{Harness: e.harnesses.Get("claude"), Model: "opus", Effort: "high"}
	chosen := chosenRun{rescue: rescue}

	changed := overChosen(e, chosen, control.Settings{Rescue: &control.Agent{Harness: "codex", Model: "gpt-6.1-sol", Effort: "high"}})
	if changed.rescue == nil || changed.rescue.Harness.Name() != "codex" {
		t.Fatalf("the adjusted rescue is %+v", changed.rescue)
	}
	kept := overChosen(e, changed, control.Settings{})
	if kept.rescue == nil || kept.rescue != changed.rescue {
		t.Fatalf("an unset change cleared the rescue: %+v", kept.rescue)
	}

	p := plan.Parse("---\nagent: opencode\n---\n", "docs/plans/PASS_REVIEW.md")
	next, err := applyPlanSettings(e, p, kept)
	if err != nil {
		t.Fatal(err)
	}
	if next.rescue == nil || next.rescue != kept.rescue {
		t.Fatalf("the next plan lost the rescue: %+v", next.rescue)
	}
}

func TestAFileEffortIsCheckedAgainstTheHarnessTheRunSettlesOn(t *testing.T) {
	for _, tc := range []struct {
		name string
		last *runSettings
	}{
		{"the last run", &runSettings{Harness: "claude", Model: "sonnet", Effort: "high"}},
		{"the default", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := worktreeRepo(t, nil)
			e.cfg.Run = config.Run{Effort: "max"}
			if tc.last != nil {
				saveLast(context.Background(), e.root, *tc.last)
			}
			cmd, f := settingsCommand(t)
			p := plan.Parse(finishedPlan, "docs/plans/PASS.md")
			if _, _, err := givenSettings(cmd, e, p, f); err == nil ||
				!strings.Contains(err.Error(), "[run] effort") {
				t.Fatalf("[run] effort max with no [run] agent: %v", err)
			}
		})
	}
}
