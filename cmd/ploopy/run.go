package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

type runFlags struct {
	planName     string
	all          bool
	agent        string
	effort       string
	model        string
	from         string
	until        string
	units        int
	retries      int
	blockRetries int
	timeout      time.Duration
	backoff      time.Duration
	tests        bool
	noTests      bool
	verify       string
	testCmd      string
	context      string
	notify       string
	allowDirty   bool
	dryRun       bool
	finish       string
	budget       float64
	fallback     string
	peak         string
	quiet        bool
	grace        time.Duration
}

func newRun(e *env) *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run a plan's open units (the default command)",
		Long: "Run a plan's open units, one fresh agent session each.\n\n" +
			"With nothing passed, ploopy asks for what it needs. Every choice is\n" +
			"also a flag, for a run nobody is watching.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runPlan(cmd.Context(), e, &f) },
	}

	flags := cmd.Flags()
	flags.StringVar(&f.planName, "plan", "", "plan path or name; asked for when left out")
	flags.BoolVar(&f.all, "all", false, "also offer finished plans")
	flags.StringVar(&f.agent, "agent", "", "harness to drive; asked for when left out")
	flags.StringVar(&f.effort, "effort", "", "effort level for the harness")
	flags.StringVar(&f.model, "model", "", "model for the harness")
	flags.StringVar(&f.from, "from", "", "start at unit ID")
	flags.StringVar(&f.until, "until", "", "stop after unit ID")
	flags.IntVar(&f.units, "units", 0, "run at most N units")
	flags.IntVar(&f.retries, "retries", 1, "retries per unit after a failed check")
	flags.IntVar(&f.blockRetries, "block-retries", 1,
		"second opinions before a BLOCKED report stops the run")
	flags.DurationVar(&f.timeout, "timeout", 90*time.Minute, "wall clock per session")
	flags.DurationVar(&f.backoff, "backoff", 2*time.Minute, "pause after a session that changed nothing")
	flags.BoolVar(&f.tests, "test", false, "run the test command after every unit")
	flags.BoolVar(&f.noTests, "no-test", false, "never run the test command")
	flags.StringVar(&f.verify, "verify", "", "command run after every unit, overriding the settings")
	flags.StringVar(&f.testCmd, "test-cmd", "", "command run when a unit wants tests")
	flags.StringVar(&f.context, "context", "", `documents every session reads first, e.g. "A.md B.md"`)
	flags.StringVar(&f.notify, "notify", "", "command run when the loop stops")
	flags.BoolVar(&f.allowDirty, "allow-dirty", false, "hand a dirty working tree to the first session")
	flags.BoolVar(&f.dryRun, "dry-run", false, "print the first session's prompt and stop")
	flags.StringVar(&f.finish, "finish", "", "when the run ends: none, suspend or poweroff")
	flags.Float64Var(&f.budget, "budget", 0, "spending cap per session, in dollars")
	flags.StringVar(&f.fallback, "fallback", "", "comma-separated models to fall back to")
	flags.StringVar(&f.peak, "peak", "", "a unit due in the harness's peak hours: wait or run")
	flags.BoolVar(&f.quiet, "quiet", false, "report decisions only, without the session feed")
	flags.DurationVar(&f.grace, "grace", time.Minute, "countdown before the end action")
	return cmd
}

