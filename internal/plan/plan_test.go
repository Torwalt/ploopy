package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `---
test: yes
context: docs/A.md
---

# Pass

Preamble prose.

## Stage 1 — First

### 1.1 One

Test: no
Context: docs/B.md

**Goal:** do the first thing.

**Done when:** it is done.

### 1.2 Two

**Goal:** do the second thing.

**Done when:** it is done.

## Notes for the author

### 9.9 Not a unit
`

func parseSample(t *testing.T) *Plan {
	t.Helper()
	return Parse(sample, "docs/plans/PASS.md")
}

func TestUnitsAreFoundInOrder(t *testing.T) {
	p := parseSample(t)
	if len(p.Units) != 2 {
		t.Fatalf("got %d units, want 2", len(p.Units))
	}
	if p.Units[0].ID != "1.1" || p.Units[1].ID != "1.2" {
		t.Fatalf("got %s and %s", p.Units[0].ID, p.Units[1].ID)
	}
	if p.Units[0].StageTitle != "First" {
		t.Fatalf("stage title %q", p.Units[0].StageTitle)
	}
}

func TestUnitsInsideFencesAreNotUnits(t *testing.T) {
	text := "## Stage 1 — S\n\n### 1.1 Real\n\n**Goal:** x\n\n```\n### 2.2 Fake\n```\n"
	p := Parse(text, "P.md")
	if len(p.Units) != 1 || p.Units[0].ID != "1.1" {
		t.Fatalf("got %d units", len(p.Units))
	}
}

func TestFrontMatterIsSettingsAndNotPreamble(t *testing.T) {
	p := parseSample(t)
	if p.Settings["test"] != "yes" || p.Settings["context"] != "docs/A.md" {
		t.Fatalf("settings %v", p.Settings)
	}
	if strings.Contains(p.Preamble(), "test: yes") {
		t.Fatal("front matter leaked into the preamble")
	}
}

func TestPreambleStopsAtTheFirstStage(t *testing.T) {
	preamble := parseSample(t).Preamble()
	if !strings.Contains(preamble, "Preamble prose.") {
		t.Fatalf("preamble %q", preamble)
	}
	if strings.Contains(preamble, "Stage 1") {
		t.Fatal("the preamble reached into the stages")
	}
}

func TestUnitFieldsAreReadFromUnderTheHeading(t *testing.T) {
	p := parseSample(t)
	if got := p.Units[0].Fields["Test"]; got != "no" {
		t.Fatalf("Test field %q", got)
	}
	if got := p.Units[0].Fields["Context"]; got != "docs/B.md" {
		t.Fatalf("Context field %q", got)
	}
	if len(p.Units[1].Fields) != 0 {
		t.Fatalf("unit 1.2 has fields %v", p.Units[1].Fields)
	}
}

func TestWorkOrderCarriesPreambleAndOneUnit(t *testing.T) {
	p := parseSample(t)
	order := p.WorkOrder(p.Units[0])
	for _, want := range []string{"Preamble prose.", "## Stage 1 — First", "### 1.1 One"} {
		if !strings.Contains(order, want) {
			t.Fatalf("work order is missing %q", want)
		}
	}
	if strings.Contains(order, "1.2 Two") {
		t.Fatal("the work order carried a later unit")
	}
	if !strings.Contains(order, "deliberately absent") {
		t.Fatal("the work order does not say later units are absent")
	}
}

func TestLastUnitStopsAtASectionThatIsNotAStage(t *testing.T) {
	p := parseSample(t)
	if body := p.Body(p.Units[1]); strings.Contains(body, "Notes for the author") {
		t.Fatalf("the last unit swallowed the author's notes: %q", body)
	}
}

func TestUnitBodyDropsTrailingRules(t *testing.T) {
	p := Parse("## Stage 1 — S\n\n### 1.1 One\n\n**Goal:** x\n\n---\n\n", "P.md")
	if body := p.Body(p.Units[0]); strings.HasSuffix(body, "---") {
		t.Fatalf("body kept a trailing rule: %q", body)
	}
}

func TestTestsComeFromTheUnitThenThePlan(t *testing.T) {
	p := parseSample(t)
	if p.WantsTests(p.Units[0]) {
		t.Fatal("unit 1.1 says Test: no and should not want tests")
	}
	if !p.WantsTests(p.Units[1]) {
		t.Fatal("unit 1.2 should inherit test: yes from the plan")
	}
}

