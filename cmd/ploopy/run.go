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
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

type runFlags struct {
	planName      string
	all           bool
	agent         string
	effort        string
	model         string
	from          string
	until         string
	units         int
	retries       int
	blockRetries  int
	timeout       time.Duration
	backoff       time.Duration
	tests         bool
	noTests       bool
	verify        string
	testCmd       string
	context       string
	notify        string
	allowDirty    bool
	worktree      bool
	dryRun        bool
	finish        string
	push          bool
	budget        float64
	secondary     string
	secondaryMod  string
	secondaryEff  string
	rescue        string
	rescueMod     string
	rescueEff     string
	peak          string
	subscriptions string
	spendAll      bool
	followUp      bool
	then          string
	triageFlags
	quiet    bool
	grace    time.Duration
	away     string
	night    string
	reserve  int
	detach   bool
	here     bool
	launched bool
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
		RunE: func(cmd *cobra.Command, _ []string) error { return runPlan(cmd, e, &f, nil) },
	}
	addRunFlags(cmd, &f)
	addTriageFlagsTo(cmd, &f.triageFlags)
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
	flags.BoolVar(&f.followUp, "follow-up", false, "after a plan or review completes, continue with its follow-up")
	flags.StringVar(&f.then, "then", "", "")
	_ = flags.MarkDeprecated("then", "use --follow-up instead")
	flags.BoolVar(&f.detach, "detach", false, "run in a tmux window of its own, as launch = \"tmux\" in .ploopy.toml does")
	flags.BoolVar(&f.here, "here", false, "run in this terminal, whatever launch in .ploopy.toml says")
	flags.BoolVar(&f.launched, launchedFlag, false, "")
	_ = flags.MarkHidden(launchedFlag)
	flags.BoolVar(&f.dryRun, "dry-run", false, "print the first session's prompt and stop")
	flags.StringVar(&f.finish, "finish", "", "when the run ends: none, suspend or poweroff")
	flags.BoolVar(&f.push, "push", false, "push the branch when the run ends, before the end action")
	flags.Float64Var(&f.budget, "budget", 0, "spending cap per session, in dollars")
	flags.StringVar(&f.secondary, "secondary", "",
		"harness that takes over when the agent hits a usage limit, or none")
	flags.StringVar(&f.secondaryMod, "secondary-model", "", "model for the secondary harness")
	flags.StringVar(&f.secondaryEff, "secondary-effort", "", "effort for the secondary harness")
	flags.StringVar(&f.rescue, "rescue", "",
		"harness that diagnoses a unit the run would stop on, or none")
	flags.StringVar(&f.rescueMod, "rescue-model", "", "model for the rescue harness")
	flags.StringVar(&f.rescueEff, "rescue-effort", "", "effort for the rescue harness")
	flags.StringVar(&f.peak, "peak", "", "a unit due in the harness's peak hours: wait or run")
	flags.StringVar(&f.subscriptions, "subscriptions", "",
		"lent subscriptions to use: all, none, or names such as claude,codex")
	flags.BoolVar(&f.spendAll, "spend-all", false, "spend the subscriptions' weeks, keeping no reserve")
	flags.BoolVar(&f.quiet, "quiet", false, "report decisions only, without the session feed")
	flags.DurationVar(&f.grace, "grace", time.Minute, "countdown before the end action")
	flags.StringVar(&f.away, "away", "", "")
	_ = flags.MarkDeprecated("away", "lend subscriptions with `ploopy lend`")
	flags.StringVar(&f.night, "night", "", "")
	_ = flags.MarkDeprecated("night", "lend subscriptions with `ploopy lend`")
	flags.IntVar(&f.reserve, "weekly-reserve", 0, "")
	_ = flags.MarkDeprecated("weekly-reserve", "lend subscriptions with `ploopy lend`")
}

// planRun is one plan's run, up to the moment it ends. What follows the run
// is deferred so a review can hand over to its fix plan first.
type planRun struct {
	root     string
	branch   string
	cfg      config.Config
	p        *plan.Plan
	runner   *loop.Loop
	report   *ui.Reporter
	result   loop.Result
	complete bool
	limited  bool
	wanted   control.Settings
	chosen   chosenRun
}

