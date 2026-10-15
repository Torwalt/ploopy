// Package loop runs a plan's units, one fresh session each, and decides from
// the repository what happened.
package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/guard"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/stats"
)

const (
	handoverFallbackLines = 40
	sessionTailLines      = 400
)

// Reporter is everything the loop says. The terminal is the UI's to own.
type Reporter interface {
	Say(format string, args ...any)
	Fail(format string, args ...any)
	UnitStart(u plan.Unit, attempt int, logPath string)
	UnitDone(u plan.Unit, v Verdict)
	Event(e harness.Event)
}

// Options is one run.
type Options struct {
	PlanPath string // relative to the repository root
	Harness  harness.Harness
	Model    string
	Effort   string

	Start    string // start at this unit instead of resuming
	Until    string // stop after this unit
	MaxUnits int

	Retries        int
	BlockRetries   int
	Timeout        time.Duration
	Backoff        time.Duration
	TransientLimit int
	LimitWait      time.Duration
	LimitWaits     int

	Tests       *bool // overrides what the plan asks for
	Context     []string
	BaseContext []string
	Skill       string // the plan-unit skill, inlined in every prompt

	// The skills of a review plan's units, and the repository's own review
	// checklist, which collecting units are given beside their skill.
	ReviewSkill string
	TriageSkill string
	Checklist   string

	// Harnesses is every harness a unit's own `Agent:` may name.
	Harnesses harness.Set

	SetupCmd    string // readies a fresh worktree, once, before the preflight
	VerifyCmd   string
	TestCmd     string
	AuthorPaths []string
	StateCommit string

	Notify       string
	AllowDirty   bool
	HarnessArgs  []string
	MaxBudgetUSD float64

	// Secondary takes over when the primary agent hits a usage limit, instead
	// of the run waiting it out. The run goes back to the primary at the first
	// unit after the limit resets.
	Secondary *Agent

	// WaitOffPeak holds a unit due to start in the harness's peak hours until
	// they end. The author decides it before the run; nothing is asked during it.
	WaitOffPeak bool

	// Night lets the author's subscriptions take sessions while the author is
	// away from them, or nil.
	Night *Night

	// History is the repository's record of every session across plans, or
	// empty to keep none. Branch is what the run works on, for that record.
	History string
	Branch  string

	// Steering is what the author changes while the run goes on. Without it,
	// the run keeps what it started with.
	Steering Steering
	Now      func() time.Time
	Sleep    func(ctx context.Context, d time.Duration) error
}

// Steer is what the author has the run do now.
type Steer struct {
	WaitOffPeak bool
	Secondary   *Agent
	Night       *Night
	Stop        bool // stop once the current unit is done
}

// Steering answers what the author wants now. Changed fires when that may
// have changed, so a wait can end early and look again.
type Steering interface {
	Steer() Steer
	Changed() <-chan struct{}
}

// errStopped is a run the author asked to stop.
var errStopped = errors.New("stopped as asked")

