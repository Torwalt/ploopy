package loop

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/state"
)

//go:embed rescue_prompt.md
var rescueTemplate string

const (
	rescueRole        = "rescue"
	rescueLogTail     = 60
	rescueWaitCap     = 2 * time.Hour
	rescueReportLimit = 4000
)

// rescueAttempt is one session this run gave the unit, as the rescue reads it.
type rescueAttempt struct {
	Agent   string
	Given   string // the failure text that attempt started from
	Result  string // what the loop concluded about it
	Outcome harness.Outcome
	Info    sessionInfo
	Stem    string
	Log     string
	Tail    string
}

// rescueCase is everything a rescue session is told about the stop.
type rescueCase struct {
	trigger     string // failed, transient or blocked
	failure     string
	attempts    []rescueAttempt
	tail        string
	base        string
	priorBlock  string
	blockReason string
	number      int
	agent       Agent // the agent that ran it, which a change may have moved since
}

type rescueAction int

const (
	rescueFailure rescueAction = iota
	rescueRetry
	rescueDecided
	rescueBlocked
)

type rescueResult struct {
	action    rescueAction
	reason    string
	report    string
	diagnosis string
	decisions string
	next      string
	wait      time.Duration
}

func (l *Loop) canRescue(rescues int) bool {
	return l.rescue() != nil && rescues < l.opts.Rescues
}

func (l *Loop) rescueReportPath(u plan.Unit, n int) string {
	return filepath.Join(l.handovers, fmt.Sprintf("%s.rescue-%d.md", u.ID, n))
}

func (l *Loop) rescueStem(u plan.Unit, n int) string {
	for index := 0; ; index++ {
		stem := fmt.Sprintf("%s-rescue-%d", u.ID, n)
		if index > 0 {
			stem = fmt.Sprintf("%s-rescue-%d-%d", u.ID, n, index)
		}
		if _, err := os.Stat(filepath.Join(l.logs, stem+".log")); os.IsNotExist(err) {
			return stem
		}
	}
}

// noteAttempt records a session for the rescue's report, before the loop may
// forget about it.
func (l *Loop) noteAttempt(attempts *[]rescueAttempt, agent Agent, given, stem, tail, result string, outcome harness.Outcome, info sessionInfo) {
	log, _ := filepath.Rel(l.root, filepath.Join(l.logs, stem+".log"))
	*attempts = append(*attempts, rescueAttempt{
		Agent: agent.Label(), Given: given, Result: result, Outcome: outcome,
		Info: info, Stem: stem, Log: log, Tail: tail,
	})
}