func runPlan(cmd *cobra.Command, e *env, f *runFlags, chosen *chosenRun) error {
	ctx := cmd.Context()
	last, err := runOnce(ctx, cmd, e, f, chosen)
	if err != nil || last == nil {
		return err
	}

	for {
		next, err := chainedPlan(ctx, cmd, e, f, last)
		if err != nil {
			finishRun(ctx, e, f, last)
			return err
		}
		if next == nil {
			break
		}
		last = next
	}

	finishRun(ctx, e, f, last)
	if last.result.Status != "done" {
		return fmt.Errorf("%s", last.result.Message)
	}
	return nil
}

// chainedPlan is the next plan the run carries, or nil when the chain ends.
func chainedPlan(ctx context.Context, cmd *cobra.Command, e *env, f *runFlags, r *planRun) (*planRun, error) {
	if !followsUp(ctx, r) {
		return nil, nil
	}
	if r.p.Reviews() != "" {
		fixes, ok := chainedFixPlan(ctx, r)
		if !ok {
			return nil, nil
		}
		r.report.Say("%s complete; running its fix plan %s", r.p.Path, fixes)
		return runChainedPlan(ctx, cmd, e, f, r, fixes)
	}
	if review.IsFix(r.p.Path) {
		return nil, nil
	}
	return chainedReview(ctx, cmd, e, f, r)
}

// followsUp reports whether the run may go on to the next plan of its chain.
func followsUp(ctx context.Context, r *planRun) bool {
	return ctx.Err() == nil && r.p != nil && r.complete && r.result.Status == "done" &&
		!r.limited && !r.result.Stopped &&
		!r.wanted.Stops() && r.wanted.FollowUp != nil && *r.wanted.FollowUp
}

// chainedReview writes and runs the review of a complete ordinary plan.
func chainedReview(ctx context.Context, cmd *cobra.Command, e *env, f *runFlags, r *planRun) (*planRun, error) {
	path := review.Path(r.p.Path)
	if isFile(filepath.Join(r.root, path)) {
		existing, err := plan.Load(filepath.Join(r.root, path), path)
		if err != nil {
			return nil, err
		}
		s, err := state.Load(filepath.Join(r.root, state.PathFor(path)))
		if err != nil {
			return nil, err
		}
		if loop.Complete(existing, s) {
			r.report.Say("%s is already complete; not reviewing %s again", path, r.p.Path)
			return nil, nil
		}
		r.report.Say("%s is already written; running it on", path)
		return runChainedPlan(ctx, cmd, e, f, r, path)
	}
	fixes := review.FixesPath(r.p.Path)
	if isFile(filepath.Join(r.root, fixes)) {
		r.report.Say("%s from an earlier review still exists; not reviewing %s again", fixes, r.p.Path)
		return nil, nil
	}
	s, err := state.Load(filepath.Join(r.root, state.PathFor(r.p.Path)))
	if err != nil {
		return nil, err
	}
	units, err := review.Reviewable(r.p, s, "")
	if err != nil {
		return nil, err
	}
	if len(units) == 0 {
		r.report.Say("%s has no landed unit with commits to review; ending the chain", r.p.Path)
		return nil, nil
	}
	rf := &reviewFlags{run: *f}
	rf.triageFlags = f.triageFlags
	triage, _, err := chooseTriageWithSources(ctx, cmd, e, rf, nil)
	if err != nil {
		return nil, err
	}
	r.report.Say("%s complete; reviewing it (triage %s)", r.p.Path, triage.Label())
	if err := writeReview(cmd, e, r.root, r.p, s, rf, triage); err != nil {
		return nil, err
	}
	return runChainedPlan(ctx, cmd, e, f, r, path)
}

