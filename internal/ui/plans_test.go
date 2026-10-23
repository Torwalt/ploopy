package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/Torwalt/ploopy/internal/catalog"
	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/state"
)

const threeUnits = `# Pass

## Stage 1 — All

### 1.1 One

**Goal:** g.

**Done when:** d.

### 1.2 Wire the retry policy into the loop and the board both

**Goal:** g.

**Done when:** d.

### 1.3 Three

**Goal:** g.

**Done when:** d.
`

func entryOf(path string, units map[string]*state.Entry, live *control.Live) catalog.Entry {
	return catalog.Entry{Best: catalog.Copy{
		Plan:  plan.Parse(threeUnits, path),
		State: &state.State{Units: units},
		Here:  true,
		Live:  live,
	}}
}

func TestPlansNestsReviewsAndFixesUnderTheirPlanAndMarksTheRun(t *testing.T) {
	now := time.Unix(10_000, 0)
	landed := func() *state.Entry { return &state.Entry{Status: state.Landed, ElapsedS: 600} }
	entries := []catalog.Entry{
		entryOf("docs/plans/OTHER.md", nil, nil),
		entryOf("docs/plans/PASS_FIXES.md", map[string]*state.Entry{"1.1": landed()},
			&control.Live{Status: control.Status{Unit: "1.2", Agent: "claude", Started: now.Add(-90 * time.Second)}}),
		entryOf("docs/plans/PASS_REVIEW.md", map[string]*state.Entry{"1.1": landed(), "1.2": landed(), "1.3": landed()}, nil),
		entryOf("docs/plans/PASS.md", map[string]*state.Entry{"1.1": landed(), "1.2": landed(), "1.3": landed()}, nil),
		entryOf("docs/plans/STUCK.md", map[string]*state.Entry{
			"1.1": {Status: state.Blocked, Reason: "the premise is wrong"},
		}, nil),
	}

	var out bytes.Buffer
	Plans(&out, entries, "", 40, now)
	text := out.String()
	t.Log("\n" + text)

	for _, want := range []string{
		"5 plans · 1 running · 1 blocked · 1 open", "2 complete",
		"○ OTHER", "next 1.1 One",
		"✓ PASS", "├─ ✓ REVIEW", "└─ ● FIXES",
		"running 1.2 Wire the retry policy",
		"claude · for 1m30s",
		"✗ STUCK", "blocked at 1.1 One: the premise",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the status lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "REVIEW") > strings.Index(text, "FIXES") {
		t.Fatalf("the review is not listed before the fixes:\n%s", text)
	}
	for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Fatalf("a line is %d columns wide, wider than the pane:\n%s", w, line)
		}
	}
}

func TestGenerationOrdersAFamilyByWhenItsPlansAreWritten(t *testing.T) {
	order := []string{"P.md", "P_REVIEW.md", "P_FIXES.md", "P_FIXES_REVIEW.md", "P_FIXES_FIXES.md"}
	for i := 1; i < len(order); i++ {
		if generation(order[i-1]) >= generation(order[i]) {
			t.Fatalf("%s does not come before %s", order[i-1], order[i])
		}
	}
}

func TestWrapBreaksAtSpacesAndSplitsLongWords(t *testing.T) {
	got := wrap("running 2 abcdefghijkl done", 8)
	want := []string{"running", "2", "abcdefgh", "ijkl", "done"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("wrap gave %q, want %q", got, want)
	}
}

func TestPlansShowsBarsInAWidePane(t *testing.T) {
	entries := []catalog.Entry{entryOf("docs/plans/PASS.md", map[string]*state.Entry{
		"1.1": {Status: state.Landed},
	}, nil)}
	var out bytes.Buffer
	Plans(&out, entries, "", 100, time.Now())
	t.Log("\n" + out.String())
	if !strings.Contains(out.String(), "━━━─────── 1/3") {
		t.Fatalf("a wide pane shows no progress bar:\n%s", out.String())
	}
}