// runRescue holds one rescue session to its report and marker, and says what
// the run does next.
func (l *Loop) runRescue(ctx context.Context, p *plan.Plan, u plan.Unit, c rescueCase) (rescueResult, error) {
	agent := *l.rescue()
	c.agent = agent
	reportPath := l.rescueReportPath(u, c.number)
	shown, _ := filepath.Rel(l.root, reportPath)

	prompt, err := l.rescuePrompt(ctx, p, u, c, shown)
	if err != nil {
		return rescueResult{}, err
	}
	stem := l.rescueStem(u, c.number)
	logPath := filepath.Join(l.logs, stem+".log")
	if err := os.WriteFile(filepath.Join(l.logs, stem+".prompt"), []byte(prompt), 0o644); err != nil {
		return rescueResult{}, err
	}

	head, err := l.repo.Head(ctx)
	if err != nil {
		return rescueResult{}, err
	}
	dirt, err := l.repo.DirtLines(ctx)
	if err != nil {
		return rescueResult{}, err
	}

	l.record("rescue", fmt.Sprintf("rescue %d for %s, %s: %s", c.number, u.ID, c.trigger, firstLine(c.failure)))
	l.report.Say("rescue %d for %s (%s): %s starts", c.number, u.ID, c.trigger, agent.Label())

	outcome, _, info, err := l.session(ctx, p, u, agent, false, prompt, stem, logPath)
	if err != nil {
		if ctx.Err() != nil {
			return rescueResult{}, ctx.Err()
		}
		return l.rescueFailed(u, c, "the session could not start: "+firstLine(err.Error()), 0), nil
	}
	l.metered(agent, outcome)
	l.accountSession(u, stem, agent, info, outcome, rescueRole, outcome.Reason, rescueRole)

	if ctx.Err() != nil {
		return rescueResult{}, ctx.Err()
	}
	if outcome.Exhausted != "" {
		return l.rescueFailed(u, c, "the session reached the spending cap: "+outcome.Exhausted, outcome.CostUSD), nil
	}
	if outcome.RateLimit != nil {
		return l.rescueFailed(u, c, "the session hit a usage limit: "+outcome.RateLimit.Reason, outcome.CostUSD), nil
	}

	verdict := l.judgeRescue(context.WithoutCancel(ctx), agent, head, dirt, outcome)
	if verdict.Kind != Landed {
		return l.rescueFailed(u, c, verdict.Reason, outcome.CostUSD), nil
	}

	switch outcome.Marker {
	case harness.Retry:
		text, err := l.rescueReport(u, c.number)
		if err != nil {
			return l.rescueFailed(u, c, err.Error(), outcome.CostUSD), nil
		}
		result := rescueResult{
			action:    rescueRetry,
			reason:    outcome.Reason,
			report:    text,
			diagnosis: reportSection(text, "Diagnosis"),
			next:      reportSection(text, "Next attempt"),
			wait:      reportWait(text, l.opts.Backoff),
		}
		if strings.TrimSpace(result.next) == "" {
			return l.rescueFailed(u, c, fmt.Sprintf("the report %s has no Next attempt section", shown), outcome.CostUSD), nil
		}
		l.recordRescue(u, c, "retry", outcome.Reason, text, outcome.CostUSD)
		l.rescueSaid(u, c.number, "RETRY", outcome.Reason)
		return result, nil

	case harness.Decided:
		text, err := l.rescueReport(u, c.number)
		if err != nil {
			return l.rescueFailed(u, c, err.Error(), outcome.CostUSD), nil
		}
		result := rescueResult{
			action:    rescueDecided,
			reason:    outcome.Reason,
			report:    text,
			diagnosis: reportSection(text, "Diagnosis"),
			decisions: reportSection(text, "Decisions"),
			next:      reportSection(text, "Next attempt"),
		}
		if strings.TrimSpace(result.decisions) == "" {
			return l.rescueFailed(u, c, fmt.Sprintf("the report %s has no Decisions section", shown), outcome.CostUSD), nil
		}
		l.recordRescue(u, c, "decided", outcome.Reason, text, outcome.CostUSD)
		l.rescueSaid(u, c.number, "DECIDED", outcome.Reason)
		return result, nil

	case harness.Blocked:
		reason := outcome.Reason
		if reason == "" {
			reason = "the rescue blocked without a reason"
		}
		result := rescueResult{action: rescueBlocked, reason: reason}
		if text, err := l.rescueReport(u, c.number); err == nil {
			result.report = text
			result.diagnosis = reportSection(text, "Diagnosis")
		}
		l.recordRescue(u, c, "blocked", reason, result.report, outcome.CostUSD)
		l.rescueSaid(u, c.number, "BLOCKED", reason)
		return result, nil
	}

	return l.rescueFailed(u, c, "the final message did not end with RETRY, DECIDED or BLOCKED", outcome.CostUSD), nil
}

// rescueFailure keeps the text the next attempt will read if the run does not
// survive the wait.
func (l *Loop) saveRescueFailure(failure string) error {
	l.progress.Failure, l.progress.Session = failure, nil
	return l.progress.save()
}

func (l *Loop) rescueFailed(u plan.Unit, c rescueCase, why string, cost float64) rescueResult {
	l.record(rescueRole, fmt.Sprintf("rescue %d for %s failed: %s", c.number, c.trigger, firstLine(why)))
	l.report.Say("rescue %d failed: %s", c.number, firstLine(why))
	text := ""
	if report, err := l.rescueReport(u, c.number); err == nil {
		text = report
	}
	l.recordRescue(u, c, "failed", why, text, cost)
	return rescueResult{action: rescueFailure, reason: why}
}

// recordRescue puts one rescue on the unit's record and saves it with the
// progress that survives a crash.
func (l *Loop) recordRescue(u plan.Unit, c rescueCase, verdict, reason, report string, cost float64) {
	decisions := ""
	if verdict == "decided" {
		decisions = reportSection(report, "Decisions")
	}
	if len(report) > rescueReportLimit {
		report = report[:rescueReportLimit]
	}
	l.progress.Rescues = append(l.progress.Rescues, state.Rescue{
		Trigger:   c.trigger,
		Verdict:   verdict,
		Agent:     c.agent.Label(),
		Reason:    reason,
		Report:    report,
		Decisions: decisions,
		Date:      l.opts.Now().Format(time.RFC3339),
		CostUSD:   cost,
	})
	_ = l.progress.save()
}