func runPlan(ctx context.Context, e *env, f *runFlags) error {
	p, err := choosePlan(e, f)
	if err != nil {
		return err
	}
	if errs := lintReport(e, p, os.Stderr, false); len(errs) > 0 {
		return fmt.Errorf("%s does not lint; fix it before running it", p.Path)
	}

	chosen, err := chooseHarness(e, p, f)
	if err != nil {
		return err
	}
	waitOffPeak, err := choosePeak(chosen.harness, f)
	if err != nil {
		return err
	}
	finish, err := chooseFinish(f)
	if err != nil {
		return err
	}

	skill, err := e.skill()
	if err != nil {
		return err
	}
	verify, test := f.verify, f.testCmd
	if verify == "" {
		verify = e.cfg.Verify
	}
	if test == "" {
		test = e.cfg.Test
	}
	if f.tests && test == "" {
		return fmt.Errorf("--test needs a test command: set `test` in .ploopy.toml or pass --test-cmd")
	}

	opts := loop.Options{
		PlanPath:       p.Path,
		Harness:        chosen.harness,
		Model:          chosen.model,
		Effort:         chosen.effort,
		Start:          f.from,
		Until:          f.until,
		MaxUnits:       f.units,
		Retries:        f.retries,
		BlockRetries:   f.blockRetries,
		Timeout:        f.timeout,
		Backoff:        f.backoff,
		Tests:          testsOverride(f),
		Context:        strings.Fields(f.context),
		BaseContext:    e.cfg.Context,
		Skill:          skill,
		VerifyCmd:      verify,
		TestCmd:        test,
		AuthorPaths:    e.cfg.AuthorPaths,
		StateCommit:    e.cfg.StateCommit,
		Notify:         f.notify,
		AllowDirty:     f.allowDirty,
		HarnessArgs:    e.extra,
		MaxBudgetUSD:   f.budget,
		FallbackModels: splitList(f.fallback),
		WaitOffPeak:    waitOffPeak,
	}

	report := ui.NewReporter(os.Stdout, os.Stderr)
	report.Quiet = f.quiet
	runner, err := loop.New(e.root, opts, report)
	if err != nil {
		return err
	}

	if f.dryRun {
		return dryRun(e, runner, p, f)
	}

	report.Say("%s with %s%s at %s effort%s%s", p.Path, chosen.harness.Name(),
		modelSuffix(chosen.model), chosen.effort, peakSuffix(waitOffPeak), finishSuffix(finish))

	release := ui.Inhibit(ctx, "ploopy is running "+p.Path)
	result := runner.Run(ctx)
	release()

	if err := finish.Run(os.Stdout, f.grace); err != nil {
		fmt.Fprintln(os.Stderr, "ploopy: the end action failed:", err)
	}
	if result.Status != "done" {
		return fmt.Errorf("%s", result.Message)
	}
	return nil
}

func dryRun(e *env, runner *loop.Loop, p *plan.Plan, f *runFlags) error {
	s, err := e.state(p)
	if err != nil {
		return err
	}
	unit := state.Resume(p, s)
	if f.from != "" {
		unit = p.Unit(f.from)
	}
	if unit == nil {
		fmt.Printf("%s is complete\n", p.Path)
		return nil
	}
	prompt, err := runner.Prompt(p, *unit, "")
	if err != nil {
		return err
	}
	fmt.Print(prompt)
	return nil
}

