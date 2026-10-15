// Package land puts a finished branch on the default branch: it rebases the
// branch, runs the checks and fast-forwards. Where git stops on a conflict or
// a check fails, a session repairs it, and git decides whether it did.
package land

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/repo"
)

//go:embed prompt.md
var promptTemplate string

// Reporter is everything a landing says.
type Reporter interface {
	Say(format string, args ...any)
	Fail(format string, args ...any)
	Event(e harness.Event)
}

// Agent is who repairs a landing.
type Agent struct {
	Harness harness.Harness
	Model   string
	Effort  string
	Budget  float64
}

// Options is one landing.
type Options struct {
	Root        string // the checkout holding the branch
	Branch      string
	Onto        string
	AuthorPaths []string

	Verify string
	Test   string

	Agent    *Agent // nil stops where a session would repair
	Skill    string
	Context  []string
	Deny     []string
	Extra    []string
	Timeout  time.Duration
	Attempts int // sessions per repair
	Rounds   int // rebases when onto moves during the checks
	Logs     string

	Report Reporter
	Now    func() time.Time
}

// Result is what a landing came to.
type Result struct {
	Before   string // the branch's commit before the first rebase
	Sessions int
	CostUSD  float64
}

func (o *Options) defaults() {
	if o.Attempts == 0 {
		o.Attempts = 3
	}
	if o.Rounds == 0 {
		o.Rounds = 3
	}
	if o.Timeout == 0 {
		o.Timeout = 90 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

type lander struct {
	o      Options
	r      *repo.Repo
	result Result
}

// Run lands the branch checked out at Root on Onto. On failure the branch is
// left rebased or as it was, and Onto as it was.
func Run(ctx context.Context, o Options) (Result, error) {
	o.defaults()
	l := &lander{o: o, r: repo.New(o.Root, o.AuthorPaths)}
	before, err := l.r.Head(ctx)
	if err != nil {
		return l.result, err
	}
	l.result.Before = before

	for round := 1; ; round++ {
		if err := l.rebase(ctx); err != nil {
			return l.result, err
		}
		if err := l.checks(ctx); err != nil {
			return l.result, err
		}
		err := l.r.FastForward(ctx, o.Branch, o.Onto)
		if !errors.Is(err, repo.ErrMoved) {
			return l.result, err
		}
		if round == o.Rounds {
			return l.result, fmt.Errorf("%s moved %d times while %s was being checked; land it again", o.Onto, round, o.Branch)
		}
		o.Report.Say("%s moved during the checks; rebasing again", o.Onto)
	}
}

// rebase replays the branch onto Onto, a session resolving each stop.
func (l *lander) rebase(ctx context.Context) error {
	o := l.o
	if l.r.IsAncestor(ctx, o.Onto, "HEAD") {
		o.Report.Say("%s already holds %s; nothing to rebase", o.Branch, o.Onto)
		return nil
	}
	o.Report.Say("rebasing %s onto %s", o.Branch, o.Onto)
	stopped, err := l.r.Rebase(ctx, o.Onto)
	for err == nil && stopped {
		if err = l.resolve(ctx); err != nil {
			break
		}
		stopped, err = l.r.ContinueRebase(ctx)
	}
	if err != nil {
		if l.r.Rebasing(ctx) {
			if abort := l.r.AbortRebase(ctx); abort != nil {
				return fmt.Errorf("%w; aborting the rebase failed too: %w", err, abort)
			}
			o.Report.Say("aborted the rebase; %s is as it was", o.Branch)
		}
		return err
	}
	return nil
}

// resolve has a session resolve the stop the rebase is at.
func (l *lander) resolve(ctx context.Context) error {
	o := l.o
	paths, err := l.r.Unresolved(ctx)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("the rebase stopped without a conflict; finish it by hand")
	}
	replaying := l.r.Show(ctx, "REBASE_HEAD")
	o.Report.Say("conflict replaying %s in %s", firstLine(replaying), strings.Join(paths, ", "))
	if o.Agent == nil {
		return fmt.Errorf("a conflict in %s, and no agent to resolve it", strings.Join(paths, ", "))
	}

	failure := ""
	for attempt := 1; attempt <= o.Attempts; attempt++ {
		head, err := l.r.Head(ctx)
		if err != nil {
			return err
		}
		task := l.conflictTask(ctx, paths, replaying)
		outcome, err := l.session(ctx, "conflict", attempt, task, failure)
		if err != nil {
			return err
		}
		if outcome.Marker == harness.Blocked {
			return fmt.Errorf("the session is blocked: %s", outcome.Reason)
		}
		failure = l.judgeResolve(ctx, head, paths, outcome)
		if failure == "" {
			return nil
		}
		o.Report.Fail("the conflict is not resolved: %s", failure)
		if !l.r.Rebasing(ctx) {
			return errors.New("the rebase is no longer going on")
		}
	}
	return fmt.Errorf("%d sessions did not resolve the conflict in %s", o.Attempts, strings.Join(paths, ", "))
}

func (l *lander) judgeResolve(ctx context.Context, head string, paths []string, outcome harness.Outcome) string {
	if now, err := l.r.Head(ctx); err != nil || now != head {
		return "the session committed; resolve and `git add`, and leave the commit to the rebase"
	}
	left, err := l.r.Unresolved(ctx)
	if err != nil {
		return err.Error()
	}
	if len(left) > 0 {
		return "still unresolved: " + strings.Join(left, ", ")
	}
	if marked := l.r.Markers(paths); len(marked) > 0 {
		return "conflict markers left in " + strings.Join(marked, ", ")
	}
	if outcome.Marker != harness.Done {
		return "the session did not finish with DONE"
	}
	return ""
}

// checks runs the checks on the rebased branch, a session repairing a
// failure with commits of its own.
func (l *lander) checks(ctx context.Context) error {
	o := l.o
	failure := l.runChecks(ctx, "checks")
	if failure == "" {
		return nil
	}
	if o.Agent == nil {
		return fmt.Errorf("%s, and no agent to repair it", firstLine(failure))
	}

	previous := ""
	for attempt := 1; attempt <= o.Attempts; attempt++ {
		head, err := l.r.Head(ctx)
		if err != nil {
			return err
		}
		outcome, err := l.session(ctx, "checks", attempt, l.checksTask(ctx, failure), previous)
		if err != nil {
			return err
		}
		if outcome.Marker == harness.Blocked {
			return fmt.Errorf("the session is blocked: %s", outcome.Reason)
		}
		previous = l.judgeRepair(ctx, head, outcome)
		if previous == "" {
			if failure = l.runChecks(ctx, fmt.Sprintf("repair-%d", attempt)); failure == "" {
				return nil
			}
			previous = "the checks still fail:\n" + failure
		}
		o.Report.Fail("not repaired: %s", firstLine(previous))
	}
	return fmt.Errorf("%d sessions did not make the checks pass on the rebased %s", o.Attempts, o.Branch)
}

func (l *lander) judgeRepair(ctx context.Context, head string, outcome harness.Outcome) string {
	now, err := l.r.Head(ctx)
	switch {
	case err != nil:
		return err.Error()
	case now == head:
		return "the session committed nothing"
	case !l.r.IsAncestor(ctx, head, now):
		return "the session rewrote the branch; add commits on top of it instead"
	case l.r.Rebasing(ctx):
		return "the session left a rebase going on"
	}
	if dirt, err := l.r.DirtLines(ctx); err != nil {
		return err.Error()
	} else if len(dirt) > 0 {
		return "the session left the tree dirty:\n" + strings.Join(dirt, "\n")
	}
	if outcome.Marker != harness.Done {
		return "the session did not finish with DONE"
	}
	return ""
}

// runChecks runs verify, then test, each once more before calling it failed.
func (l *lander) runChecks(ctx context.Context, stem string) string {
	for _, check := range []struct{ name, command string }{{"verify", l.o.Verify}, {"test", l.o.Test}} {
		if check.command == "" {
			continue
		}
		l.o.Report.Say("checking the rebased %s: `%s`", l.o.Branch, check.command)
		var logPath string
		passed := false
		for attempt := 1; attempt <= 2 && !passed; attempt++ {
			logPath = filepath.Join(l.o.Logs, fmt.Sprintf("%s.%s.%d", stem, check.name, attempt))
			passed = l.runCheck(ctx, check.command, logPath) == nil
			if !passed && attempt == 1 && ctx.Err() == nil {
				l.o.Report.Say("`%s` failed; running it once more", check.command)
			}
		}
		if ctx.Err() != nil {
			return fmt.Sprintf("`%s` was interrupted", check.command)
		}
		if !passed {
			return fmt.Sprintf("`%s` failed twice:\n%s", check.command, logTail(logPath))
		}
	}
	return ""
}

func (l *lander) runCheck(ctx context.Context, command, logPath string) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = l.o.Root
	cmd.Stdout = log
	cmd.Stderr = log
	return cmd.Run()
}