func (l *Loop) rescueSaid(u plan.Unit, n int, marker, reason string) {
	l.record(rescueRole, fmt.Sprintf("rescue %d for %s answered %s: %s", n, u.ID, marker, reason))
	l.report.Say("rescue %d answered %s: %s", n, marker, reason)
}

func (l *Loop) rescueReport(u plan.Unit, n int) (string, error) {
	text, err := os.ReadFile(l.rescueReportPath(u, n))
	if err != nil || strings.TrimSpace(string(text)) == "" {
		shown, _ := filepath.Rel(l.root, l.rescueReportPath(u, n))
		return "", fmt.Errorf("the session wrote no report to %s", shown)
	}
	return string(text), nil
}

func (l *Loop) judgeRescue(ctx context.Context, agent Agent, base string, dirt []string, outcome harness.Outcome) Verdict {
	commits, err := l.repo.Commits(ctx, base)
	if err != nil {
		return Verdict{Kind: Failed, Reason: err.Error()}
	}
	if len(commits) > 0 {
		var listing []string
		for _, commit := range commits {
			listing = append(listing, commit.Short())
		}
		return Verdict{Kind: Failed, Reason: "a rescue must change nothing, but it committed:\n" +
			strings.Join(listing, "\n") + "\nThe commits stay for the author; the run stops here"}
	}

	changed, err := l.touched(ctx, base, dirt)
	if err != nil {
		return Verdict{Kind: Failed, Reason: err.Error()}
	}
	if changed {
		current, err := l.repo.DirtLines(ctx)
		if err != nil {
			return Verdict{Kind: Failed, Reason: err.Error()}
		}
		return Verdict{Kind: Failed, Reason: "the working tree is dirty after the rescue, and a rescue " +
			"changes nothing. The changes stay for the author; the run stops here:\n" +
			strings.Join(head(current, 20), "\n")}
	}

	switch outcome.Marker {
	case harness.Retry, harness.Decided, harness.Blocked:
		return Verdict{Kind: Landed, Reason: outcome.Reason, Marker: outcome.Marker}
	}
	return Verdict{Kind: Failed, Reason: fmt.Sprintf(
		"the final message did not end with RETRY, DECIDED or BLOCKED (%s exited %d)",
		agent.Harness.Name(), outcome.Exit)}
}

func (l *Loop) rescuePrompt(ctx context.Context, p *plan.Plan, u plan.Unit, c rescueCase, report string) (string, error) {
	commits, err := l.repo.Commits(ctx, c.base)
	if err != nil {
		return "", err
	}
	var log strings.Builder
	for _, commit := range commits {
		log.WriteString(commit.Short() + "\n")
	}
	dirt, err := l.repo.DirtLines(ctx)
	if err != nil {
		return "", err
	}

	handover := "None was recorded."
	if text, err := os.ReadFile(l.handoverPath(u)); err == nil && strings.TrimSpace(string(text)) != "" {
		handover = strings.TrimSpace(string(text))
	}

	latest := ""
	if len(c.attempts) > 0 {
		latest = c.attempts[len(c.attempts)-1].Stem
	}
	verify, test := l.lastCheckLogs(latest)
	tail := c.tail
	if latest != "" {
		tail = l.sessionTail(latest, tail)
	}

	return fill(rescueTemplate, map[string]string{
		"unit":       u.ID,
		"title":      u.Title,
		"plan":       p.Path,
		"trigger":    c.trigger,
		"failure":    orDefault(strings.TrimSpace(c.failure), "None."),
		"skill":      skillBody(l.opts.RescueSkill),
		"work_order": strings.TrimSpace(p.WorkOrder(u)),
		"attempts":   rescueAttempts(c.attempts),
		"base":       c.base,
		"commits":    strings.TrimRight(log.String(), "\n"),
		"dirty":      strings.Join(dirt, "\n"),
		"handover":   handover,
		"verify_log": verify,
		"test_log":   test,
		"block":      rescueBlock(c),
		"tail":       tail,
		"report":     report,
	}), nil
}

