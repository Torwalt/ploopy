package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/catalog"
	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
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
	worktree     bool
	dryRun       bool
	finish       string
	push         bool
	budget       float64
	secondary    string
	secondaryMod string
	secondaryEff string
	peak         string
	quiet        bool
	grace        time.Duration
	away         string
	night        string
	reserve      int
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
		RunE: func(cmd *cobra.Command, _ []string) error { return runPlan(cmd, e, &f) },
	}
	addRunFlags(cmd, &f)
	return cmd
}

// addRunFlags is every setting of a run, as flags.
func addRunFlags(cmd *cobra.Command, f *runFlags) {
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
	const allowDirty = "allow-dirty"
	flags.BoolVar(&f.allowDirty, allowDirty, false, "")
	_ = flags.MarkDeprecated(allowDirty, "uncommitted changes always go to the session")
	flags.BoolVar(&f.worktree, "worktree", false, "")
	_ = flags.MarkDeprecated("worktree", "every run goes to a worktree of its branch")
	flags.BoolVar(&f.dryRun, "dry-run", false, "print the first session's prompt and stop")
	flags.StringVar(&f.finish, "finish", "", "when the run ends: none, suspend or poweroff")
	flags.BoolVar(&f.push, "push", false, "push the branch when the run ends, before the end action")
	flags.Float64Var(&f.budget, "budget", 0, "spending cap per session, in dollars")
	flags.StringVar(&f.secondary, "secondary", "",
		"harness that takes over when the agent hits a usage limit, or none")
	flags.StringVar(&f.secondaryMod, "secondary-model", "", "model for the secondary harness")
	flags.StringVar(&f.secondaryEff, "secondary-effort", "", "effort for the secondary harness")
	flags.StringVar(&f.peak, "peak", "", "a unit due in the harness's peak hours: wait or run")
	flags.BoolVar(&f.quiet, "quiet", false, "report decisions only, without the session feed")
	flags.DurationVar(&f.grace, "grace", time.Minute, "countdown before the end action")
	flags.StringVar(&f.away, "away", "", "when you leave Claude and Codex to the run, such as 21:00-09:00, or none")
	flags.StringVar(&f.night, "night", "",
		"subscriptions that take units while you are away and the fresh 5h windows each may open, such as claude=2,codex/gpt-6.1-sol/high=1")
	flags.IntVar(&f.reserve, "weekly-reserve", defaultReserve,
		"percent of every weekly window the run leaves to you while you are away")
}