func TestContextAddsPlanAndUnitDocuments(t *testing.T) {
	p := parseSample(t)
	docs := p.Context("/nowhere", &p.Units[0], []string{"AGENTS.md"})
	want := []string{"AGENTS.md", "docs/A.md", "docs/B.md"}
	if strings.Join(docs, ",") != strings.Join(want, ",") {
		t.Fatalf("context %v, want %v", docs, want)
	}
}

func TestContextDoesNotRepeatADocument(t *testing.T) {
	p := Parse("---\ncontext: AGENTS.md\n---\n\n## Stage 1 — S\n\n### 1.1 One\n\n**Goal:** x\n", "P.md")
	docs := p.Context("/nowhere", &p.Units[0], []string{"AGENTS.md"})
	if len(docs) != 1 {
		t.Fatalf("context %v", docs)
	}
}

func TestUnclosedAndUnknownFrontMatterAreErrors(t *testing.T) {
	unclosed := Parse("---\ntest: yes\n\n# Title\n", "P.md")
	if len(unclosed.Errors) == 0 {
		t.Fatal("unclosed front matter is not an error")
	}
	unknown := Parse("---\nnonsense: yes\n---\n\n# Title\n", "P.md")
	if len(unknown.Errors) == 0 {
		t.Fatal("an unknown front matter key is not an error")
	}
}

func TestAuthorityIsFoundBesideAnExecutionPlan(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/plans/PASS.md"), "# Authority\n")
	p := Parse("## Stage 1 — S\n\n### 1.1 One\n\n**Goal:** x\n", "docs/plans/PASS_EXECUTION.md")
	if got := p.Authority(root); got != filepath.Join("docs/plans", "PASS.md") {
		t.Fatalf("authority %q", got)
	}
}

func TestDiscoverySkipsAuthoritiesAndDocumentsWithoutStages(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/plans/PASS.md"), "# Authority, no stages\n")
	mustWrite(t, filepath.Join(root, "docs/plans/PASS_EXECUTION.md"),
		"## Stage 1 — S\n\n### 1.1 One\n\n**Goal:** x\n")
	mustWrite(t, filepath.Join(root, "docs/plans/NOTES.md"), "# Just notes\n")

	plans, err := Discover(root, "docs/plans")
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Name() != "PASS_EXECUTION" {
		t.Fatalf("discovered %d plans", len(plans))
	}
}

type harnesses struct{}

func (harnesses) Known(name string) bool  { return name == "claude" }
func (harnesses) Efforts(string) []string { return []string{"high"} }
func (harnesses) Names() []string         { return []string{"claude"} }

func TestLintReportsWhatTheLoopCannotRun(t *testing.T) {
	root := t.TempDir()
	text := `---
test: maybe
agent: nonsense
---

## Stage 1 — S

### 1.1 One

Test: perhaps

Body without a goal.

### 1.1 Duplicate

**Goal:** x

### 2.5 Wrong stage

**Goal:** x

Context: missing.md
`
	errs, warnings := Lint(Parse(text, "P.md"), root, harnesses{}, nil)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{"appears twice", "states no goal", "must be yes or no", "agent:"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("errors %v do not mention %q", errs, want)
		}
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "sits under Stage") {
		t.Fatalf("warnings %v", warnings)
	}
}

func TestASettledUnitMayNameADocumentItDeleted(t *testing.T) {
	text := "## Stage 1 — S\n\n### 1.1 One\n\nContext: gone.md\n\n**Goal:** x\n\n**Done when:** y\n"
	p := Parse(text, "P.md")

	_, warnings := Lint(p, t.TempDir(), harnesses{}, nil)
	if !strings.Contains(strings.Join(warnings, "\n"), "gone.md") {
		t.Fatal("a missing context document should warn")
	}

	_, settledWarnings := Lint(p, t.TempDir(), harnesses{}, map[string]bool{"1.1": true})
	if strings.Contains(strings.Join(settledWarnings, "\n"), "gone.md") {
		t.Fatal("a landed unit may have deleted the document it named")
	}
}

func TestADocumentWithoutUnitsDoesNotLint(t *testing.T) {
	errs, _ := Lint(Parse("# Title\n", "P.md"), t.TempDir(), harnesses{}, nil)
	if len(errs) == 0 {
		t.Fatal("a document with no units should not lint")
	}
}

func mustWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
