package review

import (
	"strings"
	"testing"

	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/state"
)

const reviewed = `# Pass

Preamble.

## Stage 1 — Only

### 1.1 One

**Goal:** the first thing.

### 1.2 Two

**Goal:** the second thing.

### 1.3 Three

**Goal:** the third thing.
`

func progress(entries map[string]*state.Entry) *state.State {
	return &state.State{Path: "docs/plans/PASS.state.json", Units: entries}
}

func landed(commits ...string) *state.Entry {
	return &state.Entry{Status: state.Landed, Outcome: "done", Base: "base", Commits: commits}
}

var triage = Triage{Agent: "claude", Model: "opus", Effort: "high", Budget: 3}

func TestPathsSitBesideTheReviewedPlan(t *testing.T) {
	if Path("docs/plans/PASS.md") != "docs/plans/PASS_REVIEW.md" {
		t.Fatal(Path("docs/plans/PASS.md"))
	}
	if FixesPath("docs/plans/PASS.md") != "docs/plans/PASS_FIXES.md" {
		t.Fatal(FixesPath("docs/plans/PASS.md"))
	}
}

func TestAReviewCollectsEachLandedUnitThenTheSeamsThenTriages(t *testing.T) {
	p := plan.Parse(reviewed, "docs/plans/PASS.md")
	s := progress(map[string]*state.Entry{
		"1.1": landed("aaa first"),
		"1.2": {Status: state.Landed, Outcome: "nothing-to-do"},
		"1.3": landed("bbb third", "ccc third again"),
	})
	text, err := Generate(p, s, Options{Date: "2026-10-14", Head: "abc", Triage: triage})
	if err != nil {
		t.Fatal(err)
	}

	r := plan.Parse(text, Path(p.Path))
	if r.Reviews() != p.Path {
		t.Fatalf("reviews %q", r.Reviews())
	}
	var got []string
	for _, u := range r.Units {
		got = append(got, u.ID+" "+u.Role()+" "+u.Reviewed())
	}
	want := []string{"1.1 collect 1.1", "1.2 collect 1.3", "1.3 seams ", "2.1 triage "}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("units %v, want %v", got, want)
	}
	last := r.Units[len(r.Units)-1]
	if last.Fields["Agent"] != "claude" || last.Fields["Model"] != "opus" || last.Budget() != 3 {
		t.Fatalf("triage fields %v", last.Fields)
	}
	if !strings.Contains(text, "docs/plans/PASS_FIXES.md") {
		t.Fatal("the review should name the fix plan its triage writes")
	}
	for _, want := range []string{"every finding that holds", "recommended design choices", "alternatives"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the review should describe actionable design recommendations; missing %q", want)
		}
	}
	if errs, _ := plan.Lint(r, t.TempDir(), nil, nil); len(errs) != 1 || !strings.Contains(errs[0], "does not exist") {
		t.Fatalf("lint, away from the reviewed plan: %v", errs)
	}
}

func TestOneUnitNeedsNoSeams(t *testing.T) {
	p := plan.Parse(reviewed, "docs/plans/PASS.md")
	s := progress(map[string]*state.Entry{"1.2": landed("aaa second")})
	text, err := Generate(p, s, Options{Triage: triage})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range plan.Parse(text, "R.md").Units {
		if u.Role() == plan.Seams {
			t.Fatal("a review of one unit has no seams to look at")
		}
	}
}

func TestSinceLeavesOutEarlierUnits(t *testing.T) {
	p := plan.Parse(reviewed, "docs/plans/PASS.md")
	s := progress(map[string]*state.Entry{
		"1.1": landed("aaa"), "1.2": landed("bbb"), "1.3": landed("ccc"),
	})
	units, err := Reviewable(p, s, "1.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 || units[0].ID != "1.2" {
		t.Fatalf("units %v", units)
	}
	if _, err := Reviewable(p, s, "9.9"); err == nil {
		t.Fatal("an unknown --since should be an error")
	}
}

func TestNothingLandedIsNothingToReview(t *testing.T) {
	p := plan.Parse(reviewed, "docs/plans/PASS.md")
	if _, err := Generate(p, progress(map[string]*state.Entry{}), Options{Triage: triage}); err == nil {
		t.Fatal("a plan with no landed commits has nothing to review")
	}
}

func TestAuthorSectionRunsToTheEnd(t *testing.T) {
	text := "# R\n\n## Stage 1 — S\n\n### 1.1 U\n\n" + AuthorHeading + "\n\nDecide this.\n"
	if got := AuthorSection(text); got != AuthorHeading+"\n\nDecide this." {
		t.Fatalf("section %q", got)
	}
	if AuthorSection("# R\n") != "" {
		t.Fatal("no section is empty")
	}
}

func TestTheReviewWrapsAtEightyColumns(t *testing.T) {
	p := plan.Parse(reviewed, "docs/plans/PASS.md")
	s := progress(map[string]*state.Entry{"1.1": landed("aaa"), "1.3": landed("bbb")})
	text, err := Generate(p, s, Options{Date: "2026-10-14", Head: "abcdef12", Triage: triage})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(text, "\n") {
		if len([]rune(line)) > 80 && strings.Contains(line, " ") && !strings.HasPrefix(line, "#") {
			t.Fatalf("a line runs past 80 columns: %q", line)
		}
	}
}

func TestTheTriageCanBeChangedInPlace(t *testing.T) {
	p := plan.Parse(reviewed, "docs/plans/PASS.md")
	s := progress(map[string]*state.Entry{"1.1": landed("aaa"), "1.3": landed("bbb")})
	text, err := Generate(p, s, Options{Date: "2026-10-14", Head: "abc", Triage: triage})
	if err != nil {
		t.Fatal(err)
	}
	if _, got := TriageOf(plan.Parse(text, "R.md")); got != triage {
		t.Fatalf("triage read back as %+v", got)
	}

	cheaper := Triage{Agent: "claude", Model: "sonnet", Effort: "medium"}
	changed, err := SetTriage(text, cheaper)
	if err != nil {
		t.Fatal(err)
	}
	r := plan.Parse(changed, "R.md")
	if _, got := TriageOf(r); got != cheaper {
		t.Fatalf("triage changed to %+v", got)
	}
	if strings.Contains(changed, "Budget:") {
		t.Fatal("no cap leaves no `Budget:` line")
	}
	if u, _ := TriageOf(r); u.Role() != plan.Triage {
		t.Fatal("the unit keeps its `Review: triage`")
	}
	want := strings.Replace(text, "Model: opus\nEffort: high\nBudget: 3", "Model: sonnet\nEffort: medium", 1)
	if changed != want {
		t.Fatalf("only the triage's fields change:\n%s", changed)
	}
}

func TestTheTriageLabelSaysItsCap(t *testing.T) {
	if got := triage.Label(); got != "claude opus high · cap $3" {
		t.Fatal(got)
	}
	if got := (Triage{Agent: "claude", Model: "sonnet"}).Label(); got != "claude sonnet · no cap" {
		t.Fatal(got)
	}
}