// Defaults fills in what a run does not set.
func (o *Options) Defaults() {
	if o.Timeout == 0 {
		o.Timeout = 90 * time.Minute
	}
	if o.Backoff == 0 {
		o.Backoff = 2 * time.Minute
	}
	if o.TransientLimit == 0 {
		o.TransientLimit = 3
	}
	if o.LimitWait == 0 {
		o.LimitWait = 30 * time.Minute
	}
	if o.LimitWaits == 0 {
		o.LimitWaits = 12
	}
	if o.StateCommit == "" {
		o.StateCommit = config.Default().StateCommit
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = sleep
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Result is how a run ended. Status is what a notify command is given.
type Result struct {
	Status  string // "done" or "failed"
	Message string
	Stats   stats.Run
}

// Loop is one run of one plan.
type Loop struct {
	root   string
	name   string // the plan's
	opts   Options
	repo   *repo.Repo
	report Reporter

	dir       string
	logs      string
	handovers string
	progress  *progress
	unit      string
	peakSaid  time.Time // the peak window already reported as run through

	onSecondary bool
	primaryFree time.Time // when the usage limit that moved the run to the secondary resets

	// own is the agent the current unit names for itself. It runs that unit
	// whatever the run's agent is, and never hands it to the secondary.
	own *Agent

	runID   string
	stats   stats.Run
	mark    time.Time                // when the current unit's clock last moved
	checked map[string]time.Duration // verify and test time since the last session

	meters    map[string]*meter // by harness
	nightSaid string            // the last reason given for who runs a session
	ran       map[string]int    // sessions per agent, for what degraded
}

// New prepares a run. The plan itself is read again before every unit.
func New(root string, opts Options, report Reporter) (*Loop, error) {
	opts.Defaults()
	p, err := plan.Load(filepath.Join(root, opts.PlanPath), opts.PlanPath)
	if err != nil {
		return nil, err
	}
	dir := stats.Dir(root, p.Name())
	return &Loop{
		root: root, name: p.Name(), opts: opts,
		repo:      repo.New(root, opts.AuthorPaths),
		report:    report,
		dir:       dir,
		logs:      filepath.Join(dir, "logs"),
		handovers: filepath.Join(dir, "handover"),
		progress:  loadProgress(filepath.Join(dir, "progress.json")),
	}, nil
}

// prepareWorkDir makes ploopy's directory ignore itself, so a session running
// `git add -A` cannot sweep prompts, logs and handovers into a unit's commit.
func (l *Loop) prepareWorkDir() error {
	own := filepath.Join(l.root, repo.WorkDir)
	if err := os.MkdirAll(own, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(own, ".gitignore"), []byte("*\n"), 0o644)
}

// reload reads the plan and its state again, so editing an unlanded unit or
// marking one by hand during a run takes effect at the next unit.
func (l *Loop) reload() (*plan.Plan, *state.State, error) {
	p, err := plan.Load(filepath.Join(l.root, l.opts.PlanPath), l.opts.PlanPath)
	if err != nil {
		return nil, nil, err
	}
	s, err := state.Load(filepath.Join(l.root, state.PathFor(p.Path)))
	if err != nil {
		return nil, nil, err
	}
	return p, s, nil
}

// Prompt renders what a session would be given, without running anything.
func (l *Loop) Prompt(ctx context.Context, p *plan.Plan, u plan.Unit, failure string) (string, error) {
	_, s, err := l.reload()
	if err != nil {
		return "", err
	}
	return l.prompt(ctx, p, s, u, failure)
}

func (l *Loop) prompt(ctx context.Context, p *plan.Plan, s *state.State, u plan.Unit, failure string) (string, error) {
	if u.Role() != plan.Implement {
		return l.reviewPrompt(ctx, p, s, u, failure)
	}
	handover, _ := filepath.Rel(l.root, l.handoverPath(u))
	return renderPrompt(promptArgs{
		Skill:      l.opts.Skill,
		Plan:       p,
		Unit:       u,
		Context:    l.context(p, u),
		Handover:   handover,
		Previous:   l.previousHandover(p, s, u),
		Failure:    failure,
		WantsTests: l.wantsTests(p, u),
		Verify:     l.opts.VerifyCmd,
		Test:       l.opts.TestCmd,
	}), nil
}

func (l *Loop) context(p *plan.Plan, u plan.Unit) []string {
	docs := l.opts.Context
	if len(docs) == 0 {
		docs = p.Context(l.root, &u, l.opts.BaseContext)
	}
	var out []string
	for _, doc := range docs {
		if info, err := os.Stat(filepath.Join(l.root, doc)); err == nil && info.Mode().IsRegular() {
			out = append(out, doc)
		}
	}
	return out
}

func (l *Loop) wantsTests(p *plan.Plan, u plan.Unit) bool {
	if l.opts.Tests != nil {
		return *l.opts.Tests
	}
	return p.WantsTests(u)
}

func (l *Loop) handoverPath(u plan.Unit) string {
	return filepath.Join(l.handovers, u.ID+".md")
}

func (l *Loop) previousHandover(p *plan.Plan, s *state.State, u plan.Unit) string {
	previous := p.Previous(u)
	if previous == nil {
		return ""
	}
	if entry := s.Entry(previous.ID); entry != nil && entry.Handover != "" {
		return entry.Handover
	}
	if text, err := os.ReadFile(l.handoverPath(*previous)); err == nil {
		return string(text)
	}
	return ""
}

// record appends to the run log, which is the history of everything the loop
// decided for this plan.
func (l *Loop) record(kind, message string) {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(l.dir, "run.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	line := strings.ReplaceAll(message, "\n", " ")
	fmt.Fprintf(file, "%s\t%s\t%s\t%s\n",
		l.opts.Now().Format(time.RFC3339), l.unit, kind, line)
}

// Note records what happened after the loop itself ended, such as the end
// action, in the same run log.
func (l *Loop) Note(kind, message string) { l.record(kind, message) }

// notify writes the run's status and runs the author's notify command.
func (l *Loop) notify(status, message string) {
	if err := os.MkdirAll(l.dir, 0o755); err == nil {
		_ = os.WriteFile(filepath.Join(l.dir, "status"), []byte(status), 0o644)
	}
	if l.opts.Notify == "" {
		return
	}
	cmd := exec.Command("sh", "-c", l.opts.Notify)
	cmd.Dir = l.root
	cmd.Env = append(os.Environ(), "PLOOPY_STATUS="+status, "PLOOPY_MESSAGE="+message)
	_ = cmd.Run()
}

func (l *Loop) failRun(message string) Result {
	l.record("failed", message)
	l.notify("failed", message)
	l.report.Fail("%s", message)
	return Result{Status: "failed", Message: message, Stats: l.ended("failed")}
}

func (l *Loop) finish(message string) Result {
	l.notify("done", message)
	return Result{Status: "done", Message: message, Stats: l.ended("done")}
}

// Run walks the plan's open units.
func (l *Loop) Run(ctx context.Context) Result {
	l.stats = stats.Run{Started: l.opts.Now()}
	l.runID = l.stats.Started.Format(time.RFC3339)
	if err := l.prepareWorkDir(); err != nil {
		return l.failRun(err.Error())
	}
	if err := os.MkdirAll(l.logs, 0o755); err != nil {
		return l.failRun(err.Error())
	}
	if err := os.MkdirAll(l.handovers, 0o755); err != nil {
		return l.failRun(err.Error())
	}

	p, s, err := l.reload()
	if err != nil {
		return l.failRun(err.Error())
	}

	unit := l.firstUnit(p, s)
	if unit == nil {
		message := p.Path + " is complete: every unit is landed or skipped"
		l.report.Say("%s", message)
		return l.finish(message)
	}

	if result, stop := l.checkTree(ctx, *unit); stop {
		return result
	}
	if err := l.setup(ctx); err != nil {
		return l.failRun(err.Error())
	}
	if err := l.preflight(ctx); err != nil {
		return l.failRun(err.Error())
	}

	completed := 0
	for unit != nil {
		l.backToPrimary()
		if l.own, err = l.unitAgent(*unit); err != nil {
			return l.failRun(err.Error())
		}
		if err := l.peakGate(ctx); err != nil {
			if errors.Is(err, errStopped) {
				l.report.Say("stopping before %s, as asked", unit.ID)
				return l.finish("stopped before " + unit.ID + " as asked")
			}
			return l.failRun(err.Error())
		}
		if err := l.runUnit(ctx, *unit); err != nil {
			if errors.Is(err, errStopped) {
				l.report.Say("stopping during %s, as asked; it stays open", unit.ID)
				return l.finish("stopped during " + unit.ID + " as asked")
			}
			return l.failRun(fmt.Sprintf("%s. Resume with: ploopy run --plan %s", err, l.opts.PlanPath))
		}
		completed++

		if l.steer().Stop {
			l.report.Say("stopping after %s, as asked", unit.ID)
			return l.finish("stopped after " + unit.ID + " as asked")
		}

		if l.opts.Until != "" && unit.ID == l.opts.Until {
			l.report.Say("reached --until %s", unit.ID)
			return l.finish("stopped at --until " + unit.ID)
		}
		if l.opts.MaxUnits > 0 && completed >= l.opts.MaxUnits {
			l.report.Say("reached --units %d", l.opts.MaxUnits)
			return l.finish(fmt.Sprintf("ran %d unit(s)", completed))
		}

		p, s, err = l.reload()
		if err != nil {
			return l.failRun(err.Error())
		}
		next := state.NextOpen(p, s, *unit)
		if next == nil {
			l.report.Say("plan complete: %s was the last open unit", unit.ID)
			return l.finish(p.Path + " is complete")
		}
		unit = next
	}
	return l.finish(p.Path + " is complete")
}

// agent is who runs the next session.
func (l *Loop) agent() Agent {
	agent, _, _, _ := l.pick()
	return agent
}

// pick is who runs the next session: the unit's own agent, else a night
// agent with room, else the secondary while the primary is limited, else the
// primary. why says what decided it, when the night did.
func (l *Loop) pick() (agent Agent, night, fresh bool, why string) {
	if l.own != nil {
		return *l.own, false, false, ""
	}
	n, fresh, why := l.nightPick()
	if n != nil {
		return n.Agent, true, fresh, why
	}
	if secondary := l.secondary(); l.onSecondary && secondary != nil {
		return *secondary, false, false, why
	}
	return l.primary(), false, false, why
}

// sayPick tells the author why an agent other than the planned one runs a
// session, each reason once.
func (l *Loop) sayPick(agent Agent, u plan.Unit, why string) {
	if why == "" || why == l.nightSaid {
		return
	}
	l.nightSaid = why
	l.record("night", why+"; "+agent.Label()+" runs "+u.ID)
	l.report.Say("%s, so %s runs %s", why, agent.Label(), u.ID)
}

// recorded is the agent that ran a session the last run left unjudged, when
// this run can still name its harness.
func (l *Loop) recorded(r *sessionRecord) (Agent, bool) {
	known := harness.Set{l.opts.Harness}
	if l.own != nil {
		known = append(known, l.own.Harness)
	}
	if secondary := l.secondary(); secondary != nil {
		known = append(known, secondary.Harness)
	}
	if n := l.night(); n != nil {
		for _, a := range n.Agents {
			known = append(known, a.Harness)
		}
	}
	h := append(known, l.opts.Harnesses...).Get(r.Harness)
	if h == nil {
		return Agent{}, false
	}
	return Agent{Harness: h, Model: r.Model, Effort: r.Effort}, true
}

// unitAgent is the agent a unit names for itself, or nil to run it with the
// run's own.
func (l *Loop) unitAgent(u plan.Unit) (*Agent, error) {
	name := strings.TrimSpace(u.Fields["Agent"])
	if name == "" {
		return nil, nil
	}
	h := l.opts.Harnesses.Get(name)
	if h == nil {
		return nil, fmt.Errorf("unit %s asks for agent %s, which ploopy cannot drive here", u.ID, name)
	}
	return &Agent{
		Harness: h,
		Model:   strings.TrimSpace(u.Fields["Model"]),
		Effort:  strings.TrimSpace(u.Fields["Effort"]),
	}, nil
}

func (l *Loop) primary() Agent {
	return Agent{Harness: l.opts.Harness, Model: l.opts.Model, Effort: l.opts.Effort}
}

func (l *Loop) secondary() *Agent { return l.steer().Secondary }

// steer is what the author wants now.
func (l *Loop) steer() Steer {
	if l.opts.Steering == nil {
		return Steer{WaitOffPeak: l.opts.WaitOffPeak, Secondary: l.opts.Secondary, Night: l.opts.Night}
	}
	return l.opts.Steering.Steer()
}

// toSecondary hands the run to the secondary agent when the primary hits a
// usage limit, instead of waiting the limit out.
func (l *Loop) toSecondary(agent Agent, limit *harness.RateLimit) bool {
	secondary := l.secondary()
	if l.onSecondary || secondary == nil || l.own != nil {
		return false
	}
	now := l.opts.Now()
	l.primaryFree = limit.ResetAt
	if !l.primaryFree.After(now) {
		l.primaryFree = now.Add(l.opts.LimitWait)
	}
	l.onSecondary = true
	l.record("switch", fmt.Sprintf("%s hit a usage limit; %s until %s",
		agent.Label(), secondary.Label(), l.primaryFree.Format(time.RFC3339)))
	l.report.Say("%s hit its usage limit; continuing with %s until it resets at %s",
		agent.Harness.Name(), secondary.Label(), l.primaryFree.In(now.Location()).Format("15:04"))
	return true
}

// backToPrimary returns the run to its primary agent once the limit that
// moved it has reset, or once there is no secondary left to stay on.
func (l *Loop) backToPrimary() {
	if !l.onSecondary {
		return
	}
	if l.secondary() != nil && l.opts.Now().Before(l.primaryFree) {
		return
	}
	l.onSecondary = false
	l.record("switch", "back to "+l.primary().Label())
	l.report.Say("back to %s", l.primary().Label())
}

func (l *Loop) firstUnit(p *plan.Plan, s *state.State) *plan.Unit {
	if l.opts.Start == "" {
		return state.Resume(p, s)
	}
	unit := p.Unit(l.opts.Start)
	if unit == nil {
		return nil
	}
	if status := s.Status(unit.ID); status == state.Landed || status == state.Skipped {
		l.report.Say("unit %s is already %s; running it again as asked", unit.ID, status)
	}
	return unit
}

// checkTree refuses to start on top of changes the loop did not make.
func (l *Loop) checkTree(ctx context.Context, unit plan.Unit) (Result, bool) {
	dirt, err := l.repo.DirtLines(ctx)
	if err != nil {
		return l.failRun(err.Error()), true
	}
	if len(dirt) == 0 {
		return Result{}, false
	}
	ours := l.progress.forUnit(unit.ID) && l.progress.LeftDirty
	if !ours && !l.opts.AllowDirty {
		l.unit = unit.ID
		return l.failRun("the working tree is dirty and the loop did not leave it so; " +
			"commit or stash it, or pass --allow-dirty to hand it to the session"), true
	}
	l.report.Say("continuing with the uncommitted work an earlier attempt left in the tree")
	return Result{}, false
}

// setup readies a fresh worktree, which has none of the repository's ignored
// dependencies. Whatever it changes must be ignored, or the first unit would
// be blamed for it.
func (l *Loop) setup(ctx context.Context) error {
	if l.opts.SetupCmd == "" {
		return nil
	}
	l.report.Say("setup: `%s`", l.opts.SetupCmd)
	logPath := filepath.Join(l.logs, "setup")
	if err := l.timedCheck(ctx, "setup", l.opts.SetupCmd, logPath); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("setup failed: `%s`:\n%s", l.opts.SetupCmd, logTail(logPath))
	}
	dirt, err := l.repo.DirtLines(ctx)
	if err != nil {
		return err
	}
	if len(dirt) > 0 {
		return fmt.Errorf("setup left the tree dirty; `%s` may change only ignored files:\n%s",
			l.opts.SetupCmd, strings.Join(dirt, "\n"))
	}
	return nil
}

// preflight runs the repository's own check once, so a tree that was already
// red is not blamed on the first unit.
func (l *Loop) preflight(ctx context.Context) error {
	if l.opts.VerifyCmd == "" {
		return nil
	}
	l.report.Say("preflight: `%s`", l.opts.VerifyCmd)
	logPath := filepath.Join(l.logs, "preflight.verify")
	if err := l.timedCheck(ctx, "preflight", l.opts.VerifyCmd, logPath); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("preflight failed: `%s` is already red before the first unit:\n%s",
			l.opts.VerifyCmd, logTail(logPath))
	}
	return nil
}

// peakGate holds a unit due to start in hours the harness bills double, when
// the author chose to wait. It never interrupts a unit already running.
func (l *Loop) peakGate(ctx context.Context) error {
	// Windows that meet at midnight are waited out together.
	for {
		if l.steer().Stop {
			return errStopped
		}
		// Who takes the unit depends on the windows as they stand now.
		l.readMeters(ctx)
		agent := l.agent()
		windows := agent.Harness.PeakWindows()
		if len(windows) == 0 {
			return nil
		}
		now := l.opts.Now()
		inPeak, until := harness.Peak(windows, now)
		if !inPeak {
			return nil
		}
		local := until.In(now.Location())

		if !l.steer().WaitOffPeak {
			if !until.Equal(l.peakSaid) {
				l.peakSaid = until
				l.report.Say("%s is in peak hours until %s; running anyway",
					agent.Harness.Name(), local.Format("15:04"))
			}
			return nil
		}

		// On the secondary, the primary may come back before the peak ends,
		// and a night agent may get room.
		wake := until
		if l.onSecondary && l.primaryFree.After(now) && l.primaryFree.Before(until) {
			wake = l.primaryFree
		}
		if night := l.nightWake(); !night.IsZero() && night.Before(wake) {
			wake = night
		}
		l.record("peak", "waiting for off-peak until "+wake.Format(time.RFC3339))
		l.report.Say("%s bills double until %s; waiting until %s",
			agent.Harness.Name(), local.Format("15:04"), wake.In(now.Location()).Format("15:04"))
		if _, err := l.pause(ctx, "peak", wake.Sub(now)); err != nil {
			return err
		}
		l.backToPrimary()
	}
}

func (l *Loop) sessionStem(u plan.Unit) string {
	for index := 0; ; index++ {
		stem := fmt.Sprintf("%s-%d", u.ID, index)
		if _, err := os.Stat(filepath.Join(l.logs, stem+".log")); os.IsNotExist(err) {
			return stem
		}
	}
}

func (l *Loop) runUnit(ctx context.Context, unit plan.Unit) error {
	p, s, err := l.reload()
	if err != nil {
		return err
	}
	current := p.Unit(unit.ID)
	if current == nil {
		return fmt.Errorf("unit %s is no longer in %s", unit.ID, p.Path)
	}
	u := *current
	l.unit = u.ID
	if l.own, err = l.unitAgent(u); err != nil {
		return err
	}
	l.mark = l.opts.Now()
	l.checked = map[string]time.Duration{}

	resumed := l.progress.forUnit(u.ID)
	base := ""
	failure := ""
	var replay *sessionRecord
	if resumed {
		base, failure, replay = l.progress.Base, l.progress.Failure, l.progress.Session
	}
	if base == "" || !l.repo.Contains(ctx, base) {
		if base, err = l.repo.Head(ctx); err != nil {
			return err
		}
	}
	if failure == "" {
		failure = blockedEarlier(s.Entry(u.ID))
	}
	if !resumed {
		_ = os.Remove(l.handoverPath(u))
	}
	if err := l.progress.reset(u.ID, base, failure, replay); err != nil {
		return err
	}

	l.report.Say("unit %s — %s", u.ID, u.Title)

	attempt, transient, waits, blocks := 0, 0, 0, 0
	priorBlock := ""
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		var outcome harness.Outcome
		var stem, tail string
		var info sessionInfo
		if replay == nil {
			l.readMeters(ctx)
		}
		agent, night, fresh, why := l.pick()

		if replay != nil {
			outcome, stem, tail, info = replay.Outcome, replay.Stem, replay.Tail, replay.Info
			if ran, ok := l.recorded(replay); ok {
				agent = ran
			}
			replay = nil
			l.report.Say("the last session (%s) ended but was never judged; judging it "+
				"instead of starting a new one", stem)
		} else {
			sessionHead, err := l.repo.Head(ctx)
			if err != nil {
				return err
			}
			sessionDirt, err := l.repo.DirtLines(ctx)
			if err != nil {
				return err
			}

			stem = l.sessionStem(u)
			prompt, err := l.prompt(ctx, p, s, u, failure)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(l.logs, stem+".prompt"), []byte(prompt), 0o644); err != nil {
				return err
			}

			logPath := filepath.Join(l.logs, stem+".log")
			shown, err := filepath.Rel(l.root, logPath)
			if err != nil {
				shown = logPath
			}
			l.sayPick(agent, u, why)
			l.report.UnitStart(u, attempt+1, shown)
			if err := l.progress.save(); err != nil {
				return err
			}

			outcome, tail, info, err = l.session(ctx, p, u, agent, prompt, stem, logPath)
			if err != nil && ctx.Err() == nil {
				if !night {
					return err
				}
				l.benched(agent, "could not start: "+firstLine(err.Error()))
				continue
			}
			if fresh {
				l.opening(agent, info.Started)
			}
			l.metered(agent, outcome)
			if outcome.Budget != nil {
				l.report.Event(harness.Event{Kind: harness.EventBudget, Budget: outcome.Budget})
			}

			// The signal that stops the run must not also stop it recording the
			// session it cut off.
			after := context.WithoutCancel(ctx)
			changed, err := l.touched(after, sessionHead, sessionDirt)
			if err != nil {
				return err
			}

			if ctx.Err() != nil && outcome.Marker == harness.None {
				if err := l.interrupted(after, u, stem, agent, info, outcome, changed); err != nil {
					return err
				}
				return ctx.Err()
			}

			if outcome.Exhausted != "" {
				l.account(u, stem, agent, info, outcome, "exhausted", outcome.Exhausted)
				return errors.New(outcome.Exhausted)
			}
			if outcome.RateLimit != nil {
				l.account(u, stem, agent, info, outcome, "limit", outcome.RateLimit.Reason)
				if changed {
					failure = cutOffMidUnit
					l.progress.Failure = failure
					if err := l.progress.save(); err != nil {
						return err
					}
				}
				// A night agent's limit only takes it out of turn.
				if night {
					l.record("limit", agent.Label()+" hit its usage limit; the next session goes to who has room")
					continue
				}
				if l.toSecondary(agent, outcome.RateLimit) {
					continue
				}
				waits++
				if waits > l.opts.LimitWaits {
					return fmt.Errorf("unit %s: still at a usage limit after %d waits", u.ID, l.opts.LimitWaits)
				}
				if err := l.waitOutLimit(ctx, agent, outcome.RateLimit); err != nil {
					return err
				}
				continue
			}
			if outcome.Exit != 0 && !outcome.TimedOut && !changed {
				l.account(u, stem, agent, info, outcome, "transient", fmt.Sprintf("exited %d", outcome.Exit))
				// A night agent that cannot run hands over rather than cost the unit its tries.
				if night {
					l.benched(agent, fmt.Sprintf("exited %d without touching the repository", outcome.Exit))
					continue
				}
				transient++
				l.record("transient", fmt.Sprintf("%s exited %d without touching the repository",
					agent.Harness.Name(), outcome.Exit))
				l.report.Say("session exited %d and changed nothing; treating it as transient (%d of %d)",
					outcome.Exit, transient, l.opts.TransientLimit)
				if transient >= l.opts.TransientLimit {
					return fmt.Errorf("unit %s: %d sessions in a row ended without touching the "+
						"repository (last exit %d)", u.ID, transient, outcome.Exit)
				}
				if _, err := l.pause(ctx, "backoff", l.opts.Backoff); err != nil {
					return err
				}
				continue
			}
			transient = 0

			head, err := l.repo.Head(after)
			if err != nil {
				return err
			}
			l.progress.Session = &sessionRecord{Head: head, Outcome: outcome, Stem: stem, Tail: tail,
				Harness: agent.Harness.Name(), Model: agent.Model, Effort: agent.Effort, Info: info}
			if err := l.progress.save(); err != nil {
				return err
			}
		}

		// A cancelled run still judges and records the session it was running.
		judgeCtx := context.WithoutCancel(ctx)
		verdict := l.judge(judgeCtx, p, u, agent, base, outcome, stem)
		l.account(u, stem, agent, info, outcome, verdict.Kind, verdict.Reason)

		if verdict.Fatal {
			l.progress.Session = nil
			if err := l.progress.save(); err != nil {
				return err
			}
			return fmt.Errorf("unit %s: %s", u.ID, verdict.Reason)
		}

		if verdict.Kind == Blocked {
			if blocks < l.opts.BlockRetries {
				blocks++
				priorBlock = verdict.Reason
				if failure, err = l.blockedAttempt(judgeCtx, verdict); err != nil {
					return err
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
			if priorBlock != "" {
				verdict.Notes = append(verdict.Notes,
					"a previous session blocked with: "+priorBlock)
			}
		}

		settled, err := l.settle(judgeCtx, p, u, agent, verdict, base, attempt+blocks, outcome, tail)
		if err != nil {
			return err
		}
		if settled {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}

		attempt, failure, err = l.failedAttempt(judgeCtx, u, verdict, attempt)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// session runs one agent and streams what it does to the reporter, keeping the
// tail for the handover fallback.
func (l *Loop) session(ctx context.Context, p *plan.Plan, u plan.Unit, agent Agent, prompt, stem, logPath string) (harness.Outcome, string, sessionInfo, error) {
	budget := l.opts.MaxBudgetUSD
	if own := u.Budget(); own > 0 {
		budget = own
	}
	spec := harness.Spec{
		Prompt:       prompt,
		Model:        agent.Model,
		Effort:       agent.Effort,
		Title:        fmt.Sprintf("ploopy %s %s", p.Name(), u.ID),
		SessionID:    uuid.NewString(),
		Dir:          l.root,
		Deny:         guard.DeniedTools,
		Extra:        l.opts.HarnessArgs,
		Timeout:      l.opts.Timeout,
		MaxBudgetUSD: budget,
		LogPath:      logPath,
	}

	info := sessionInfo{Started: l.opts.Now(), WorkOrder: len(p.WorkOrder(u))}
	if night := l.night(); night != nil {
		_, _, info.Away = night.Away.Span(info.Started)
	}
	session, err := agent.Harness.Start(ctx, spec)
	if err != nil {
		return harness.Outcome{}, "", info, err
	}

	var tail []string
	for event := range session.Events() {
		l.report.Event(event)
		switch event.Kind {
		case harness.EventToolUse:
			info.Tools++
		case harness.EventToolDone:
			l.log(stats.Record{
				Kind: "tool", Unit: u.ID, Stem: stem, Agent: agent.Label(),
				Started: l.opts.Now().Add(-event.Took), Seconds: event.Took.Seconds(),
				Tool: event.Tool, Command: event.Text,
			})
		}
		if event.Kind == harness.EventText || event.Kind == harness.EventRaw {
			tail = append(tail, event.Text)
			if len(tail) > sessionTailLines {
				tail = tail[len(tail)-sessionTailLines:]
			}
		}
	}
	outcome, err := session.Wait()
	info.Took = l.opts.Now().Sub(info.Started)
	if outcome.SessionID == "" {
		outcome.SessionID = spec.SessionID
	}
	return outcome, strings.Join(tail, "\n"), info, err
}

func (l *Loop) touched(ctx context.Context, head string, dirt []string) (bool, error) {
	now, err := l.repo.Head(ctx)
	if err != nil {
		return false, err
	}
	if now != head {
		return true, nil
	}
	current, err := l.repo.DirtLines(ctx)
	if err != nil {
		return false, err
	}
	return strings.Join(current, "\n") != strings.Join(dirt, "\n"), nil
}

// cutOffMidUnit is what the session after a usage limit is told.
const cutOffMidUnit = "A usage limit cut the previous session off mid-unit. The repository " +
	"is as it left it, and its commits count toward this unit. Continue from there."

// stoppedMidUnit is what the session after a stopped run is told.
const stoppedMidUnit = "The run was stopped while the previous session worked on this unit. " +
	"The repository is as it left it, and its commits count toward this unit. Continue from there."

// interrupted records a session the run's own stop cut off. It spends no
// attempt: the next run hands what it left to a fresh session.
func (l *Loop) interrupted(ctx context.Context, u plan.Unit, stem string, agent Agent, info sessionInfo, outcome harness.Outcome, changed bool) error {
	l.account(u, stem, agent, info, outcome, "interrupted", "the run was stopped mid-session")
	dirt, err := l.repo.DirtLines(ctx)
	if err != nil {
		return err
	}
	if changed {
		l.progress.Failure = stoppedMidUnit
	}
	l.progress.Session, l.progress.LeftDirty = nil, len(dirt) > 0
	if err := l.progress.save(); err != nil {
		return err
	}
	l.record("interrupted", "stopped mid-session; the next run continues from the tree as it is")
	l.report.Say("stopped mid-session; the next run continues from the tree as it is")
	return nil
}

// waitOutLimit waits for a usage limit to lift. When the secondary is the one
// limited and the primary comes back first, the wait ends there and the run
// goes back to the primary. A secondary named during the wait takes over at
// once, and a stop asked for ends the wait.
func (l *Loop) waitOutLimit(ctx context.Context, agent Agent, limit *harness.RateLimit) error {
	now := l.opts.Now()
	wait := l.opts.LimitWait
	if !limit.ResetAt.IsZero() && limit.ResetAt.After(now) {
		wait = limit.ResetAt.Sub(now)
	}
	if l.onSecondary && l.primaryFree.After(now) && l.primaryFree.Before(now.Add(wait)) {
		wait = l.primaryFree.Sub(now)
	}
	if night := l.nightWake(); !night.IsZero() && night.Before(now.Add(wait)) {
		wait = night.Sub(now)
	}
	until := now.Add(wait)
	l.record("limit", "usage limit; waiting until "+until.Format(time.RFC3339))
	l.report.Say("usage limit reached; waiting until %s and starting the session again",
		until.Format("15:04"))
	for {
		woken, err := l.pause(ctx, "limit", until.Sub(now))
		if err != nil {
			return err
		}
		if !woken {
			break
		}
		if l.steer().Stop {
			return errStopped
		}
		if l.toSecondary(agent, limit) {
			return nil
		}
		if now = l.opts.Now(); !now.Before(until) {
			break
		}
	}
	if l.onSecondary && !l.opts.Now().Before(l.primaryFree) {
		l.backToPrimary()
	}
	return nil
}

// handover is what the next session is told, from the file the session wrote
// or, failing that, from the end of what it said.
func (l *Loop) handover(u plan.Unit, tail string, notes *[]string) string {
	if text, err := os.ReadFile(l.handoverPath(u)); err == nil && strings.TrimSpace(string(text)) != "" {
		return strings.TrimSpace(string(text))
	}
	*notes = append(*notes, "the session wrote no handover; this is the tail of its final output")
	lines := strings.Split(harness.StripANSI(tail), "\n")
	if len(lines) > handoverFallbackLines {
		lines = lines[len(lines)-handoverFallbackLines:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (l *Loop) entry(agent Agent, verdict Verdict, base string, attempts int, handover string, outcome harness.Outcome) *state.Entry {
	status := state.Landed
	if verdict.Kind != Landed {
		status = state.Blocked
	}
	commits := make([]string, 0, len(verdict.Commits))
	for _, commit := range verdict.Commits {
		commits = append(commits, commit.Hash+" "+commit.Subject)
	}

	spent := l.progress.Spent
	var tokens *harness.Tokens
	if spent.Tokens != (harness.Tokens{}) {
		tokens = &spent.Tokens
	}

	return &state.Entry{
		Status:    status,
		Outcome:   strings.ToLower(string(verdict.Marker)),
		Date:      l.opts.Now().Format(time.RFC3339),
		Agent:     agent.Label(),
		Base:      base,
		Commits:   commits,
		Attempts:  attempts,
		Reason:    verdict.Reason,
		Notes:     verdict.Notes,
		Handover:  handover,
		SessionID: outcome.SessionID,
		CostUSD:   spent.CostUSD,
		Harness:   agent.Harness.Name(),
		ElapsedS:  seconds(spent.Elapsed),
		SessionS:  seconds(spent.Session),
		VerifyS:   seconds(spent.Verify),
		TestS:     seconds(spent.Test),
		Tokens:    tokens,
	}
}

func (l *Loop) commitState(ctx context.Context, p *plan.Plan, s *state.State) error {
	if err := s.Save(); err != nil {
		return err
	}
	path, err := filepath.Rel(l.root, s.Path)
	if err != nil {
		return err
	}
	message := fmt.Sprintf(l.opts.StateCommit, p.Name())
	// The commit that lands the last unit carries the plan's report, which
	// outlives the state file and this checkout's stats log.
	if Complete(p, s) {
		records, _ := stats.Load(l.dir)
		run := l.stats
		run.Ended = l.opts.Now()
		message += "\n\n" + strings.TrimRight(stats.Report(p, s, records, &run), "\n")
	}
	return l.repo.CommitOnly(ctx, path, message)
}

// Complete reports whether every unit of a plan is landed or skipped.
func Complete(p *plan.Plan, s *state.State) bool {
	for _, u := range p.Units {
		if status := s.Status(u.ID); status != state.Landed && status != state.Skipped {
			return false
		}
	}
	return true
}

// settle records a verdict that ends the unit. It returns true when the unit
// is done with, and an error when the run must stop.
func (l *Loop) settle(ctx context.Context, p *plan.Plan, u plan.Unit, agent Agent, verdict Verdict, base string, attempt int, outcome harness.Outcome, tail string) (bool, error) {
	if verdict.Kind == Failed {
		return false, nil
	}

	_, s, err := l.reload()
	if err != nil {
		return false, err
	}

	var notes []string
	handover := l.handover(u, tail, &notes)

	if verdict.Kind == Blocked {
		verdict.Notes = append(verdict.Notes, notes...)
		s.Set(u.ID, l.entry(agent, verdict, base, attempt+1, handover, outcome))
		if err := l.commitState(ctx, p, s); err != nil {
			return false, err
		}
		dirt, _ := l.repo.DirtLines(ctx)
		l.progress.Failure, l.progress.Session, l.progress.LeftDirty = "", nil, len(dirt) > 0
		_ = l.progress.save()

		l.report.UnitDone(u, verdict)
		return false, fmt.Errorf("unit %s is blocked: %s", u.ID, verdict.Reason)
	}

	verdict.Notes = append(verdict.Notes, notes...)
	s.Set(u.ID, l.entry(agent, verdict, base, attempt+1, handover, outcome))
	if err := l.commitState(ctx, p, s); err != nil {
		return false, err
	}
	l.progress.clear()

	summary := verdict.Marker
	if len(verdict.Commits) > 0 {
		var hashes []string
		for _, commit := range verdict.Commits {
			hashes = append(hashes, commit.Short())
		}
		summary = harness.Marker(strings.Join(hashes, " "))
	}
	l.record("complete", string(summary))
	l.report.UnitDone(u, verdict)
	return true, nil
}

// failedAttempt prepares the retry, and gives up when the retries run out.
func (l *Loop) failedAttempt(ctx context.Context, u plan.Unit, verdict Verdict, attempt int) (int, string, error) {
	attempt++
	l.report.Say("check failed: %s", verdict.Reason)
	failure := fmt.Sprintf("Attempt %d failed the loop check:\n\n%s\n\n"+
		"The repository is as that attempt left it, and commits it made still count "+
		"toward this unit. Fix this before anything else.", attempt, verdict.Reason)

	dirt, _ := l.repo.DirtLines(ctx)
	l.progress.Failure, l.progress.Session, l.progress.LeftDirty = failure, nil, len(dirt) > 0
	if err := l.progress.save(); err != nil {
		return attempt, failure, err
	}

	if attempt > l.opts.Retries {
		first, _, _ := strings.Cut(verdict.Reason, "\n")
		return attempt, failure, fmt.Errorf("unit %s failed after %d attempt(s): %s",
			u.ID, attempt, strings.TrimSuffix(first, ":"))
	}
	return attempt, failure, nil
}

// blockedAttempt sends a block back for a second opinion. A block is the one
// claim the repository cannot answer, so it is believed only once a fresh
// session with no memory of the first agrees with it.
func (l *Loop) blockedAttempt(ctx context.Context, verdict Verdict) (string, error) {
	l.report.Say("blocked: %s; asking a fresh session to establish it", verdict.Reason)
	l.record("blocked", "second opinion on: "+verdict.Reason)

	dirt, _ := l.repo.DirtLines(ctx)
	failure := fmt.Sprintf("A previous session stopped at this unit and reported:\n\n"+
		"    BLOCKED %s\n\n"+
		"It had this work order and no more information than you, and the repository is "+
		"as it left it: %s. That claim is not established. Check it against the "+
		"repository before you accept it: if it is wrong, implement the unit; if it "+
		"holds, finish BLOCKED with the same reason and say in the handover what you "+
		"checked.", verdict.Reason, leftBehind(len(verdict.Commits), len(dirt)))

	l.progress.Failure, l.progress.Session, l.progress.LeftDirty = failure, nil, len(dirt) > 0
	return failure, l.progress.save()
}

// leftBehind is what the repository shows for a session that blocked.
func leftBehind(commits, dirt int) string {
	left := "it committed nothing"
	if commits > 0 {
		left = fmt.Sprintf(
			"it left %d commit(s), which still count toward this unit", commits)
	}
	if dirt > 0 {
		left += fmt.Sprintf(", and %d path(s) are left uncommitted", dirt)
	}
	return left
}

// blockedEarlier is what an earlier run's block tells the session that picks
// the unit up again, so a re-run does not start cold.
func blockedEarlier(entry *state.Entry) string {
	if entry == nil || entry.Status != state.Blocked {
		return ""
	}
	reason := entry.Reason
	if reason == "" {
		reason = "no reason given"
	}
	text := fmt.Sprintf("An earlier run stopped at this unit and reported:\n\n"+
		"    BLOCKED %s\n\n"+
		"It is being tried again. The code may have moved since, or the claim may have "+
		"been wrong. Establish it for yourself before accepting it: if it still holds, "+
		"finish BLOCKED with the same reason.", reason)
	if strings.TrimSpace(entry.Handover) != "" {
		text += "\n\nThat session's handover:\n\n" + entry.Handover
	}
	return text
}
