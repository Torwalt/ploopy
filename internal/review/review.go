// Package review writes collected or direct review plans. Pure text:
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
	Agent  string  `json:"agent"`
	Model  string  `json:"model,omitempty"`
	Effort string  `json:"effort,omitempty"`
	Budget float64 `json:"budget,omitempty"` // dollars; 0 leaves the session uncapped
}

// fields are the triage unit's lines that name its agent.
func (t Triage) fields() []string {
	out := []string{"Agent: " + t.Agent}
	if t.Model != "" {
		out = append(out, "Model: "+t.Model)
	}
	if t.Effort != "" {
		out = append(out, "Effort: "+t.Effort)
	}
	if t.Budget > 0 {
		out = append(out, fmt.Sprintf("Budget: %g", t.Budget))
	}
	return out
}

// Label names the triage's agent on one line.
func (t Triage) Label() string {
	parts := []string{t.Agent}
	if t.Model != "" {
		parts = append(parts, t.Model)
	}
	if t.Effort != "" {
		parts = append(parts, t.Effort)
	}
	if t.Budget > 0 {
		parts = append(parts, fmt.Sprintf("· cap $%g", t.Budget))
	} else {
		parts = append(parts, "· no cap")
	}
	return strings.Join(parts, " ")
}

// TriageOf is the triage unit of a review plan and the agent it names.
func TriageOf(p *plan.Plan) (*plan.Unit, Triage) {
	for i := range p.Units {
		if u := p.Units[i]; u.Role() == plan.Triage || u.Role() == plan.Direct {
			return &p.Units[i], Triage{
				Agent:  strings.TrimSpace(u.Fields["Agent"]),
				Model:  strings.TrimSpace(u.Fields["Model"]),
				Effort: strings.TrimSpace(u.Fields["Effort"]),
				Budget: u.Budget(),
			}
		}
	}
	return nil, Triage{}
}

// SetTriage rewrites a review plan's triage unit to run with t, keeping every
// other line of the document as it was.
func SetTriage(text string, t Triage) (string, error) {
	p := plan.Parse(text, "")
	u, _ := TriageOf(p)
	if u == nil {
		return "", fmt.Errorf("the review has no triage unit")
	}
	named := map[string]bool{"Agent": true, "Model": true, "Effort": true, "Budget": true}
	lines := p.Lines
	var out []string
	out = append(out, lines[:u.Start+1]...)
	i := u.Start + 1
	for ; i < u.End && strings.TrimSpace(lines[i]) == ""; i++ {
		out = append(out, lines[i])
	}
	for ; i < u.End; i++ {
		name, _, ok := strings.Cut(lines[i], ":")
		if !ok || strings.TrimSpace(lines[i]) == "" || strings.ContainsAny(name, " *#") {
			break
		}
		if !named[name] {
			out = append(out, lines[i])
		}
	}
	out = append(out, t.fields()...)
	out = append(out, lines[i:]...)
	return strings.Join(out, "\n") + "\n", nil
}

// Options is one review to write.
type Options struct {
	Mode   string // collected or direct
	Base   string // the recorded start of the reviewed work
	Date   string // when the review was written
	Head   string // the short HEAD it was written against
	Since  string // review the landed units from this id on; "" for all
	Triage Triage
}

const (
	Collected = "collected"
	Direct    = "direct"
)

func ValidateMode(mode string) error {
	if mode != "" && mode != Collected && mode != Direct {
		return fmt.Errorf("review mode must be collected or direct, not %q", mode)
	}
	return nil
}

func ModeOf(p *plan.Plan) string {
	if u, _ := TriageOf(p); u != nil && u.Role() == plan.Direct {
		return Direct
	}
	return Collected
}

// FollowupsPath survives closing the plan and keeps separate review passes apart.
func FollowupsPath(reviewed, head string) string {
	if len(head) > 12 {
		head = head[:12]
	}
	suffix := "_FOLLOWUPS"
	if head != "" {
		suffix += "_" + head
	}
	return sibling(reviewed, suffix)
}

// Path is where the review of a plan lives: beside it, named after it.
func Path(reviewed string) string { return sibling(reviewed, "_REVIEW") }

// FixesPath is the fix plan the triage of a plan's review writes.
func FixesPath(reviewed string) string { return sibling(reviewed, "_FIXES") }