const checkTailLines = 60

func logTail(path string) string {
	output, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(lines) > checkTailLines {
		lines = lines[len(lines)-checkTailLines:]
	}
	return strings.Join(lines, "\n")
}

// session runs one repair and streams it to the reporter.
func (l *lander) session(ctx context.Context, kind string, attempt int, task, failure string) (harness.Outcome, error) {
	o := l.o
	stem := fmt.Sprintf("%s-%s-%d", kind, o.Now().Format("20060102-150405"), attempt)
	logPath := filepath.Join(o.Logs, stem+".log")
	if err := os.MkdirAll(o.Logs, 0o755); err != nil {
		return harness.Outcome{}, err
	}
	o.Report.Say("session %d of %d with %s · %s", attempt, o.Attempts, o.Agent.Harness.Name(), logPath)
	spec := harness.Spec{
		Prompt:       l.prompt(task, failure),
		Model:        o.Agent.Model,
		Effort:       o.Agent.Effort,
		Title:        fmt.Sprintf("ploopy land %s %s", o.Branch, kind),
		SessionID:    uuid.NewString(),
		Dir:          o.Root,
		Deny:         o.Deny,
		Extra:        o.Extra,
		Timeout:      o.Timeout,
		MaxBudgetUSD: o.Agent.Budget,
		LogPath:      logPath,
	}
	session, err := o.Agent.Harness.Start(ctx, spec)
	if err != nil {
		return harness.Outcome{}, err
	}
	for event := range session.Events() {
		o.Report.Event(event)
	}
	outcome, err := session.Wait()
	l.result.Sessions++
	l.result.CostUSD += outcome.CostUSD
	if len(outcome.Degraded) > 0 {
		o.Report.Say("⚠ the %s adapter could not read all of what the harness reported: %s",
			o.Agent.Harness.Name(), strings.Join(outcome.Degraded, "; "))
	}
	if err != nil {
		return outcome, err
	}
	if ctx.Err() != nil {
		return outcome, ctx.Err()
	}
	return outcome, nil
}

