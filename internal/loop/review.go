package loop

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
)

//go:embed review_prompt.md
var reviewTemplate string

//go:embed triage_prompt.md
var triageTemplate string

const noHandover = "None was recorded."

// reviewPrompt is what a unit of a review plan starts from. A collecting unit
// is handed one landed unit's work order, handover and commits; the seams unit
// the span of all of them; the triage unit every finding the others wrote.
func (l *Loop) reviewPrompt(ctx context.Context, p *plan.Plan, s *state.State, u plan.Unit, failure string) (string, error) {
	rp, rs, err := l.reviewed(p)
	if err != nil {
		return "", err
	}
	handover, _ := filepath.Rel(l.root, l.handoverPath(u))
	failure = strings.TrimSpace(orDefault(failure, noFailure))

	if u.Role() == plan.Triage {
		return fill(triageTemplate, map[string]string{
			"unit":           u.ID,
			"title":          u.Title,
			"plan":           p.Path,
			"reviewed":       rp.Path,
			"fixes":          review.FixesPath(rp.Path),
			"author_heading": review.AuthorHeading,
			"skill":          skillBody(l.opts.TriageSkill),
			"context":        reading(l.reviewContext(rp, nil, false)),
			"handover":       handover,
			"failure":        failure,
			"findings":       l.collected(p, s, rp, rs),
		}), nil
	}

	var target string
	var docs []string
	if u.Role() == plan.Seams {
		target, err = l.seamsTarget(ctx, p, rp, rs)
		docs = l.reviewContext(rp, nil, true)
	} else {
		ru := rp.Unit(u.Reviewed())
		if ru == nil {
			return "", fmt.Errorf("unit %s reviews unit %s, which %s no longer has", u.ID, u.Reviewed(), rp.Path)
		}
		target, err = l.collectTarget(ctx, rp, rs, *ru)
		docs = l.reviewContext(rp, ru, false)
	}
	if err != nil {
		return "", err
	}
	return fill(reviewTemplate, map[string]string{
		"unit":      u.ID,
		"title":     u.Title,
		"plan":      p.Path,
		"skill":     skillBody(l.opts.ReviewSkill),
		"checklist": checklistSection(l.opts.Checklist),
		"context":   reading(docs),
		"findings":  handover,
		"failure":   failure,
		"target":    target,
	}), nil
}

// reviewed is the plan a review plan reviews, and its progress.
func (l *Loop) reviewed(p *plan.Plan) (*plan.Plan, *state.State, error) {
	path := p.Reviews()
	if path == "" {
		return nil, nil, fmt.Errorf("%s has review units but names no plan it `reviews:`", p.Path)
	}
	rp, err := plan.Load(filepath.Join(l.root, path), path)
	if err != nil {
		return nil, nil, fmt.Errorf("the reviewed plan %s: %w", path, err)
	}
	rs, err := state.Load(filepath.Join(l.root, state.PathFor(path)))
	if err != nil {
		return nil, nil, err
	}
	return rp, rs, nil
}

// reviewContext is what a review session reads first: what the reviewed work
// was written against, and for the seams the reviewed plan itself.
func (l *Loop) reviewContext(rp *plan.Plan, ru *plan.Unit, withPlan bool) []string {
	docs := l.opts.Context
	if len(docs) == 0 {
		docs = rp.Context(l.root, ru, l.opts.BaseContext)
	}
	if withPlan {
		docs = append(append([]string(nil), docs...), rp.Path)
	}
	var out []string
	for _, doc := range docs {
		if info, err := os.Stat(filepath.Join(l.root, doc)); err == nil && info.Mode().IsRegular() {
			out = append(out, doc)
		}
	}
	return out
}

// span is the range of commits a landed unit made: from the head it started
// at to its last commit.
func span(entry *state.Entry) (string, string) {
	first, _, _ := strings.Cut(entry.Commits[0], " ")
	last, _, _ := strings.Cut(entry.Commits[len(entry.Commits)-1], " ")
	from := entry.Base
	if from == "" {
		from = first + "^"
	}
	return short(from), short(last)
}

func short(hash string) string {
	if len(hash) > 12 && !strings.ContainsAny(hash, "^~") {
		return hash[:12]
	}
	return hash
}

func landedEntry(rs *state.State, id string) *state.Entry {
	entry := rs.Entry(id)
	if entry == nil || entry.Status != state.Landed || len(entry.Commits) == 0 {
		return nil
	}
	return entry
}

