package main

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

func reviewBoard(t *testing.T, e *env, p *plan.Plan, tri review.Triage, givens map[string]bool, locked string) *settingsBoard {
	return reviewBoardFrom(t, e, p, tri, givens, nil, locked)
}

func reviewBoardFrom(t *testing.T, e *env, p *plan.Plan, tri review.Triage, givens map[string]bool, sources map[string]string, locked string) *settingsBoard {
	t.Helper()
	cmd, f := settingsCommand(t)
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	fill(e, &s, g, nil)
	return newReviewBoard(context.Background(), e, p, g, &s, "pass", 3, tri, givens, sources, locked)
}

func columnByTitle(t *testing.T, b *ui.Board, title string) (int, ui.Column) {
	t.Helper()
	for i, column := range b.Columns {
		if column.Title == title {
			return i, column
		}
	}
	t.Fatalf("the board has no %s column", title)
	return 0, ui.Column{}
}

func fieldByKey(t *testing.T, column ui.Column, key rune) ui.BoardField {
	t.Helper()
	for _, field := range column.Fields {
		if field.Key == key {
			return field
		}
	}
	t.Fatalf("the %s column has no %c field", column.Title, key)
	return ui.BoardField{}
}

func TestANewReviewsBoardTakesItsTriageFromTheLastReview(t *testing.T) {
	e, _ := worktreeRepo(t, nil)
	cmd, f := triageCommand(t, "--triage-model", "sonnet", "--triage-budget", "2")
	last, err := chooseTriage(context.Background(), cmd, e, f, nil)
	if err != nil {
		t.Fatal(err)
	}

	cmd, f = triageCommand(t)
	tri, givens, sources, ran, err := reviewTriage(context.Background(), cmd, e, f, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ran || tri != last {
		t.Fatalf("a new review starts with %+v, ran %v; want %+v", tri, ran, last)
	}

	p := plan.Parse("---\nreviews: docs/plans/PASS.md\n---\n", "docs/plans/PASS_REVIEW.md")
	board := reviewBoardFrom(t, e, p, tri, givens, sources, "")
	at, column := columnByTitle(t, board.board, "Triage")
	if run, _ := columnByTitle(t, board.board, "Run"); at > run {
		t.Fatalf("the Triage column is after Run")
	}
	fields := map[rune]string{
		'a': last.Agent,
		'm': last.Model,
		'e': last.Effort,
		'b': strconv.FormatFloat(last.Budget, 'f', -1, 64),
	}
	for key, want := range fields {
		if got := *fieldByKey(t, column, key).Value; got != want {
			t.Fatalf("%c shows %q, want %q", key, got, want)
		}
	}
}

func TestAWrittenReviewsPendingTriageIsPrefilledAndChangesCommit(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	reviewed := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	s := &state.State{Units: map[string]*state.Entry{"1.1": {
		Status: state.Landed, Outcome: "done", Base: "aaa", Commits: []string{"bbb one"},
	}}}
	own := review.Triage{Agent: "claude", Model: "sonnet", Effort: "medium"}
	text, err := review.Generate(reviewed, s, review.Options{Triage: own})
	if err != nil {
		t.Fatal(err)
	}
	path := review.Path(reviewed.Path)
	write(t, tree, path, text)
	git(t, tree, "add", "-A")
	git(t, tree, "commit", "-q", "-m", "plans: review PASS")

	p := plan.Parse(text, path)
	cmd, f := triageCommand(t)
	tri, givens, sources, ran, err := reviewTriage(context.Background(), cmd, e, f, p, &state.State{Units: map[string]*state.Entry{}})
	if err != nil {
		t.Fatal(err)
	}
	if ran || tri != own {
		t.Fatalf("the pending triage is %+v, ran %v; want %+v", tri, ran, own)
	}

	board := reviewBoardFrom(t, e, p, tri, givens, sources, "")
	at, column := columnByTitle(t, board.board, "Triage")
	for _, field := range column.Fields {
		if field.Key != 'v' && field.Locked != "" {
			t.Fatalf("%c is locked with %q before the triage has run", field.Key, field.Locked)
		}
	}
	if got := *fieldByKey(t, column, 'm').Value; got != own.Model {
		t.Fatalf("the board shows %q, want the review's %q", got, own.Model)
	}
	if got := *fieldByKey(t, column, 'e').Value; got != own.Effort {
		t.Fatalf("the board shows effort %q, want the review's %q", got, own.Effort)
	}

	key(board.board, rune('1'+at))
	key(board.board, 'm')
	final, err := board.triageValue()
	if err != nil {
		t.Fatal(err)
	}
	if final == own {
		t.Fatal("the board did not change the triage")
	}
	if err := writeTriage(context.Background(), e, tree, p, final); err != nil {
		t.Fatal(err)
	}
	changed, err := plan.Load(filepath.Join(tree, path), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, got := review.TriageOf(changed); got != final {
		t.Fatalf("the review now runs its triage with %+v, want %+v", got, final)
	}
	if out := gitOut(t, tree, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("the change is committed:\n%s", out)
	}
	if got := strings.TrimSpace(gitOut(t, tree, "log", "-1", "--format=%s")); got != "plans: triage PASS_REVIEW with "+final.Agent {
		t.Fatalf("the change commits, last commit %q", got)
	}
}

func TestAReviewWhoseTriageRanHasALockedTriageColumn(t *testing.T) {
	e, _ := worktreeRepo(t, nil)
	reviewed := plan.Parse(finishedPlan, "docs/plans/PASS.md")
	s := &state.State{Units: map[string]*state.Entry{"1.1": {
		Status: state.Landed, Outcome: "done", Base: "aaa", Commits: []string{"bbb one"},
	}}}
	own := review.Triage{Agent: "claude", Model: "sonnet", Effort: "medium"}
	text, err := review.Generate(reviewed, s, review.Options{Triage: own})
	if err != nil {
		t.Fatal(err)
	}
	p := plan.Parse(text, review.Path(reviewed.Path))
	u, _ := review.TriageOf(p)
	ran := &state.State{Units: map[string]*state.Entry{u.ID: {Status: state.Landed}}}

	cmd, f := triageCommand(t)
	tri, givens, sources, hasRun, err := reviewTriage(context.Background(), cmd, e, f, p, ran)
	if err != nil {
		t.Fatal(err)
	}
	if !hasRun || tri != own {
		t.Fatalf("a review whose triage ran reads %+v, ran %v; want %+v", tri, hasRun, own)
	}

	board := reviewBoardFrom(t, e, p, tri, givens, sources, triageRanLock)
	_, column := columnByTitle(t, board.board, "Triage")
	for _, field := range column.Fields {
		if field.Locked != triageRanLock {
			t.Fatalf("%c is locked with %q", field.Key, field.Locked)
		}
	}
}

func TestTheReviewBoardOffersFollowUpAfterTheReview(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	e, _ := worktreeRepo(t, nil)
	p := plan.Parse("---\nreviews: docs/plans/PASS.md\n---\n", "docs/plans/PASS_REVIEW.md")
	board := reviewBoard(t, e, p, defaultTriage, nil, "")
	at, automation := columnByTitle(t, board.board, "Automation")
	field := fieldByKey(t, automation, 'n')
	if got := *field.Value; got != "no" {
		t.Fatalf("follow-up starts as %q, want no", got)
	}
	key(board.board, rune('1'+at))
	chosen, err := board.start(func() error {
		key(board.board, 'n')
		board.board.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !chosen.followUp {
		t.Fatal("the board did not turn follow-up on")
	}
	if got := *field.Value; got != "yes" {
		t.Fatalf("follow-up cycles to %q, want yes", got)
	}
}

func TestANewReviewsModeCanChangeOnTheBoardBeforeItRuns(t *testing.T) {
	e, tree := chainRepo(t)
	rp, err := plan.Load(filepath.Join(tree, "docs/plans/PASS.md"), "docs/plans/PASS.md")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := state.Load(filepath.Join(tree, "docs/plans/PASS.state.json"))
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitOut(t, tree, "rev-parse", "HEAD"))
	text, err := review.Generate(rp, rs, review.Options{Head: head, Triage: defaultTriage})
	if err != nil {
		t.Fatal(err)
	}
	p := plan.Parse(text, review.Path(rp.Path))
	write(t, tree, p.Path, text)
	git(t, tree, "add", p.Path)
	git(t, tree, "commit", "-q", "-m", "write review")
	board := reviewBoard(t, e, p, defaultTriage, nil, "")
	board.reviewModeLock = ""
	board.rebuild()
	at, _ := columnByTitle(t, board.board, "Triage")
	key(board.board, rune('1'+at))
	chosen, err := board.start(func() error {
		key(board.board, 'v')
		board.board.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if chosen.reviewMode != review.Direct {
		t.Fatalf("mode %q", chosen.reviewMode)
	}
	columnByTitle(t, board.board, "Reviewer")
	if err := rewriteReviewMode(context.Background(), e, tree, p, chosen.reviewMode, "", defaultTriage); err != nil {
		t.Fatal(err)
	}
	changed, err := plan.Load(filepath.Join(tree, p.Path), p.Path)
	if err != nil || review.ModeOf(changed) != review.Direct || len(changed.Units) != 1 {
		t.Fatalf("rewritten review: %+v, %v", changed, err)
	}
	if changed.Settings["review-head"] != head {
		t.Fatal("mode change moved the reviewed revision")
	}
}
