package loop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
)

// Verdict kinds. A unit either landed, failed this attempt, or is blocked.
const (
	Landed  = "landed"
	Failed  = "failed"
	Blocked = "blocked"
)

// Verdict is what the repository says about a session's work.
type Verdict struct {
	Kind    string
	Reason  string
	Marker  harness.Marker
	Commits []repo.Commit
	Notes   []string
}

const checkTailLines = 40

// judge decides from the repository whether a unit landed. The order of these
// checks is the contract; a session's own account of itself only ever opens
// the question.
func (l *Loop) judge(ctx context.Context, p *plan.Plan, u plan.Unit, base string, outcome harness.Outcome, stem string) Verdict {
	if outcome.Marker == harness.Blocked {
		return l.blocked(ctx, outcome, base)
	}
	if outcome.TimedOut {
		return Verdict{Kind: Failed, Reason: fmt.Sprintf(
			"the session hit the %s wall clock and was killed", l.opts.Timeout)}
	}

	dirt, err := l.repo.DirtLines(ctx)
	if err != nil {
		return Verdict{Kind: Failed, Reason: err.Error()}
	}
	if len(dirt) > 0 {
		listing := dirt
		if len(listing) > 20 {
			listing = listing[:20]
		}
		return Verdict{Kind: Failed, Reason: "the working tree is dirty after the session. " +
			"Commit what belongs to the unit; leave changes you did not make alone and name them " +
			"in the handover:\n" + strings.Join(listing, "\n")}
	}

	if outcome.Marker == harness.None {
		return Verdict{Kind: Failed, Reason: fmt.Sprintf(
			"the final message did not end with DONE, NOTHING-TO-DO or BLOCKED (%s exited %d)",
			l.opts.Harness.Name(), outcome.Exit)}
	}

	commits, err := l.repo.Commits(ctx, base)
	if err != nil {
		return Verdict{Kind: Failed, Reason: err.Error()}
	}
	if outcome.Marker == harness.Done && len(commits) == 0 {
		short := base
		if len(short) > 10 {
			short = short[:10]
		}
		return Verdict{Kind: Failed, Reason: fmt.Sprintf(
			"the session reported DONE but nothing was committed since the unit started at %s; "+
				"when the outcome already exists, finish with NOTHING-TO-DO", short)}
	}

	var notes []string
	if outcome.Exit != 0 {
		notes = append(notes, fmt.Sprintf("%s exited %d after reporting %s",
			l.opts.Harness.Name(), outcome.Exit, outcome.Marker))
	}

	failure := l.check(ctx, "verify", l.opts.VerifyCmd, stem, &notes)
	if failure == "" && l.wantsTests(p, u) {
		failure = l.check(ctx, "test", l.opts.TestCmd, stem, &notes)
	}
	if failure != "" {
		return Verdict{Kind: Failed, Reason: failure}
	}

	return Verdict{Kind: Landed, Reason: outcome.Reason, Marker: outcome.Marker, Commits: commits, Notes: notes}
}

// blocked is the one verdict the repository cannot answer: a claim about what
// cannot be done. What the session left behind is read all the same, so a
// second opinion and the record start from more than the claim. This is
// evidence, never the verdict, so what git cannot answer is simply left out.
func (l *Loop) blocked(ctx context.Context, outcome harness.Outcome, base string) Verdict {
	reason := outcome.Reason
	if reason == "" {
		reason = "no reason given"
	}
	verdict := Verdict{Kind: Blocked, Reason: reason, Marker: outcome.Marker}
	verdict.Commits, _ = l.repo.Commits(ctx, base)
	if len(verdict.Commits) > 0 {
		verdict.Notes = append(verdict.Notes, fmt.Sprintf(
			"the session committed %d time(s) before blocking", len(verdict.Commits)))
	}
	if dirt, _ := l.repo.DirtLines(ctx); len(dirt) > 0 {
		verdict.Notes = append(verdict.Notes, fmt.Sprintf(
			"the session left %d uncommitted path(s)", len(dirt)))
	}
	return verdict
}

// check runs a repository command, and runs it a second time before blaming
// the session for a failure that does not reproduce.
func (l *Loop) check(ctx context.Context, name, command, stem string, notes *[]string) string {
	if command == "" {
		return ""
	}
	l.report.Say("checking the unit: `%s`", command)

	var logPath string
	for attempt := 1; attempt <= 2; attempt++ {
		suffix := ""
		if attempt == 2 {
			suffix = "-rerun"
		}
		logPath = filepath.Join(l.logs, stem+"."+name+suffix)
		if l.runCheck(ctx, command, logPath) == nil {
			if attempt == 2 {
				*notes = append(*notes, fmt.Sprintf("`%s` failed once and passed on a rerun", command))
			}
			return ""
		}
		if ctx.Err() != nil {
			return fmt.Sprintf("`%s` was interrupted", command)
		}
		if attempt == 1 {
			l.report.Say("`%s` failed; running it once more before blaming the session", command)
		}
	}

	output, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(lines) > checkTailLines {
		lines = lines[len(lines)-checkTailLines:]
	}
	return fmt.Sprintf("`%s` failed twice:\n%s", command, strings.Join(lines, "\n"))
}

func (l *Loop) runCheck(ctx context.Context, command, logPath string) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer log.Close()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = l.root
	cmd.Stdout = log
	cmd.Stderr = log
	return cmd.Run()
}
