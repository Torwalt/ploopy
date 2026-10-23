package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"

	"github.com/Torwalt/ploopy/internal/catalog"
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/stats"
)

// barCells is the length of a plan's progress bar; a pane narrower than
// minBarWidth drops it.
const (
	barCells    = 10
	minBarWidth = 48
	maxWidth    = 80 // wider, a count drifts too far from its plan to read
)

// Width is how many columns out has to fill: the terminal's, else $COLUMNS,
// else 0 for output that is not wrapped.
func Width(out io.Writer) int {
	if file, ok := out.(*os.File); ok {
		if w, _, err := term.GetSize(int(file.Fd())); err == nil && w > 0 {
			return w
		}
	}
	if w, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && w > 0 {
		return w
	}
	return 0
}

// family is a plan and the review and fix plans descending from it.
type family struct {
	root    string
	members []catalog.Entry
}

// families gathers entries under the plan each descends from. Members follow
// the order they were written in: the plan, its review, the fixes, the review
// of the fixes.
func families(entries []catalog.Entry) []family {
	byRoot := map[string]*family{}
	var order []string
	for _, entry := range entries {
		root := review.Family(entry.Path())
		f, seen := byRoot[root]
		if !seen {
			f = &family{root: root}
			byRoot[root] = f
			order = append(order, root)
		}
		f.members = append(f.members, entry)
	}
	sort.Strings(order)
	out := make([]family, 0, len(order))
	for _, root := range order {
		f := byRoot[root]
		sort.SliceStable(f.members, func(i, j int) bool {
			a, b := generation(f.members[i].Path()), generation(f.members[j].Path())
			if a != b {
				return a < b
			}
			return f.members[i].Path() < f.members[j].Path()
		})
		out = append(out, *f)
	}
	return out
}

// generation orders a family: PASS 0, PASS_REVIEW 1, PASS_FIXES 2,
// PASS_FIXES_REVIEW 3. A fix plan is named after the plan reviewed, not
// after the review, so each _FIXES comes after one _REVIEW.
func generation(path string) int {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	n := 0
	for {
		switch {
		case strings.HasSuffix(stem, "_REVIEW"):
			stem, n = strings.TrimSuffix(stem, "_REVIEW"), n+1
		case strings.HasSuffix(stem, "_FIXES"):
			stem, n = strings.TrimSuffix(stem, "_FIXES"), n+2
		default:
			return n
		}
	}
}

type phase int

const (
	notStarted phase = iota
	inProgress
	blocked
	running
	complete
)

func phaseOf(c catalog.Copy) phase {
	if c.Live != nil {
		return running
	}
	next := state.Resume(c.Plan, c.State)
	switch {
	case next == nil:
		return complete
	case c.State.Status(next.ID) == state.Blocked:
		return blocked
	}
	if done, _ := c.Done(); done == 0 && len(c.State.Units) == 0 {
		return notStarted
	}
	return inProgress
}

type planStyles struct {
	bold, dim, good, bad, warn lipgloss.Style
}

// Plans renders every plan of the repository, each review and fix plan
// under the plan it descends from, and marks the runs going on. here is the
// checkout ploopy was started in; width wraps the details, and 0 leaves them
// unwrapped.
func Plans(out io.Writer, entries []catalog.Entry, here string, width int, now time.Time) {
	width = min(width, maxWidth)
	r := lipgloss.NewRenderer(out)
	if !colourful(out) {
		r.SetColorProfile(termenv.Ascii)
	}
	st := planStyles{
		bold: r.NewStyle().Bold(true),
		dim:  r.NewStyle().Faint(true),
		good: r.NewStyle().Bold(true).Foreground(lipgloss.Color("2")),
		bad:  r.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
		warn: r.NewStyle().Foreground(lipgloss.Color("3")),
	}

	counts := map[phase]int{}
	for _, entry := range entries {
		counts[phaseOf(entry.Best)]++
	}
	header := []string{count(len(entries), "plan")}
	if n := counts[running]; n > 0 {
		header = append(header, st.good.Render(fmt.Sprintf("%d running", n)))
	}
	if n := counts[blocked]; n > 0 {
		header = append(header, st.bad.Render(fmt.Sprintf("%d blocked", n)))
	}
	if n := counts[inProgress] + counts[notStarted]; n > 0 {
		header = append(header, fmt.Sprintf("%d open", n))
	}
	if n := counts[complete]; n > 0 {
		header = append(header, st.dim.Render(fmt.Sprintf("%d complete", n)))
	}
	for _, line := range joinWrapped(header, st.dim.Render(" · "), width) {
		fmt.Fprintln(out, line)
	}

	for _, f := range families(entries) {
		fmt.Fprintln(out)
		rootName := strings.TrimSuffix(filepath.Base(f.root), filepath.Ext(f.root))
		start := 0
		if f.members[0].Path() != f.root {
			// The plan they descend from is gone; its name still heads them.
			fmt.Fprintln(out, st.dim.Render(rootName))
		} else {
			planRow(out, st, f.members[0].Best, rootName, "", "  ", here, width, now)
			start = 1
		}
		for i := start; i < len(f.members); i++ {
			c := f.members[i].Best
			branch, rest := "├─ ", "│  "
			if i == len(f.members)-1 {
				branch, rest = "└─ ", "   "
			}
			name := strings.TrimPrefix(c.Plan.Name(), rootName+"_")
			planRow(out, st, c, name, st.dim.Render(branch), st.dim.Render(rest)+"  ", here, width, now)
		}
	}
}