func (l *Loop) collectTarget(ctx context.Context, rp *plan.Plan, rs *state.State, ru plan.Unit) (string, error) {
	entry := landedEntry(rs, ru.ID)
	if entry == nil {
		return "", fmt.Errorf("unit %s of %s has no landed commits to review", ru.ID, rp.Path)
	}
	from, to := span(entry)
	stat, err := l.repo.DiffStat(ctx, from, to)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Unit %s, *%s*, of `%s`", ru.ID, ru.Title, rp.Path)
	if entry.Agent != "" {
		fmt.Fprintf(&b, ", landed by %s", entry.Agent)
	}
	b.WriteString(".\n\n### Its commits\n\n")
	for _, commit := range entry.Commits {
		hash, subject, _ := strings.Cut(commit, " ")
		fmt.Fprintf(&b, "- %s %s\n", short(hash), subject)
	}
	fmt.Fprintf(&b, "\nThe whole change is `git diff %s..%s`:\n\n```\n%s\n```\n\n", from, to, stat)
	fmt.Fprintf(&b, "### Its work order\n\n%s\n\n", strings.TrimSpace(rp.WorkOrder(ru)))
	fmt.Fprintf(&b, "### Its handover\n\n%s\n", orDefault(strings.TrimSpace(entry.Handover), noHandover))
	return b.String(), nil
}

// covered is the reviewed units this review plan has a collecting unit for.
func covered(p, rp *plan.Plan, rs *state.State) []plan.Unit {
	var units []plan.Unit
	for _, u := range p.Units {
		if u.Role() != plan.Collect {
			continue
		}
		if ru := rp.Unit(u.Reviewed()); ru != nil && landedEntry(rs, ru.ID) != nil {
			units = append(units, *ru)
		}
	}
	return units
}

func (l *Loop) seamsTarget(ctx context.Context, p, rp *plan.Plan, rs *state.State) (string, error) {
	units := covered(p, rp, rs)
	if len(units) == 0 {
		return "", fmt.Errorf("%s covers no landed unit of %s", p.Path, rp.Path)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "The seams between the %d units of `%s` this review covers. Each unit has a\n"+
		"review session of its own; yours is only what lies between them.\n\n", len(units), rp.Path)
	for _, ru := range units {
		entry := landedEntry(rs, ru.ID)
		from, to := span(entry)
		fmt.Fprintf(&b, "- %s *%s* — `git diff %s..%s`, %s\n", ru.ID, ru.Title, from, to,
			plural(len(entry.Commits), "commit"))
	}
	first, _ := span(landedEntry(rs, units[0].ID))
	_, last := span(landedEntry(rs, units[len(units)-1].ID))
	stat, err := l.repo.DiffStat(ctx, first, last)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "\nThe whole span is `git diff %s..%s`. Besides the units' work it holds the\n"+
		"loop's own commits to `%s`, which are not part of the work:\n\n```\n%s\n```\n\n",
		first, last, state.PathFor(rp.Path), stat)
	fmt.Fprintf(&b, "Each unit's handover is the `handover` field of its entry in `%s`. The plan\n"+
		"itself is in your reading list.\n", state.PathFor(rp.Path))
	return b.String(), nil
}

// collected is every finding the review's other units wrote, in plan order.
func (l *Loop) collected(p *plan.Plan, s *state.State, rp *plan.Plan, rs *state.State) string {
	var parts []string
	for _, u := range p.Units {
		var heading string
		switch u.Role() {
		case plan.Collect:
			heading = fmt.Sprintf("### From %s, on unit %s", u.ID, u.Reviewed())
			if ru := rp.Unit(u.Reviewed()); ru != nil {
				heading += " *" + ru.Title + "*"
			}
			if entry := landedEntry(rs, u.Reviewed()); entry != nil {
				from, to := span(entry)
				heading += fmt.Sprintf(" (`git diff %s..%s`)", from, to)
			}
		case plan.Seams:
			heading = fmt.Sprintf("### From %s, on the seams between the units", u.ID)
		default:
			continue
		}
		body := "This review did not land; it has no findings."
		if entry := s.Entry(u.ID); entry != nil && entry.Status == state.Landed {
			body = orDefault(strings.TrimSpace(entry.Handover), noHandover)
		}
		parts = append(parts, heading+"\n\n"+body)
	}
	if len(parts) == 0 {
		return "No review unit collected anything."
	}
	return strings.Join(parts, "\n\n")
}