func runPlan(cmd *cobra.Command, e *env, f *runFlags) error {
	ctx := cmd.Context()
	c, err := choosePlan(ctx, e, f)
	if err != nil {
		return err
	}
	if c.Live != nil {
		if !ui.Interactive() {
			return fmt.Errorf("%s is already running in %s; change it with `ploopy adjust`", c.Plan.Path, c.Live.Root)
		}
		fmt.Printf("%s is already running in %s.\n", c.Plan.Path, c.Live.Root)
		return adjustInteractively(e, *c.Live)
	}
	t, err := prepareTarget(ctx, e, f, c)
	if err != nil {
		return err
	}
	p, cfg := c.Plan, t.cfg
	if errs := lintReport(e, t.root, p, os.Stderr, false); len(errs) > 0 {
		return fmt.Errorf("%s does not lint; fix it before running it", p.Path)
	}

	chosen, err := chooseSettings(ctx, cmd, e, p, f, t.branch, openUnits(p, c.State))
	if err != nil {
		return err
	}

	skill, err := skillsAt(t.root, cfg)
	if err != nil {
		return err
	}
	verify, test := f.verify, f.testCmd
	if verify == "" {
		verify = cfg.Verify
	}
	if test == "" {
		test = cfg.Test
	}
	if f.tests && test == "" {
		return fmt.Errorf("--test needs a test command: set `test` in .ploopy.toml or pass --test-cmd")
	}

	opts := loop.Options{
		PlanPath:     p.Path,
		Harness:      chosen.agent.Harness,
		Model:        chosen.agent.Model,
		Effort:       chosen.agent.Effort,
		Start:        f.from,
		Until:        f.until,
		MaxUnits:     f.units,
		Retries:      f.retries,
		BlockRetries: f.blockRetries,
		Timeout:      f.timeout,
		Backoff:      f.backoff,
		Tests:        testsOverride(f),
		Context:      strings.Fields(f.context),
		BaseContext:  cfg.Context,
		Skill:        skill.unit,
		ReviewSkill:  skill.review,
		TriageSkill:  skill.triage,
		Checklist:    skill.checklist,
		Harnesses:    e.harnesses,
		VerifyCmd:    verify,
		TestCmd:      test,
		AuthorPaths:  cfg.AuthorPaths,
		StateCommit:  cfg.StateCommit,
		Notify:       f.notify,
		HarnessArgs:  e.extra,
		MaxBudgetUSD: f.budget,
		Secondary:    chosen.secondary,
		WaitOffPeak:  chosen.waitOffPeak,
		Night:        chosen.nightly,
		History:      historyPath(ctx, e.root),
	}

	report := ui.NewReporter(os.Stdout, os.Stderr)
	report.Quiet = f.quiet
	if f.dryRun {
		runner, err := loop.New(t.root, opts, report)
		if err != nil {
			return err
		}
		return dryRun(ctx, runner, c, f)
	}

	root := t.root
	if !samePath(root, e.root) {
		report.Say("%s runs in %s, where %s is checked out", p.Path, root, t.branch)
	}
	if t.fresh {
		opts.SetupCmd = cfg.Setup
	}
	branch, _ := repo.New(root, nil).Branch(ctx)
	opts.Branch = branch
	steer := announce(control.Status{
		Root: root, Plan: p.Path, Branch: branch,
		Agent: chosen.agent.Label(),
		Settings: control.Settings{
			Finish: string(chosen.finish), Peak: peakName(chosen.waitOffPeak), Push: &chosen.push,
			Secondary: toControlAgent(chosen.secondary),
			Away:      chosen.away, Night: chosen.night, Reserve: chosen.reserve,
		},
	}, e.harnesses, report)
	defer steer.close()
	opts.Steering = steer.loopSteering()

	runner, err := loop.New(root, opts, tracked{Reporter: report, steer: steer})
	if err != nil {
		return err
	}

	report.Say("%s with %s%s at %s effort%s%s%s%s%s", p.Path, chosen.agent.Harness.Name(),
		modelSuffix(chosen.agent.Model), chosen.agent.Effort, secondarySuffix(chosen.secondary),
		nightSuffix(chosen.away, chosen.night), peakSuffix(chosen), pushSuffix(chosen.push), finishSuffix(chosen.finish))
	if steer.run != nil {
		report.Say("change what happens next from another terminal with `ploopy adjust`")
	}

	release := ui.Inhibit(ctx, "ploopy is running "+p.Path)
	result := runner.Run(ctx)
	release()

	complete := false
	if s, err := state.Load(filepath.Join(root, state.PathFor(p.Path))); err == nil {
		complete = loop.Complete(p, s)
		if complete {
			fmt.Println()
			fmt.Print(reportOf(catalog.Copy{Plan: p, State: s, Root: root}))
			if p.Reviews() != "" {
				reviewOutcome(root, p)
			}
		} else {
			report.Summary(p, s, &result.Stats)
		}
	}
	report.Degraded(result.Stats.Degraded)

	wanted := steer.wanted()
	if wanted.Pushes() {
		pushBranch(ctx, root, runner, report)
	}
	// Only a worktree ploopy made is removed: one the author made is theirs,
	// and a run started inside a worktree has its shell there.
	if branch != "" && e.ours(ctx, root) {
		leaveWorktree(ctx, e, root, branch, p.Path, cfg, complete, runner, report)
	}
	endAction(ctx, runner, report, ui.Finish(wanted.Finish), f.grace)
	if result.Status != "done" {
		return fmt.Errorf("%s", result.Message)
	}
	return nil
}

// pushBranch sends the branch to its remote once the run is over, whatever
// the run's result: what landed before a block is still good. A push that fails
// is reported and changes nothing else.
func pushBranch(ctx context.Context, root string, runner *loop.Loop, report *ui.Reporter) {
	if ctx.Err() != nil {
		report.Say("the run was cancelled; not pushing")
		runner.Note("push", "skipped: the run was cancelled")
		return
	}
	pushCtx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	where, err := repo.New(root, nil).Push(pushCtx)
	if err != nil {
		report.Fail("push failed: %v", err)
		runner.Note("push", "failed: "+err.Error())
		return
	}
	report.Say("pushed %s", where)
	runner.Note("push", where)
}