// runOnce carries one plan to its end, without pushing, leaving its worktree
// or taking the end action.
func runOnce(ctx context.Context, cmd *cobra.Command, e *env, f *runFlags, chosen *chosenRun) (*planRun, error) {
	c, err := choosePlan(ctx, e, f)
	if err != nil {
		return nil, err
	}
	if c.Live != nil {
		if !ui.Interactive() {
			return nil, fmt.Errorf("%s is already running in %s; change it with `ploopy adjust`", c.Plan.Path, c.Live.Root)
		}
		fmt.Printf("%s is already running in %s.\n", c.Plan.Path, c.Live.Root)
		return nil, adjustInteractively(ctx, e, *c.Live)
	}
	t, err := prepareTarget(ctx, e, f, c)
	if err != nil {
		return nil, err
	}
	p, cfg := c.Plan, t.cfg
	if errs := lintReport(e, t.root, p, os.Stderr, false); len(errs) > 0 {
		return nil, fmt.Errorf("%s does not lint; fix it before running it", p.Path)
	}

	var pick chosenRun
	if chosen == nil {
		settled, err := chooseSettings(ctx, cmd, e, p, f, t.branch, openUnits(p, c.State))
		if err != nil {
			return nil, err
		}
		pick = settled
	} else {
		pick = *chosen
	}

	if detached(e, f) {
		return nil, launch(ctx, cmd, e, f, p, pick)
	}

	skill, err := skillsAt(t.root, cfg)
	if err != nil {
		return nil, err
	}
	verify, test := f.verify, f.testCmd
	if verify == "" {
		verify = cfg.Verify
	}
	if test == "" {
		test = cfg.Test
	}
	if f.tests && test == "" {
		return nil, fmt.Errorf("--test needs a test command: set `test` in .ploopy.toml or pass --test-cmd")
	}

	report := ui.NewReporter(os.Stdout, os.Stderr)
	report.Quiet = f.quiet
	leases := newLendingLoader(e.harnesses, pick.agent.Harness.Name(), report)
	opts := runOptions(ctx, e, f, p, cfg, skill, pick, verify, test)

	if f.dryRun {
		runner, err := loop.New(t.root, opts, report)
		if err != nil {
			return nil, err
		}
		return nil, dryRun(ctx, runner, c, f)
	}

	root := t.root
	if !samePath(root, e.root) {
		report.Say("%s runs in %s, where %s is checked out", p.Path, root, t.branch)
	}
	if t.fresh {
		opts.SetupCmd = cfg.Setup
		opts.SetupEnv = e.setupEnv(ctx)
	}
	branch, _ := repo.New(root, nil).Branch(ctx)
	opts.Branch = branch
	opts.Lending = leases.load(time.Now(), pick.subscriptions, pick.spendAll)
	limited := f.until != "" || f.units > 0
	followUp := &pick.followUp
	if review.IsFix(p.Path) {
		followUp = nil
	}
	steer := announce(control.Status{
		Root: root, Plan: p.Path, Branch: branch,
		Agent: pick.agent.Label(),
		Settings: control.Settings{
			Finish: string(pick.finish), Peak: peakName(pick.waitOffPeak), Push: &pick.push,
			Secondary:     toControlAgent(pick.secondary),
			Rescue:        toControlAgent(pick.rescue),
			Subscriptions: pick.subscriptions, SpendAll: &pick.spendAll, FollowUp: followUp,
		},
	}, p.Reviews() != "", e.harnesses, leases, report)
	defer steer.close()
	opts.Steering = steer.loopSteering()

	runner, err := loop.New(root, opts, tracked{Reporter: report, steer: steer})
	if err != nil {
		return nil, err
	}

	report.Say("%s with %s%s at %s effort%s%s%s%s%s", p.Path, pick.agent.Harness.Name(),
		modelSuffix(pick.agent.Model), pick.agent.Effort, secondarySuffix(pick.secondary),
		peakSuffix(pick), pushSuffix(pick.push), finishSuffix(pick.finish),
		followUpSuffix(pick.followUp, p, limited))
	if fallback := harness.FallbackModel(pick.agent.Harness, pick.agent.Model); fallback != "" {
		report.Say("on a usage limit, %s continues with %s before waiting or switching harness", pick.agent.Model, fallback)
	}
	announceSubscriptions(report, e, p, pick)
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
				pending := &planRun{
					root: root, p: p, result: result, complete: true,
					limited: limited, wanted: steer.wanted(),
				}
				_, next := chainedFixPlan(ctx, pending)
				reviewOutcome(root, p, next)
			}
		} else {
			report.Summary(p, s, &result.Stats)
		}
	}
	report.Degraded(result.Stats.Degraded)

	return &planRun{
		root: root, branch: branch, cfg: cfg, p: p, runner: runner, report: report,
		result: result, complete: complete, limited: limited,
		wanted: steer.wanted(), chosen: pick,
	}, nil
}