func rescueAttempts(attempts []rescueAttempt) string {
	if len(attempts) == 0 {
		return "None."
	}
	var b strings.Builder
	for i, a := range attempts {
		fmt.Fprintf(&b, "### Attempt %d\n\n", i+1)
		fmt.Fprintf(&b, "- agent: %s\n", orDefault(a.Agent, "unknown"))
		if a.Result != "" {
			fmt.Fprintf(&b, "- result: %s\n", a.Result)
		}
		if a.Given != "" {
			fmt.Fprintf(&b, "- it was told: %s\n", strings.ReplaceAll(a.Given, "\n", " "))
		}
		fmt.Fprintf(&b, "- exit: %d, timed out: %t\n", a.Outcome.Exit, a.Outcome.TimedOut)
		fmt.Fprintf(&b, "- marker: %s\n", orDefault(string(a.Outcome.Marker), "none"))
		if a.Outcome.Tokens != (harness.Tokens{}) {
			fmt.Fprintf(&b, "- tokens: %d read, %d written\n",
				a.Outcome.Tokens.Prompt(), a.Outcome.Tokens.Generated())
		}
		if len(a.Outcome.Degraded) > 0 {
			fmt.Fprintf(&b, "- adapter notes: %s\n", strings.Join(a.Outcome.Degraded, "; "))
		}
		if a.Log != "" {
			fmt.Fprintf(&b, "- log: `%s`\n", a.Log)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func rescueBlock(c rescueCase) string {
	if c.trigger != "blocked" {
		return ""
	}
	var b strings.Builder
	b.WriteString("## The block\n\n")
	if c.priorBlock != "" {
		fmt.Fprintf(&b, "The first blocking session said:\n\n    BLOCKED %s\n\n", c.priorBlock)
	}
	if c.blockReason != "" {
		fmt.Fprintf(&b, "The second opinion agreed:\n\n    BLOCKED %s\n", c.blockReason)
	}
	return strings.TrimRight(b.String(), "\n")
}

// reportSection is the body under a `## Name` heading, up to the next one.
func reportSection(text, name string) string {
	var out []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "## ") {
			in = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "## ")), name)
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// reportWait is the optional `Wait:` line a RETRY report carries, capped.
func reportWait(text string, fallback time.Duration) time.Duration {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(trimmed), "wait:") {
			continue
		}
		value := strings.TrimSpace(trimmed[len("wait:"):])
		if index := strings.Index(value, "#"); index >= 0 {
			value = strings.TrimSpace(value[:index])
		}
		wait, err := time.ParseDuration(value)
		if err != nil || wait <= 0 {
			return fallback
		}
		if wait > rescueWaitCap {
			return rescueWaitCap
		}
		return wait
	}
	return fallback
}

// sessionTail is the end of a session's log, for the latest attempt.
func (l *Loop) sessionTail(stem, fallback string) string {
	text, err := os.ReadFile(filepath.Join(l.logs, stem+".log"))
	if err != nil {
		text = []byte(fallback)
	}
	lines := strings.Split(strings.TrimRight(string(text), "\n"), "\n")
	if len(lines) > rescueLogTail {
		lines = lines[len(lines)-rescueLogTail:]
	}
	if strings.TrimSpace(strings.Join(lines, "")) == "" {
		return "The session log is empty."
	}
	return strings.Join(lines, "\n")
}

// lastCheckLogs names the latest verify and test logs a rescue can read.
func (l *Loop) lastCheckLogs(stem string) (string, string) {
	if stem == "" {
		return "None.", "None."
	}
	pick := func(suffixes ...string) string {
		for i := len(suffixes) - 1; i >= 0; i-- {
			path := filepath.Join(l.logs, stem+"."+suffixes[i])
			if _, err := os.Stat(path); err == nil {
				if shown, err := filepath.Rel(l.root, path); err == nil {
					return "`" + shown + "`"
				}
				return "`" + path + "`"
			}
		}
		return "None."
	}
	return pick("verify", "verify-rerun"), pick("test", "test-rerun")
}

func diagnosed(previous, next string) string {
	if strings.TrimSpace(previous) == "" {
		previous = "The run stopped at this unit."
	}
	return strings.TrimSpace(previous) + "\n\nA rescue diagnosed this:\n\n" + strings.TrimSpace(next)
}

func decided(decisions, next string) string {
	text := "The run stopped at this unit and a rescue, standing in for the author, decided what it " +
		"was waiting on. These decisions are settled: do not reopen them, and record them in the handover."
	text += "\n\n## Decisions\n\n" + strings.TrimSpace(decisions)
	if strings.TrimSpace(next) != "" {
		text += "\n\n## Next attempt\n\n" + strings.TrimSpace(next)
	}
	return text
}
