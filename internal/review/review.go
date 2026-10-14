// Package review turns a plan's landed work into a review plan the loop runs:
// one collecting unit per landed unit, one on the seams between them, and one
// triage unit that weighs what they found and writes the fix plan. Pure text:
// no git, no process execution.
package review

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/state"
)

// AuthorHeading opens the section the triage adds to the review plan, after
// its last stage, where no session reads it.
const AuthorHeading = "## For the author"

// Triage is the agent that weighs the findings, usually a stronger one than
// the agents that collect them.
type Triage struct {
	Agent  string
	Model  string
	Effort string
	Budget float64 // dollars; 0 leaves the session uncapped
}

// Options is one review to write.
type Options struct {
	Date   string // when the review was written
	Head   string // the short HEAD it was written against
	Since  string // review the landed units from this id on; "" for all
	Triage Triage
}

// Path is where the review of a plan lives: beside it, named after it.
func Path(reviewed string) string { return sibling(reviewed, "_REVIEW") }

// FixesPath is the fix plan the triage of a plan's review writes.
func FixesPath(reviewed string) string { return sibling(reviewed, "_FIXES") }

func sibling(path, suffix string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return filepath.Join(filepath.Dir(path), stem+suffix+".md")
}

// Reviewable is the plan's landed units that committed something, in plan
// order, from since on.
func Reviewable(p *plan.Plan, s *state.State, since string) ([]plan.Unit, error) {
	var from []int
	if since != "" {
		u := p.Unit(since)
		if u == nil {
			return nil, fmt.Errorf("%s has no unit %s", p.Path, since)
		}
		from = u.Key()
	}
	var units []plan.Unit
	for _, u := range p.Units {
		if from != nil && before(u.Key(), from) {
			continue
		}
		entry := s.Entry(u.ID)
		if entry == nil || entry.Status != state.Landed || len(entry.Commits) == 0 {
			continue
		}
		units = append(units, u)
	}
	return units, nil
}

func before(a, b []int) bool {
	for i := range a {
		if i >= len(b) {
			return false
		}
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// Generate writes the review plan of p's landed work.
func Generate(p *plan.Plan, s *state.State, o Options) (string, error) {
	units, err := Reviewable(p, s, o.Since)
	if err != nil {
		return "", err
	}
	if len(units) == 0 {
		return "", fmt.Errorf("%s has no landed unit with commits to review", p.Path)
	}
	if o.Triage.Agent == "" {
		return "", fmt.Errorf("the triage needs an agent")
	}

	var b strings.Builder
	para := func(format string, args ...any) { b.WriteString(wrap(fmt.Sprintf(format, args...)) + "\n\n") }
	fixes := FixesPath(p.Path)
	fmt.Fprintf(&b, "---\nreviews: %s\ntest: no\n---\n\n", p.Path)
	fmt.Fprintf(&b, "# Review of %s\n\n", p.Name())
	para("Status: written by `ploopy review` on %s against `%s`. It reviews %s of `%s`.",
		o.Date, o.Head, count(len(units), "landed unit"), p.Path)
	seams := ""
	if len(units) > 1 {
		seams = " The seams unit looks only at what lies between the units."
	}
	para("Each collecting unit reads one landed unit's work order, handover and commits, and "+
		"writes its findings without changing the repository.%s The triage unit weighs every "+
		"finding, writes the fix plan `%s` for the slips and gaps that hold, and adds what needs "+
		"the author's decision to this document under *For the author*.", seams, fixes)

	b.WriteString("## Stage 1 — Collect\n\n")
	for i, u := range units {
		fmt.Fprintf(&b, "### 1.%d Review %s %s\n\nReview: %s\n\n", i+1, u.ID, u.Title, u.ID)
		para("**Goal:** findings on unit %s of `%s`, *%s*, against its work order.", u.ID, p.Path, u.Title)
		para("**Done when:** the findings file is written and the repository is unchanged.")
	}
	if len(units) > 1 {
		fmt.Fprintf(&b, "### 1.%d Review the seams between the units\n\nReview: %s\n\n", len(units)+1, plan.Seams)
		para("**Goal:** findings on what lies between the reviewed units: the contracts they share, " +
			"work one left for another, leftovers no unit owns, documents none updated.")
		para("**Done when:** the findings file is written and the repository is unchanged.")
	}

	b.WriteString("## Stage 2 — Triage\n\n")
	fmt.Fprintf(&b, "### 2.1 Weigh the findings\n\nReview: %s\nAgent: %s\n", plan.Triage, o.Triage.Agent)
	if o.Triage.Model != "" {
		fmt.Fprintf(&b, "Model: %s\n", o.Triage.Model)
	}
	if o.Triage.Effort != "" {
		fmt.Fprintf(&b, "Effort: %s\n", o.Triage.Effort)
	}
	if o.Triage.Budget > 0 {
		fmt.Fprintf(&b, "Budget: %g\n", o.Triage.Budget)
	}
	b.WriteString("\n")
	para("**Goal:** every finding confirmed or dropped, duplicates merged, each one classed as a " +
		"slip, a gap or a design question; a fix plan for the slips and gaps; the design questions " +
		"put to the author.")
	b.WriteString(wrap(fmt.Sprintf("**Done when:** one commit touches only `%s` and this document, "+
		"this document ends with a *For the author* section, and the fix plan, when there is one, "+
		"lints clean.", fixes)) + "\n")
	return b.String(), nil
}

// wrap breaks a paragraph into lines of at most 80 columns where it can.
func wrap(text string) string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && len(line)+1+len(word) > 80 {
			lines = append(lines, line)
			line = word
			continue
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	return strings.Join(append(lines, line), "\n")
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// AuthorSection is the section the triage wrote for the author, from its
// heading to the end of the review plan, or "" when there is none.
func AuthorSection(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == AuthorHeading {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return ""
}
