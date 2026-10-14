package loop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/fake"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/stats"
)

const reviewPath = "docs/plans/PASS_REVIEW.md"

// reviewFixture lands both units of PASS, then writes and commits its review:
// 1.1 and 1.2 collect on PASS's 1.1 and 1.2, 1.3 looks at the seams, and 2.1
// is the triage, run by the harness named "other".
func reviewFixture(t *testing.T, collect []fake.Step, triage ...fake.Step) (*fixture, *fake.Harness) {
	t.Helper()
	f := setup(t, twoUnits,
		fake.Step{Do: commits("one.go", "do the first thing"), Outcome: done()},
		fake.Step{Do: commits("two.go", "do the second thing"), Outcome: done()},
	)
	if result := f.run(); result.Status != "done" {
		t.Fatalf("landing the reviewed plan: %+v", result)
	}

	p, err := plan.Load(filepath.Join(f.root, "docs/plans/PASS.md"), "docs/plans/PASS.md")
	if err != nil {
		t.Fatal(err)
	}
	text, err := review.Generate(p, f.state(), review.Options{
		Date: "2026-10-14", Head: "abc", Triage: review.Triage{Agent: "other", Model: "big"},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.root, reviewPath, text)
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "plans: review PASS")

	f.h = fake.New(collect...)
	other := fake.New(triage...)
	other.Named = "other"
	f.opts.Harness = f.h
	f.opts.Harnesses = harness.Set{f.h, other}
	f.opts.PlanPath = reviewPath
	f.opts.ReviewSkill = "---\nname: plan-review\n---\n\n# review skill body\n"
	f.opts.TriageSkill = "---\nname: review-triage\n---\n\n# triage skill body\n"
	f.opts.Checklist = "- the owner holds its state\n"
	f.opts.VerifyCmd = "echo verified >> .ploopy/verified"
	return f, other
}

// finds is a review session that writes its findings and changes nothing.
func finds(id, text string) func(string) error {
	return func(dir string) error {
		path := filepath.Join(stats.Dir(dir, "PASS_REVIEW"), "handover", id+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(text), 0o644)
	}
}

const fixPlan = `---
test: yes
---

# Fixes from the review of PASS

## Stage 1 — Fixes

### 1.1 Reverse the condition

**Goal:** the condition in one.go is reversed.

**Done when:** a test shows it.
`

// triages is a triage session that commits the fix plan, the section for the
// author, and whatever else it is given.
func triages(fixes string, also ...string) func(string) error {
	return func(dir string) error {
		if fixes != "" {
			if err := os.WriteFile(filepath.Join(dir, "docs/plans/PASS_FIXES.md"), []byte(fixes), 0o644); err != nil {
				return err
			}
		}
		file, err := os.OpenFile(filepath.Join(dir, reviewPath), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		fmt.Fprintf(file, "\n%s\n\nNothing needs a decision.\n", review.AuthorHeading)
		file.Close()
		for _, path := range also {
			if err := os.WriteFile(filepath.Join(dir, path), []byte("fixed\n"), 0o644); err != nil {
				return err
			}
		}
		for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "plans: triage"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, out)
			}
		}
		return nil
	}
}