// runOptions is the loop's settings for one plan, from what the run settled
// on and the checks chosen for it.
func runOptions(ctx context.Context, e *env, f *runFlags, p *plan.Plan, cfg config.Config, skill skills, pick chosenRun, verify, test string) loop.Options {
	return loop.Options{
		PlanPath:     p.Path,
		Harness:      pick.agent.Harness,
		Model:        pick.agent.Model,
		Effort:       pick.agent.Effort,
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
		RescueSkill:  skill.rescue,
		Checklist:    skill.checklist,
		Harnesses:    e.harnesses,
		VerifyCmd:    verify,
		TestCmd:      test,
		AuthorPaths:  cfg.AuthorPaths,
		StateCommit:  cfg.StateCommit,
		Notify:       f.notify,
		HarnessArgs:  e.extra,
		MaxBudgetUSD: f.budget,
		Secondary:    pick.secondary,
		Rescue:       pick.rescue,
		WaitOffPeak:  pick.waitOffPeak,
		History:      historyPath(ctx, e.root),
	}
}

// announceSubscriptions says which subscriptions the run may use and whether
// it spends all of their weeks. A narrowed set is never silent.
func announceSubscriptions(report *ui.Reporter, e *env, p *plan.Plan, pick chosenRun) {
	if pick.subscriptions != "all" {
		origin := "this run"
		if planSet, err := subscriptionSet(e.harnesses, p.Settings["subscriptions"]); err == nil &&
			p.Settings["subscriptions"] != "" && planSet == pick.subscriptions {
			origin = "the plan"
		}
		report.Say("%s uses %s", origin, subscriptionText(pick.subscriptions))
	}
	if pick.spendAll {
		report.Say("spending all of the subscriptions' weeks")
	}
}

// chainedFixPlan is the fix plan a complete review hands over to, when
// follow-up says so and the triage wrote one.
func chainedFixPlan(ctx context.Context, r *planRun) (string, bool) {
	if !followsUp(ctx, r) || r.p.Reviews() == "" {
		return "", false
	}
	fixes := review.FixesPath(r.p.Reviews())
	if !isFile(filepath.Join(r.root, fixes)) {
		return "", false
	}
	return fixes, true
}

// runChainedPlan runs the next plan of the chain in the same worktree and
// settings, but with its own front matter where it gives some.
func runChainedPlan(ctx context.Context, cmd *cobra.Command, e *env, f *runFlags, previous *planRun, path string) (*planRun, error) {
	p, err := plan.Load(filepath.Join(previous.root, path), path)
	if err != nil {
		return nil, err
	}
	settings, err := applyPlanSettings(e, p, overChosen(e, previous.chosen, previous.wanted))
	if err != nil {
		return nil, err
	}
	next := *f
	next.planName = path
	next.from, next.until, next.units = "", "", 0
	next.dryRun = false
	// The plan's own test setting, when it gives one, answers for itself.
	if _, given := p.Settings["test"]; given {
		next.tests, next.noTests = false, false
	}
	return runOnce(ctx, cmd, e, &next, &settings)
}

// finishRun pushes, leaves the worktree and takes the end action once, after
// the last plan the run carried.
func finishRun(ctx context.Context, e *env, f *runFlags, r *planRun) {
	if r.wanted.Pushes() {
		pushBranch(ctx, r.root, r.runner, r.report)
	}
	if r.branch != "" && e.ours(ctx, r.root) {
		leaveWorktree(ctx, r.root, r.branch, r.p.Path, r.cfg, r.complete, r.report)
	}
	endAction(ctx, r.runner, r.report, ui.Finish(r.wanted.Finish), f.grace)
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
	if !chosen.waitOffPeak {
		return ""
	}
	a := chosen.agent
	if len(harness.PeakWindowsFor(a.Harness, a.Model)) > 0 ||
		(chosen.secondary != nil && len(harness.PeakWindowsFor(chosen.secondary.Harness, chosen.secondary.Model)) > 0) {
		return ", off-peak only"
	}
	if fallback := harness.FallbackModel(a.Harness, a.Model); fallback != "" && len(harness.PeakWindowsFor(a.Harness, fallback)) > 0 {
		return ", fallback off-peak only"
	}
	return ""
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

// followUpSuffix says which plan a run with follow-up goes on to, if any.
func followUpSuffix(followUp bool, p *plan.Plan, limited bool) string {
	if !followUp || p == nil || limited {
		return ""
	}
	switch {
	case p.Reviews() != "":
		return ", then its fix plan"
	case review.IsFix(p.Path):
		return ""
	default:
		return ", then its review and fix plan"
	}
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