func testsOverride(f *runFlags) *bool {
	switch {
	case f.tests:
		yes := true
		return &yes
	case f.noTests:
		no := false
		return &no
	}
	return nil
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func modelSuffix(model string) string {
	if model == "" {
		return ""
	}
	return " (" + model + ")"
}

func peakSuffix(waitOffPeak bool) string {
	if !waitOffPeak {
		return ""
	}
	return ", off-peak only"
}

func finishSuffix(finish ui.Finish) string {
	if finish == ui.FinishNone || finish == "" {
		return ""
	}
	return ", then " + string(finish)
}

// choosePlan takes the plan from the flag, or offers the open ones.
func choosePlan(e *env, f *runFlags) (*plan.Plan, error) {
	if f.planName != "" {
		return e.resolve(f.planName)
	}

	plans, err := e.discover()
	if err != nil {
		return nil, err
	}
	var choices []ui.Choice
	byPath := map[string]*plan.Plan{}
	for _, p := range plans {
		s, err := e.state(p)
		if err != nil {
			return nil, err
		}
		done, total := progressOf(p, s)
		if done == total && !f.all {
			continue
		}
		label := fmt.Sprintf("%-44s %2d/%-2d", p.Path, done, total)
		if next := state.Resume(p, s); next != nil {
			label += "  next " + next.ID + " " + next.Title
		}
		choices = append(choices, ui.Choice{Label: label, Value: p.Path})
		byPath[p.Path] = p
	}
	if len(choices) == 0 {
		return nil, fmt.Errorf("no open plan under %s; --all offers finished ones", e.cfg.Plans)
	}

	picked, err := ui.Pick("Which plan?", "plan", choices)
	if err != nil {
		return nil, err
	}
	return byPath[picked], nil
}

type harnessChoice struct {
	harness harness.Harness
	model   string
	effort  string
}

// chooseHarness takes what the flags say, then what the plan's front matter
// says, then asks.
func chooseHarness(e *env, p *plan.Plan, f *runFlags) (harnessChoice, error) {
	name := f.agent
	if name == "" {
		name = p.Settings["agent"]
	}
	if name == "" {
		choices := make([]ui.Choice, 0, len(e.harnesses))
		for _, h := range e.harnesses {
			choices = append(choices, ui.Choice{Label: h.Name(), Value: h.Name()})
		}
		picked, err := ui.Pick("Which harness?", "agent", choices)
		if err != nil {
			return harnessChoice{}, err
		}
		name = picked
	}

	chosen := e.harnesses.Get(name)
	if chosen == nil {
		return harnessChoice{}, fmt.Errorf("--agent must be one of %s, not %s",
			strings.Join(e.harnesses.Names(), ", "), name)
	}
	// Front matter only answers for the harness it names.
	fromPlan := p.Settings["agent"] == name

	model := f.model
	if model == "" && fromPlan {
		model = p.Settings["model"]
	}
	if model == "" {
		// A harness offers its default first, so a run nobody is watching
		// needs no model named.
		if !ui.Interactive() {
			model = chosen.Models()[0]
		} else {
			picked, err := ui.Pick("Which model?", "model", labelled(chosen.Models()))
			if err != nil {
				return harnessChoice{}, err
			}
			model = picked
		}
	}

	effort := f.effort
	if effort == "" && fromPlan {
		effort = p.Settings["effort"]
	}
	if effort == "" {
		picked, err := ui.Pick("Which effort?", "effort", labelled(chosen.Efforts()))
		if err != nil {
			return harnessChoice{}, err
		}
		effort = picked
	}
	if !contains(chosen.Efforts(), effort) {
		return harnessChoice{}, fmt.Errorf("--effort for %s must be one of %s",
			chosen.Name(), strings.Join(chosen.Efforts(), ", "))
	}
	return harnessChoice{harness: chosen, model: model, effort: effort}, nil
}

// choosePeak settles, before the run, what a unit due to start in the
// harness's peak hours does. A run nobody is watching runs through them.
func choosePeak(h harness.Harness, f *runFlags) (bool, error) {
	switch f.peak {
	case "wait":
		return true, nil
	case "run":
		return false, nil
	case "":
	default:
		return false, fmt.Errorf("--peak must be wait or run, not %s", f.peak)
	}
	windows := h.PeakWindows()
	if len(windows) == 0 || !ui.Interactive() {
		return false, nil
	}
	title := fmt.Sprintf("%s bills double %s. A unit due to start then?",
		h.Name(), peakHours(windows, time.Now()))
	picked, err := ui.Pick(title, "peak", []ui.Choice{
		{Label: "wait for off-peak", Value: "wait"},
		{Label: "run anyway", Value: "run"},
	})
	if err != nil {
		return false, err
	}
	return picked == "wait", nil
}

// peakHours renders the windows in local time, today's offset applied. The
// days stay UTC days.
func peakHours(windows []harness.Window, now time.Time) string {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	clock := func(minutes int) string {
		return midnight.Add(time.Duration(minutes) * time.Minute).In(now.Location()).Format("15:04")
	}
	var parts []string
	last := ""
	for _, w := range windows {
		span := clock(w.Start) + "–" + clock(w.End)
		if days := weekdays(w.Days); days != last || len(parts) == 0 {
			parts = append(parts, days+" "+span)
			last = days
		} else {
			parts[len(parts)-1] += ", " + span
		}
	}
	return strings.Join(parts, "; ")
}

// weekdays names a set of days, a consecutive run as a range.
func weekdays(days []time.Weekday) string {
	names := make([]string, 0, len(days))
	for _, day := range days {
		names = append(names, day.String()[:3])
	}
	consecutive := len(days) > 2
	for i := 1; i < len(days); i++ {
		consecutive = consecutive && days[i] == days[i-1]+1
	}
	if consecutive {
		return names[0] + "–" + names[len(names)-1]
	}
	return strings.Join(names, ", ")
}

func chooseFinish(f *runFlags) (ui.Finish, error) {
	if f.finish != "" {
		finish := ui.Finish(f.finish)
		if !finish.Valid() {
			return "", fmt.Errorf("--finish must be none, suspend or poweroff, not %s", f.finish)
		}
		return finish, nil
	}
	if !ui.Interactive() {
		return ui.FinishNone, nil
	}
	picked, err := ui.Pick("When the run ends?", "finish", ui.Finishes)
	if err != nil {
		return "", err
	}
	return ui.Finish(picked), nil
}

func labelled(values []string) []ui.Choice {
	choices := make([]ui.Choice, 0, len(values))
	for _, value := range values {
		choices = append(choices, ui.Choice{Label: value, Value: value})
	}
	return choices
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