func checklistSection(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "## The repository's checklist\n\nStep 5 of the skill goes through every item of it.\n\n" +
		strings.TrimSpace(skillBody(text))
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// judgeCollect holds a collecting session to what a review is: findings
// written, and the repository exactly as it was.
func (l *Loop) judgeCollect(ctx context.Context, u plan.Unit, agent Agent, base string, outcome harness.Outcome) Verdict {
	commits, err := l.repo.Commits(ctx, base)
	if err != nil {
		return Verdict{Kind: Failed, Reason: err.Error()}
	}
	if len(commits) > 0 {
		var listing []string
		for _, commit := range commits {
			listing = append(listing, commit.Short())
		}
		return Verdict{Kind: Failed, Fatal: true, Commits: commits, Reason: "a review session must change " +
			"nothing, but it committed:\n" + strings.Join(listing, "\n") +
			"\nTake those commits out by hand, then run the review again"}
	}

	dirt, err := l.repo.DirtLines(ctx)
	if err != nil {
		return Verdict{Kind: Failed, Reason: err.Error()}
	}
	if len(dirt) > 0 {
		return Verdict{Kind: Failed, Reason: "the working tree is dirty after the review session, and a " +
			"review changes nothing. Undo what you changed, and only that:\n" + strings.Join(head(dirt, 20), "\n")}
	}
	if outcome.Marker == harness.None {
		return noMarker(agent, outcome)
	}

	path := l.handoverPath(u)
	if text, err := os.ReadFile(path); err != nil || strings.TrimSpace(string(text)) == "" {
		shown, _ := filepath.Rel(l.root, path)
		return Verdict{Kind: Failed, Reason: fmt.Sprintf(
			"the session wrote no findings to %s; write them there, even when there are none", shown)}
	}
	return Verdict{Kind: Landed, Reason: outcome.Reason, Marker: outcome.Marker}
}

// judgeTriage holds the triage to its two documents: the fix plan, which must
// lint, and the section for the author at the end of the review plan.
func (l *Loop) judgeTriage(ctx context.Context, p *plan.Plan, u plan.Unit, agent Agent, base string, outcome harness.Outcome) Verdict {
	fixes := review.FixesPath(p.Reviews())
	paths, err := l.repo.ChangedPaths(ctx, base)
	if err != nil {
		return Verdict{Kind: Failed, Reason: err.Error()}
	}
	var stray []string
	for _, path := range paths {
		if path != p.Path && path != fixes {
			stray = append(stray, path)
		}
	}
	if len(stray) > 0 {
		return Verdict{Kind: Failed, Fatal: true, Reason: fmt.Sprintf("the triage may change only %s and %s, "+
			"but its commits touch:\n%s\nTake those changes out by hand, then run the review again",
			p.Path, fixes, strings.Join(head(stray, 20), "\n"))}
	}

	dirt, err := l.repo.DirtLines(ctx)
	if err != nil {
		return Verdict{Kind: Failed, Reason: err.Error()}
	}
	if len(dirt) > 0 {
		return Verdict{Kind: Failed, Reason: "the working tree is dirty after the triage. Commit the fix plan " +
			"and the review plan, and leave everything else as it was:\n" + strings.Join(head(dirt, 20), "\n")}
	}
	if outcome.Marker == harness.None {
		return noMarker(agent, outcome)
	}
	commits, err := l.repo.Commits(ctx, base)
	if err != nil {
		return Verdict{Kind: Failed, Reason: err.Error()}
	}
	if outcome.Marker == harness.Done && len(commits) == 0 {
		return Verdict{Kind: Failed, Reason: fmt.Sprintf("the session reported DONE but committed nothing; "+
			"commit the %s section of %s, and the fix plan when there is one", review.AuthorHeading, p.Path)}
	}

	current, err := plan.Load(filepath.Join(l.root, p.Path), p.Path)
	if err != nil {
		return Verdict{Kind: Failed, Reason: err.Error()}
	}
	if !afterStages(current, review.AuthorHeading) {
		return Verdict{Kind: Failed, Reason: fmt.Sprintf("%s has no `%s` section after its last stage; "+
			"add it, even when it only says that nothing needs a decision, and commit it",
			p.Path, review.AuthorHeading)}
	}

	if info, err := os.Stat(filepath.Join(l.root, fixes)); err == nil && info.Mode().IsRegular() {
		fp, err := plan.Load(filepath.Join(l.root, fixes), fixes)
		if err != nil {
			return Verdict{Kind: Failed, Reason: err.Error()}
		}
		var harnesses plan.Harnesses
		if l.opts.Harnesses != nil {
			harnesses = l.opts.Harnesses
		}
		if errs, _ := plan.Lint(fp, l.root, harnesses, nil); len(errs) > 0 {
			return Verdict{Kind: Failed, Reason: fmt.Sprintf("the fix plan %s does not lint:\n- %s",
				fixes, strings.Join(errs, "\n- "))}
		}
	}
	return Verdict{Kind: Landed, Reason: outcome.Reason, Marker: outcome.Marker, Commits: commits}
}

// afterStages reports whether a heading line follows the plan's last unit.
func afterStages(p *plan.Plan, heading string) bool {
	last := -1
	if n := len(p.Units); n > 0 {
		last = p.Units[n-1].Start
	}
	for i := len(p.Lines) - 1; i > last; i-- {
		if strings.TrimSpace(p.Lines[i]) == heading {
			return true
		}
	}
	return false
}

func noMarker(agent Agent, outcome harness.Outcome) Verdict {
	return Verdict{Kind: Failed, Reason: fmt.Sprintf(
		"the final message did not end with DONE, NOTHING-TO-DO or BLOCKED (%s exited %d)",
		agent.Harness.Name(), outcome.Exit)}
}

func head(lines []string, n int) []string {
	if len(lines) > n {
		return lines[:n]
	}
	return lines
}
