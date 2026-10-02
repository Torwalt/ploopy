// Package ui owns the terminal: the selection form, the live run and the
// countdown that ends it. Nothing else writes to the screen.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
)

// Reporter renders a run. It is deliberately not a full-screen program:
// scrollback stays readable, and so does output redirected to a file.
type Reporter struct {
	out   io.Writer
	err   io.Writer
	Quiet bool // no event feed, only decisions

	tag     lipgloss.Style
	unit    lipgloss.Style
	dim     lipgloss.Style
	tool    lipgloss.Style
	good    lipgloss.Style
	bad     lipgloss.Style
	warn    lipgloss.Style
	started time.Time
	cost    float64
	tokens  int
}

// NewReporter builds a renderer for a writer, with colour only where it lands
// on a terminal that wants it.
func NewReporter(out, errOut io.Writer) *Reporter {
	renderer := lipgloss.NewRenderer(out)
	if !colourful(out) {
		renderer.SetColorProfile(termenv.Ascii)
	}
	return &Reporter{
		out: out, err: errOut,
		tag:  renderer.NewStyle().Bold(true),
		unit: renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		dim:  renderer.NewStyle().Faint(true),
		tool: renderer.NewStyle().Foreground(lipgloss.Color("6")),
		good: renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("2")),
		bad:  renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
		warn: renderer.NewStyle().Foreground(lipgloss.Color("3")),
	}
}

func colourful(out io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	file, ok := out.(*os.File)
	return ok && isatty.IsTerminal(file.Fd())
}

// Say reports a decision the loop made.
func (r *Reporter) Say(format string, args ...any) {
	fmt.Fprintf(r.out, "\n%s %s\n", r.tag.Render("[ploopy]"), fmt.Sprintf(format, args...))
}

// Fail reports why the run stopped.
func (r *Reporter) Fail(format string, args ...any) {
	fmt.Fprintf(r.err, "\n%s\n", r.bad.Render("[ploopy] "+fmt.Sprintf(format, args...)))
}

// UnitStart opens a unit's session.
func (r *Reporter) UnitStart(u plan.Unit, attempt int, logPath string) {
	r.started = time.Now()
	r.cost, r.tokens = 0, 0
	fmt.Fprintf(r.out, "\n%s %s\n", r.unit.Render("▌ "+u.ID+"  "+u.Title),
		r.dim.Render(fmt.Sprintf("attempt %d · %s", attempt, logPath)))
}

// Event renders one step of a session.
func (r *Reporter) Event(e harness.Event) {
	switch e.Kind {
	case harness.EventUsage:
		// Both are running totals, so the last one seen is the answer.
		if e.Tokens > 0 {
			r.tokens = e.Tokens
		}
		if e.CostUSD > 0 {
			r.cost = e.CostUSD
		}
		return
	case harness.EventToolUse:
		if r.Quiet {
			return
		}
		line := r.tool.Render("  → " + e.Tool)
		if e.Text != "" {
			line += " " + r.dim.Render(truncate(e.Text, 96))
		}
		fmt.Fprintln(r.out, line)
	case harness.EventText, harness.EventNotice:
		if r.Quiet {
			return
		}
		for _, line := range head(e.Text, 3) {
			fmt.Fprintln(r.out, r.dim.Render("    "+truncate(line, 96)))
		}
	case harness.EventRaw:
		if !r.Quiet {
			fmt.Fprintln(r.out, r.dim.Render("    "+truncate(e.Text, 96)))
		}
	}
}

// UnitDone closes a unit with its verdict.
func (r *Reporter) UnitDone(u plan.Unit, v loop.Verdict) {
	elapsed := time.Since(r.started).Round(time.Second)
	for _, commit := range v.Commits {
		fmt.Fprintf(r.out, "    %s\n", commit.Short())
	}
	for _, note := range v.Notes {
		fmt.Fprintf(r.out, "    %s\n", r.warn.Render("note: "+note))
	}

	tail := elapsed.String()
	if r.cost > 0 {
		tail += fmt.Sprintf(" · $%.2f", r.cost)
	}
	if v.Kind == loop.Blocked {
		fmt.Fprintf(r.out, "\n%s %s\n", r.bad.Render("▌ "+u.ID+" BLOCKED"), r.dim.Render(tail))
		fmt.Fprintf(r.out, "    %s\n", v.Reason)
		return
	}
	fmt.Fprintf(r.out, "\n%s %s\n", r.good.Render("▌ "+u.ID+" complete"), r.dim.Render(tail))
}

func head(text string, limit int) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
		if len(out) == limit {
			break
		}
	}
	return out
}

func truncate(text string, limit int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}