func reviewState(t *testing.T, f *fixture) *state.State {
	t.Helper()
	s, err := state.Load(filepath.Join(f.root, "docs/plans/PASS_REVIEW.state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAReviewCollectsFindingsAndTheTriageWritesTheFixPlan(t *testing.T) {
	f, other := reviewFixture(t,
		[]fake.Step{
			{Do: finds("1.1", "# Findings\n\n## F1 — the condition is reversed\n"), Outcome: done()},
			{Do: finds("1.2", "# Findings\n\nNo findings.\n"), Outcome: done()},
			{Do: finds("1.3", "# Findings\n\n## F1 — one.go and two.go disagree\n"), Outcome: done()},
		},
		fake.Step{Do: triages(fixPlan), Outcome: done()},
	)
	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}

	s := reviewState(t, f)
	for _, id := range []string{"1.1", "1.2", "1.3", "2.1"} {
		if entry := s.Entry(id); entry == nil || entry.Status != state.Landed {
			t.Fatalf("review unit %s is %+v", id, entry)
		}
	}
	if !strings.Contains(s.Entry("1.1").Handover, "the condition is reversed") {
		t.Fatalf("the findings are the review unit's record: %q", s.Entry("1.1").Handover)
	}
	if len(s.Entry("2.1").Commits) != 1 {
		t.Fatalf("the triage's commit is recorded: %v", s.Entry("2.1").Commits)
	}

	if f.h.Started() != 3 || other.Started() != 1 {
		t.Fatalf("collecting sessions %d, triage sessions %d", f.h.Started(), other.Started())
	}
	if spec := other.Specs[0]; spec.Model != "big" {
		t.Fatalf("the triage runs with its own model, not %q", spec.Model)
	}
	triage := other.Prompts[0]
	for _, want := range []string{"# triage skill body", "the condition is reversed", "No findings.",
		"one.go and two.go disagree", "docs/plans/PASS_FIXES.md", review.AuthorHeading} {
		if !strings.Contains(triage, want) {
			t.Fatalf("the triage prompt lacks %q:\n%s", want, triage)
		}
	}

	verified, _ := os.ReadFile(filepath.Join(f.root, ".ploopy/verified"))
	if n := strings.Count(string(verified), "verified"); n != 1 {
		t.Fatalf("verify ran %d times; a review runs it only as the preflight", n)
	}
}

func TestACollectingPromptCarriesTheUnitItReviews(t *testing.T) {
	f, _ := reviewFixture(t, []fake.Step{{Do: finds("1.1", "No findings.\n"), Outcome: done()}})
	f.opts.MaxUnits = 1
	f.run()

	prompt := f.h.Prompts[0]
	for _, want := range []string{"# review skill body", "the owner holds its state",
		"Unit 1.1, *One*, of `docs/plans/PASS.md`", "do the first thing", "**Goal:** the first thing.",
		"git diff ", "one.go", ".ploopy/pass_review/handover/1.1.md"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the collecting prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "the second thing") {
		t.Fatal("a collecting session sees only the unit it reviews")
	}
}

func TestTheSeamsPromptSpansEveryUnit(t *testing.T) {
	f, _ := reviewFixture(t, []fake.Step{
		{Do: finds("1.1", "No findings.\n"), Outcome: done()},
		{Do: finds("1.2", "No findings.\n"), Outcome: done()},
		{Do: finds("1.3", "No findings.\n"), Outcome: done()},
	})
	f.opts.Until = "1.3"
	f.run()

	prompt := f.h.Prompts[2]
	for _, want := range []string{"1.1 *One*", "1.2 *Two*", "one.go", "two.go", "docs/plans/PASS.state.json",
		"* `docs/plans/PASS.md`"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the seams prompt lacks %q:\n%s", want, prompt)
		}
	}
}

func TestAReviewSessionThatCommitsStopsTheReview(t *testing.T) {
	f, _ := reviewFixture(t, []fake.Step{{Do: commits("fixed.go", "fix what I saw"), Outcome: done()}})
	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "must change nothing") {
		t.Fatalf("run %+v", result)
	}
	if f.h.Started() != 1 {
		t.Fatalf("a commit is not retried; %d sessions ran", f.h.Started())
	}
	if entry := reviewState(t, f).Entry("1.1"); entry != nil {
		t.Fatalf("nothing is recorded for a review the author must clean up: %+v", entry)
	}
}

func TestAReviewSessionMustWriteItsFindings(t *testing.T) {
	f, _ := reviewFixture(t, []fake.Step{
		{Outcome: done()},
		{Do: finds("1.1", "No findings.\n"), Outcome: done()},
	})
	f.opts.MaxUnits = 1
	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	entry := reviewState(t, f).Entry("1.1")
	if entry == nil || entry.Status != state.Landed || entry.Attempts != 2 {
		t.Fatalf("unit 1.1 is %+v", entry)
	}
	if !strings.Contains(f.h.Prompts[1], "wrote no findings") {
		t.Fatalf("the retry is told why:\n%s", f.h.Prompts[1])
	}
}