// planRow is one plan: its name and progress on a line, then what it is
// doing, wrapped under the name.
func planRow(out io.Writer, st planStyles, c catalog.Copy, name, branch, indent string, here string, width int, now time.Time) {
	ph := phaseOf(c)
	done, total := c.Done()

	glyph, nameStyle := "○", st.bold
	switch ph {
	case running:
		glyph, nameStyle = st.good.Render("●"), st.good
	case complete:
		glyph, nameStyle = st.dim.Render("✓"), st.dim
	case blocked:
		glyph = st.bad.Render("✗")
	case inProgress:
		glyph = "◐"
	}
	left := branch + glyph + " " + nameStyle.Render(name)

	right := fmt.Sprintf("%d/%d", done, total)
	if width == 0 || width >= minBarWidth {
		right = progressBar(st, ph, done, total) + " " + right
	}
	if ph == complete {
		// A finished plan keeps to one line; what it took goes beside it.
		if totals := Totals(c.State); totals != "" {
			right = totals + "  " + right
		}
		right = st.dim.Render(right)
	}
	gap := 2
	if width > 0 {
		gap = max(width-lipgloss.Width(left)-lipgloss.Width(right), 2)
	} else if w := lipgloss.Width(left); w < 40 {
		gap = 40 - w
	}
	fmt.Fprintln(out, left+strings.Repeat(" ", gap)+right)

	for _, line := range details(st, c, ph, here, now) {
		for _, wrapped := range wrap(line.text, width-lipgloss.Width(indent)) {
			fmt.Fprintln(out, indent+line.style.Render(wrapped))
		}
	}
}

type detail struct {
	text  string
	style lipgloss.Style
}

func details(st planStyles, c catalog.Copy, ph phase, here string, now time.Time) []detail {
	var out []detail
	var facts []string
	switch ph {
	case running:
		doing := "running"
		if c.Live.Unit != "" {
			doing += " " + c.Live.Unit
			if u := c.Plan.Unit(c.Live.Unit); u != nil {
				doing += " " + u.Title
			}
		}
		out = append(out, detail{doing, st.good})
		if c.Live.Agent != "" {
			facts = append(facts, c.Live.Agent)
		}
		if !c.Live.Started.IsZero() {
			facts = append(facts, "for "+stats.Clock(now.Sub(c.Live.Started)))
		}
	case blocked:
		next := state.Resume(c.Plan, c.State)
		line := "blocked at " + next.ID + " " + next.Title
		if reason := c.State.Entry(next.ID).Reason; reason != "" {
			line += ": " + reason
		}
		out = append(out, detail{line, st.bad})
	case inProgress, notStarted:
		next := state.Resume(c.Plan, c.State)
		out = append(out, detail{"next " + next.ID + " " + next.Title, lipgloss.NewStyle()})
	}

	if totals := Totals(c.State); totals != "" && ph != complete {
		facts = append(facts, totals)
	}
	if !c.Here {
		facts = append(facts, c.Where(here))
	}
	if c.Live != nil {
		if wanted := c.Live.Wanted.Describe(c.Plan.Reviews() != ""); wanted != "no end action" {
			facts = append(facts, wanted)
		}
	}
	if len(facts) > 0 {
		out = append(out, detail{strings.Join(facts, " · "), st.dim})
	}
	return out
}

func progressBar(st planStyles, ph phase, done, total int) string {
	filled := 0
	if total > 0 {
		filled = done * barCells / total
	}
	fill := st.dim
	switch ph {
	case running:
		fill = st.good
	case blocked:
		fill = st.bad
	case inProgress:
		fill = st.warn
	}
	return fill.Render(strings.Repeat("━", filled)) + st.dim.Render(strings.Repeat("─", barCells-filled))
}

// wrap breaks text at spaces into lines of at most width columns, and a word
// longer than that across lines. A width below 1 leaves it whole.
func wrap(text string, width int) []string {
	if width < 1 {
		return []string{text}
	}
	var lines []string
	var line []rune
	for word := range strings.FieldsSeq(text) {
		w := []rune(word)
		if len(line) > 0 && len(line)+1+len(w) > width {
			lines = append(lines, string(line))
			line = nil
		}
		if len(line) > 0 {
			line = append(line, ' ')
		}
		line = append(line, w...)
		for len(line) > width {
			lines = append(lines, string(line[:width]))
			line = line[width:]
		}
	}
	if len(line) > 0 || len(lines) == 0 {
		lines = append(lines, string(line))
	}
	return lines
}

// joinWrapped joins styled parts with sep, starting a new line before a part
// that would pass width. A width below 1 keeps one line.
func joinWrapped(parts []string, sep string, width int) []string {
	var lines []string
	line := ""
	for _, part := range parts {
		switch {
		case line == "":
			line = part
		case width > 0 && lipgloss.Width(line+sep+part) > width:
			lines = append(lines, line)
			line = part
		default:
			line += sep + part
		}
	}
	return append(lines, line)
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