// pushTimeout bounds a push nobody is watching, so a stuck remote cannot hold
// off the end action.
const pushTimeout = 2 * time.Minute

// endAction powers off or suspends after a countdown, and records what came of
// it. A run the author cancelled does nothing more: whoever stopped it is
// either at the keyboard or closed the terminal the countdown would show in.
func endAction(ctx context.Context, runner *loop.Loop, report *ui.Reporter, finish ui.Finish, grace time.Duration) {
	if !finish.Acts() {
		return
	}
	if ctx.Err() != nil {
		report.Say("the run was cancelled; not going to %s", finish)
		runner.Note("finish", string(finish)+" skipped: the run was cancelled")
		return
	}
	if finish.Countdown(os.Stdout, grace) {
		report.Say("cancelled; the machine stays up")
		runner.Note("finish", string(finish)+" cancelled at the keyboard")
		return
	}
	runner.Note("finish", string(finish))
	if err := finish.Execute(os.Stdout); err != nil {
		report.Fail("the end action failed: %v", err)
		runner.Note("finish", string(finish)+" failed: "+err.Error())
	}
}

func dryRun(ctx context.Context, runner *loop.Loop, c catalog.Copy, f *runFlags) error {
	p := c.Plan
	unit := state.Resume(p, c.State)
	if f.from != "" {
		unit = p.Unit(f.from)
	}
	if unit == nil {
		fmt.Printf("%s is complete\n", p.Path)
		return nil
	}
	prompt, err := runner.Prompt(ctx, p, *unit, "")
	if err != nil {
		return err
	}
	fmt.Print(prompt)
	return nil
}

// openUnits counts the units still to land.
func openUnits(p *plan.Plan, s *state.State) int {
	open := 0
	for _, u := range p.Units {
		if status := s.Status(u.ID); status != state.Landed && status != state.Skipped {
			open++
		}
	}
	return open
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

func modelSuffix(model string) string {
	if model == "" {
		return ""
	}
	return " (" + model + ")"
}

func secondarySuffix(secondary *loop.Agent) string {
	if secondary == nil {
		return ""
	}
	return ", then " + secondary.Label() + " on a usage limit"
}

func peakSuffix(chosen chosenRun) string {
	peaked := len(chosen.agent.Harness.PeakWindows()) > 0 ||
		(chosen.secondary != nil && len(chosen.secondary.Harness.PeakWindows()) > 0)
	if !chosen.waitOffPeak || !peaked {
		return ""
	}
	return ", off-peak only"
}

func pushSuffix(push bool) string {
	if !push {
		return ""
	}
	return ", pushed at the end"
}

func finishSuffix(finish ui.Finish) string {
	if finish == ui.FinishNone || finish == "" {
		return ""
	}
	return ", then " + string(finish)
}

// choosePlan takes the plan from the flag, or offers the open ones, each
// where its progress lives.
func choosePlan(ctx context.Context, e *env, f *runFlags) (catalog.Copy, error) {
	if f.planName != "" {
		return e.find(ctx, f.planName)
	}
	return e.pick(ctx, "Which plan?", !f.all)
}

// target is where a run happens: always a worktree of the plan's branch.
type target struct {
	root   string
	cfg    config.Config
	branch string
	fresh  bool // no run has used the worktree yet, so it wants the setup command
}

func prepareTarget(ctx context.Context, e *env, f *runFlags, c catalog.Copy) (target, error) {
	root := ""
	if f.dryRun {
		checked, err := e.checkedOut(ctx, c)
		if err != nil {
			return target{}, err
		}
		root = checked.Root
	} else {
		placed, err := e.place(ctx, c)
		if err != nil {
			return target{}, err
		}
		root = placed
	}
	cfg, err := config.Load(root)
	if err != nil {
		return target{}, err
	}
	branch, err := repo.New(root, nil).Branch(ctx)
	if err != nil {
		return target{}, err
	}
	_, used := os.Stat(filepath.Join(root, repo.WorkDir))
	return target{root: root, cfg: cfg, branch: branch, fresh: os.IsNotExist(used)}, nil
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