var (
	placeholderRe = regexp.MustCompile(`\{\{(\w+)\}\}`)
	frontMatterRe = regexp.MustCompile(`(?s)\A---\n.*?\n---\n+`)
)

func (l *lander) prompt(task, failure string) string {
	if strings.TrimSpace(failure) == "" {
		failure = "None. This is the first attempt."
	}
	values := map[string]string{
		"branch":  l.o.Branch,
		"onto":    l.o.Onto,
		"skill":   strings.TrimSpace(frontMatterRe.ReplaceAllString(l.o.Skill, "")),
		"context": reading(l.o.Context),
		"failure": strings.TrimSpace(failure),
		"task":    strings.TrimSpace(task),
	}
	return placeholderRe.ReplaceAllStringFunc(promptTemplate, func(match string) string {
		return values[placeholderRe.FindStringSubmatch(match)[1]]
	})
}

func reading(docs []string) string {
	if len(docs) == 0 {
		return "* Nothing beyond this prompt."
	}
	lines := make([]string, 0, len(docs))
	for _, doc := range docs {
		lines = append(lines, "* `"+doc+"`")
	}
	return strings.Join(lines, "\n")
}

const logLimit = 60

func (l *lander) conflictTask(ctx context.Context, paths []string, replaying string) string {
	o := l.o
	var b strings.Builder
	fmt.Fprintf(&b, "## A conflict\n\n`git rebase %s` stopped replaying a commit of `%s`. These paths conflict:\n\n", o.Onto, o.Branch)
	for _, path := range paths {
		fmt.Fprintf(&b, "* `%s`\n", path)
	}
	fmt.Fprintf(&b, "\nResolve each one so that both sides' intent survives, then `git add` it. "+
		"Do not commit and do not run `git rebase`: ploopy continues the rebase once every path is "+
		"resolved, and stops here again at the next conflict.\n\n")
	fmt.Fprintf(&b, "### The commit being replayed\n\n```\n%s\n```\n\n", replaying)
	fmt.Fprintf(&b, "### What `%s` gained since the branch left it\n\n```\n%s\n```\n", o.Onto, l.gained(ctx))
	return b.String()
}

func (l *lander) checksTask(ctx context.Context, failure string) string {
	o := l.o
	var b strings.Builder
	fmt.Fprintf(&b, "## A failing check\n\n`%s` is rebased onto `%s` without a conflict, but a check fails on it. "+
		"Both sides passed on their own, so the failure is where the branch's work and what `%s` gained "+
		"meet. Find that seam and repair it in one or more commits on the branch, following the "+
		"repository's own commit conventions. Do not rewrite the branch's existing commits.\n\n", o.Branch, o.Onto, o.Onto)
	var checks []string
	for _, command := range []string{o.Verify, o.Test} {
		if command != "" {
			checks = append(checks, "`"+command+"`")
		}
	}
	if len(checks) > 0 {
		fmt.Fprintf(&b, "%s must be green before you finish; ploopy runs them after you.\n\n", strings.Join(checks, " and "))
	}
	fmt.Fprintf(&b, "### The failure\n\n```\n%s\n```\n\n", failure)
	fmt.Fprintf(&b, "### What `%s` gained since the branch left it\n\n```\n%s\n```\n\n", o.Onto, l.gained(ctx))
	fmt.Fprintf(&b, "### What the branch carries\n\n```\n%s\n```\n", orNone(l.r.Log(ctx, o.Onto, "HEAD", logLimit)))
	return b.String()
}

// gained is what Onto has that the branch, as it was before the rebase, did not.
func (l *lander) gained(ctx context.Context) string {
	return orNone(l.r.Log(ctx, l.result.Before, l.o.Onto, logLimit))
}

func orNone(text string) string {
	if text == "" {
		return "(nothing)"
	}
	return text
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}