func TestAReviewSessionThatLeavesTheTreeDirtyFails(t *testing.T) {
	f, _ := reviewFixture(t, []fake.Step{
		{Do: func(dir string) error {
			if err := finds("1.1", "No findings.\n")(dir); err != nil {
				return err
			}
			return leaves("scratch.go")(dir)
		}, Outcome: done()},
	})
	f.opts.MaxUnits = 1
	f.opts.Retries = 0
	if result := f.run(); result.Status != "failed" || !strings.Contains(result.Message, "1.1") {
		t.Fatalf("run %+v", result)
	}
}

func TestATriageThatTouchesAnythingElseStopsTheReview(t *testing.T) {
	collect := []fake.Step{
		{Do: finds("1.1", "F1\n"), Outcome: done()},
		{Do: finds("1.2", "F1\n"), Outcome: done()},
		{Do: finds("1.3", "F1\n"), Outcome: done()},
	}
	f, other := reviewFixture(t, collect, fake.Step{Do: triages(fixPlan, "one.go"), Outcome: done()})
	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "one.go") {
		t.Fatalf("run %+v", result)
	}
	if other.Started() != 1 {
		t.Fatalf("a triage that fixed code is not retried; %d sessions ran", other.Started())
	}
}

func TestATriageMustLeaveTheAuthorSectionAndALintingFixPlan(t *testing.T) {
	collect := []fake.Step{
		{Do: finds("1.1", "F1\n"), Outcome: done()},
		{Do: finds("1.2", "F1\n"), Outcome: done()},
		{Do: finds("1.3", "F1\n"), Outcome: done()},
	}
	broken := "# Fixes\n\nNo stages at all.\n"
	f, other := reviewFixture(t, collect,
		fake.Step{Do: triages(broken), Outcome: done()},
		fake.Step{Do: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "docs/plans/PASS_FIXES.md"), []byte(fixPlan), 0o644)
		}, Outcome: done()},
	)
	f.opts.Retries = 0
	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "does not lint") {
		t.Fatalf("run %+v", result)
	}
	if other.Started() != 1 {
		t.Fatalf("sessions %d", other.Started())
	}
}

func TestATriageWithoutTheAuthorSectionFails(t *testing.T) {
	collect := []fake.Step{
		{Do: finds("1.1", "F1\n"), Outcome: done()},
		{Do: finds("1.2", "F1\n"), Outcome: done()},
		{Do: finds("1.3", "F1\n"), Outcome: done()},
	}
	f, _ := reviewFixture(t, collect, fake.Step{Do: commits("docs/plans/PASS_FIXES.md", "plans: fixes"), Outcome: done()})
	f.opts.Retries = 0
	result := f.run()
	if result.Status != "failed" || !strings.Contains(result.Message, "2.1") {
		t.Fatalf("run %+v", result)
	}
	last := reviewState(t, f).Entry("2.1")
	if last != nil {
		t.Fatalf("the triage did not land: %+v", last)
	}
}

func TestAUnitsOwnAgentIsNeverHandedToTheSecondary(t *testing.T) {
	collect := []fake.Step{
		{Do: finds("1.1", "F1\n"), Outcome: done()},
		{Do: finds("1.2", "F1\n"), Outcome: done()},
		{Do: finds("1.3", "F1\n"), Outcome: done()},
	}
	limit := harness.Outcome{RateLimit: &harness.RateLimit{Reason: "limit"}}
	f, other := reviewFixture(t, collect,
		fake.Step{Outcome: limit},
		fake.Step{Do: triages(""), Outcome: done()},
	)
	secondary := fake.New()
	secondary.Named = "second"
	f.opts.Secondary = &Agent{Harness: secondary}
	if result := f.run(); result.Status != "done" {
		t.Fatalf("run %+v", result)
	}
	if secondary.Started() != 0 || other.Started() != 2 {
		t.Fatalf("secondary %d, triage %d", secondary.Started(), other.Started())
	}
}