// Lineage is a plan and each plan it descends from by review or triage,
// nearest first: PASS_FIXES_REVIEW, PASS_FIXES, PASS.
func Lineage(path string) []string {
	dir, ext := filepath.Dir(path), filepath.Ext(path)
	stem := strings.TrimSuffix(filepath.Base(path), ext)
	out := []string{path}
	for {
		trimmed := stem
		for _, suffix := range []string{"_REVIEW", "_FIXES"} {
			if trimmed == stem {
				trimmed = strings.TrimSuffix(stem, suffix)
			}
		}
		if trimmed == stem || trimmed == "" {
			return out
		}
		stem = trimmed
		out = append(out, filepath.Join(dir, stem+ext))
	}
}

// Family is the plan a review or fix plan descends from in the end: PASS_REVIEW
// and PASS_FIXES belong to PASS, and so does a review of PASS_FIXES. A plan of
// no review is its own.
func Family(path string) string {
	lineage := Lineage(path)
	return lineage[len(lineage)-1]
}

// IsFix reports whether a plan is the fix plan a triage wrote: its name ends
// in _FIXES. A review of a fix plan is not itself a fix plan.
func IsFix(path string) bool {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return strings.HasSuffix(stem, "_FIXES")
}

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
	if err := ValidateMode(o.Mode); err != nil {
		return "", err
	}
	if o.Mode == Direct && o.Since != "" {
		return "", fmt.Errorf("direct review covers the whole branch; --since is only for collected reviews")
	}
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
	if o.Base == "" {
		entry := s.Entry(units[0].ID)
		o.Base = entry.Base
		if o.Base == "" {
			first, _, _ := strings.Cut(entry.Commits[0], " ")
			o.Base = first + "^"
		}
	}

	var b strings.Builder
	para := func(format string, args ...any) { b.WriteString(wrap(fmt.Sprintf(format, args...)) + "\n\n") }
	fixes := FixesPath(p.Path)
	fmt.Fprintf(&b, "---\nreviews: %s\ntest: no\nreview-base: %s\nreview-head: %s\n---\n\n", p.Path, o.Base, o.Head)
	fmt.Fprintf(&b, "# Review of %s\n\n", p.Name())
	para("Status: written by `ploopy review` on %s against `%s`. It reviews %s of `%s`.", o.Date, o.Head, count(len(units), "landed unit"), p.Path)
	para("Only required fixes go into `%s`: evidenced defects, regressions, violated repository rules or unmet plan requirements. "+
		"Useful optional improvements go into one follow-up issue for this review pass, using the session's persistence instructions. "+
		"Unsupported concerns are dropped. Zero fixes and zero optional findings is a successful review. "+
		"Explain required design choices and their alternatives under *For the author*.", fixes)
	if o.Mode == Direct {
		para("One frontier session reviews the complete change `git diff %s..%s` against the plan at the recorded tip, including fixes already applied. "+
			"It checks the units and their integration itself, without collecting sessions, and writes the fix plan only when necessary.", o.Base, o.Head)
		b.WriteString("## Stage 1 — Review\n\n### 1.1 Review the branch against the plan\n\nReview: direct\n")
	} else {
		para("Each collecting unit reads one landed unit's work order, handover and commits, and writes findings without changing the repository. " +
			"The seams unit, when present, checks integration. The triage weighs every finding that holds, separates required fixes from optional improvements, " +
			"and writes the fix plan with recommended design choices only for required fixes.")
		b.WriteString("## Stage 1 — Collect\n\n")
		for i, u := range units {
			fmt.Fprintf(&b, "### 1.%d Review %s %s\n\nReview: %s\n\n", i+1, u.ID, u.Title, u.ID)
			para("**Goal:** findings on unit %s of `%s`, *%s*, against its work order.", u.ID, p.Path, u.Title)
			para("**Done when:** the findings file is written and the repository is unchanged.")
		}
		if len(units) > 1 {
			fmt.Fprintf(&b, "### 1.%d Review the seams between the units\n\nReview: %s\n\n", len(units)+1, plan.Seams)
			para("**Goal:** findings on the contracts the reviewed units share, work one left for another, and required documentation none updated.")
			para("**Done when:** the findings file is written and the repository is unchanged.")
		}
		b.WriteString("## Stage 2 — Triage\n\n### 2.1 Weigh the findings\n\nReview: triage\n")
	}
	for _, field := range o.Triage.fields() {
		b.WriteString(field + "\n")
	}
	b.WriteString("\n")
	para("**Goal:** evidence checked, duplicates merged, required fixes separated from optional improvements and unsupported concerns dropped; " +
		"a fix plan only for required fixes, with any necessary design decisions resolved; optional findings persisted together in one issue.")
	para("**Done when:** the review summary is committed under *For the author*, the fix plan when necessary lints clean, " +
		"and any optional findings are committed in one follow-up document linked from the summary. No source or tests changed.")
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
