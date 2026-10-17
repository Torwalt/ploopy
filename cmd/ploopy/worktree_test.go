package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/plan"
)

const openPlan = "# Pass\n\n## Stage 1 — Only\n\n### 1.1 One\n\n**Goal:** x\n\n**Done when:** y\n\n### 1.2 Two\n\n**Goal:** x\n\n**Done when:** y\n"

const halfState = `{"units": {"1.1": {"status": "landed", "outcome": "done", "date": "2026-10-14T10:00:00Z", "base": "aaa", "commits": ["bbb one"]}}}
`

// mainRepo is a main checkout on master with one commit, and nothing else.
func mainRepo(t *testing.T) *env {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q", "-b", "master")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	write(t, root, "README.md", "start\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "first")
	return &env{root: root, cfg: config.Default(), harnesses: allHarnesses()}
}

// onBranch commits the open plan on branch pass, and leaves master checked out.
func onBranch(t *testing.T, e *env) {
	t.Helper()
	git(t, e.root, "switch", "-q", "-c", "pass")
	write(t, e.root, "docs/plans/PASS.md", openPlan)
	write(t, e.root, "docs/plans/PASS.state.json", halfState)
	git(t, e.root, "add", "-A")
	git(t, e.root, "commit", "-q", "-m", "plans")
	git(t, e.root, "switch", "-q", "master")
}

func placed(t *testing.T, e *env) string {
	t.Helper()
	ctx := context.Background()
	c, err := e.find(ctx, "PASS")
	if err != nil {
		t.Fatal(err)
	}
	root, err := e.place(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAPlanOnlyOnABranchGetsAWorktreeFromMaster(t *testing.T) {
	e := mainRepo(t)
	onBranch(t, e)

	root := placed(t, e)
	if root != worktreePath(e.root, "pass") {
		t.Fatalf("the run should go to ploopy's worktree for pass, not %s", root)
	}
	if got := output(t, root, "branch", "--show-current"); got != "pass" {
		t.Fatalf("the worktree holds %q", got)
	}
	if got := output(t, e.root, "branch", "--show-current"); got != "master" {
		t.Fatalf("the main checkout stays on master, not %q", got)
	}
	if again := placed(t, e); again != root {
		t.Fatalf("the next run should find the same worktree, not %s", again)
	}
}

func TestWhatARemovedWorktreeLeftBehindIsCleared(t *testing.T) {
	e := mainRepo(t)
	onBranch(t, e)
	stale := worktreePath(e.root, "pass")
	write(t, stale, ".ploopy/pass/run.log", "finish\tpoweroff\n")

	root := placed(t, e)
	if root != stale || !exists(filepath.Join(root, "docs/plans/PASS.md")) {
		t.Fatalf("the worktree should take the leftover's place, got %s", root)
	}
	if exists(filepath.Join(root, ".ploopy/pass/run.log")) {
		t.Fatal("the leftover log should be gone")
	}
}

func TestAnythingElseInTheWayIsMovedAside(t *testing.T) {
	e := mainRepo(t)
	onBranch(t, e)
	stale := worktreePath(e.root, "pass")
	write(t, stale, "notes.txt", "keep me\n")

	if root := placed(t, e); root != stale {
		t.Fatalf("the worktree goes where ploopy puts it, not %s", root)
	}
	moved, _ := filepath.Glob(stale + ".old-*/notes.txt")
	if len(moved) != 1 {
		t.Fatalf("what was in the way should be kept beside it, found %v", moved)
	}
}

func TestAPlanOnMasterGetsABranchNamedAfterIt(t *testing.T) {
	e := mainRepo(t)
	write(t, e.root, "docs/plans/PLAYTEST_FIXES.md", openPlan)
	git(t, e.root, "add", "-A")
	git(t, e.root, "commit", "-q", "-m", "plans")

	ctx := context.Background()
	c, err := e.find(ctx, "PLAYTEST_FIXES")
	if err != nil {
		t.Fatal(err)
	}
	root, err := e.place(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if root != worktreePath(e.root, "playtest-fixes") {
		t.Fatalf("the plan should run on playtest-fixes, in %s", root)
	}
	if got := output(t, root, "rev-parse", "HEAD"); got != output(t, e.root, "rev-parse", "master") {
		t.Fatal("the branch starts where master is")
	}

	c, err = e.find(ctx, "PLAYTEST_FIXES")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := e.place(ctx, c); err != nil || again != root {
		t.Fatalf("a second run before any progress should use the same branch, got %s, %v", again, err)
	}
	if out := output(t, e.root, "branch", "--list", "playtest-fixes-*"); out != "" {
		t.Fatalf("no second branch: %s", out)
	}
}

func TestAnUncommittedPlanIsCommittedOnItsNewBranch(t *testing.T) {
	e := mainRepo(t)
	write(t, e.root, "docs/plans/PASS.md", openPlan)

	root := placed(t, e)
	if got := output(t, root, "log", "-1", "--format=%s"); got != "plans: add PASS" {
		t.Fatalf("the plan should be committed on its branch, last commit %q", got)
	}
	if exists(filepath.Join(e.root, "docs/plans/PASS.md")) {
		t.Fatal("the plan moved to its branch; master's checkout should not keep a copy")
	}
	if out := output(t, e.root, "status", "--porcelain"); out != "" {
		t.Fatalf("master's checkout should be clean:\n%s", out)
	}
	if got := output(t, e.root, "log", "-1", "--format=%s", "master"); got != "first" {
		t.Fatalf("master gains no commit, it is at %q", got)
	}
}

func TestTheMainCheckoutHandsItsBranchToAWorktree(t *testing.T) {
	e := mainRepo(t)
	onBranch(t, e)
	git(t, e.root, "switch", "-q", "pass")
	write(t, e.root, "docs/plans/PASS.md", openPlan+"\n### 1.3 Three\n\n**Goal:** x\n\n**Done when:** y\n")

	root := placed(t, e)
	if got := output(t, e.root, "branch", "--show-current"); got != "master" {
		t.Fatalf("the main checkout should move to master, it is on %q", got)
	}
	if got := output(t, root, "branch", "--show-current"); got != "pass" {
		t.Fatalf("the worktree should hold pass, it holds %q", got)
	}
	if got := output(t, root, "log", "-1", "--format=%s"); got != "plans: add PASS" {
		t.Fatalf("the edited plan should be committed on pass first, last commit %q", got)
	}
}

func TestOtherChangesOnTheBranchMoveWithIt(t *testing.T) {
	e := mainRepo(t)
	onBranch(t, e)
	e.cfg.AuthorPaths = []string{"NOTES.md"}
	git(t, e.root, "switch", "-q", "pass")
	write(t, e.root, "README.md", "half done\n")
	write(t, e.root, "half.go", "package half\n")
	write(t, e.root, "NOTES.md", "mine\n")

	root := placed(t, e)
	if got := output(t, e.root, "branch", "--show-current"); got != "master" {
		t.Fatalf("the main checkout should move to master, it is on %q", got)
	}
	if got := output(t, root, "branch", "--show-current"); got != "pass" {
		t.Fatalf("the worktree should hold pass, it holds %q", got)
	}
	for path, want := range map[string]string{"README.md": "half done\n", "half.go": "package half\n"} {
		if got, _ := os.ReadFile(filepath.Join(root, path)); string(got) != want {
			t.Fatalf("%s did not move with the branch: %q", path, got)
		}
	}
	if out := output(t, root, "status", "--porcelain"); !strings.Contains(out, "README.md") ||
		!strings.Contains(out, "half.go") {
		t.Fatalf("the worktree should hold the changes uncommitted:\n%s", out)
	}
	if out := output(t, e.root, "status", "--porcelain"); !strings.Contains(out, "NOTES.md") ||
		strings.Contains(out, "README.md") || strings.Contains(out, "half.go") {
		t.Fatalf("only the author's path should stay in the main checkout:\n%s", out)
	}
	for _, dir := range []string{e.root, root} {
		if out := output(t, dir, "stash", "list"); out != "" {
			t.Fatalf("the stash should be empty in %s:\n%s", dir, out)
		}
	}
}

func TestAStagedDeletionMovesWithTheBranch(t *testing.T) {
	e := mainRepo(t)
	onBranch(t, e)
	e.cfg.AuthorPaths = []string{"NOTES.md"}
	git(t, e.root, "switch", "-q", "pass")
	git(t, e.root, "rm", "-q", "README.md")
	write(t, e.root, "NOTES.md", "mine\n")

	root := placed(t, e)
	if got := output(t, e.root, "branch", "--show-current"); got != "master" {
		t.Fatalf("the main checkout should move to master, it is on %q", got)
	}
	if got := output(t, root, "branch", "--show-current"); got != "pass" {
		t.Fatalf("the worktree should hold pass, it holds %q", got)
	}
	if exists(filepath.Join(root, "README.md")) {
		t.Fatal("the staged deletion did not move with the branch")
	}
	if out := output(t, root, "status", "--porcelain"); !strings.Contains(out, "README.md") {
		t.Fatalf("the worktree should hold the deletion uncommitted:\n%s", out)
	}
	if out := output(t, e.root, "status", "--porcelain"); !strings.Contains(out, "NOTES.md") ||
		strings.Contains(out, "README.md") {
		t.Fatalf("only the author's path should stay in the main checkout:\n%s", out)
	}
	for _, dir := range []string{e.root, root} {
		if out := output(t, dir, "stash", "list"); out != "" {
			t.Fatalf("the stash should be empty in %s:\n%s", dir, out)
		}
	}
}

func TestAStagedRenameMovesWithTheBranch(t *testing.T) {
	e := mainRepo(t)
	onBranch(t, e)
	e.cfg.AuthorPaths = []string{"NOTES.md"}
	git(t, e.root, "switch", "-q", "pass")
	write(t, e.root, "old.txt", "kept\n")
	git(t, e.root, "add", "old.txt")
	git(t, e.root, "commit", "-q", "-m", "old")
	git(t, e.root, "mv", "old.txt", "new.txt")
	write(t, e.root, "NOTES.md", "mine\n")

	root := placed(t, e)
	if got := output(t, e.root, "branch", "--show-current"); got != "master" {
		t.Fatalf("the main checkout should move to master, it is on %q", got)
	}
	if got := output(t, root, "branch", "--show-current"); got != "pass" {
		t.Fatalf("the worktree should hold pass, it holds %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "new.txt")); string(got) != "kept\n" {
		t.Fatalf("the staged rename did not move with the branch: %q", got)
	}
	if exists(filepath.Join(root, "old.txt")) {
		t.Fatal("the old name should be gone in the worktree")
	}
	if out := output(t, root, "status", "--porcelain"); !strings.Contains(out, "new.txt") ||
		!strings.Contains(out, "old.txt") {
		t.Fatalf("the worktree should hold the rename uncommitted:\n%s", out)
	}
	if out := output(t, e.root, "status", "--porcelain"); !strings.Contains(out, "NOTES.md") ||
		strings.Contains(out, "old.txt") || strings.Contains(out, "new.txt") {
		t.Fatalf("only the author's path should stay in the main checkout:\n%s", out)
	}
	for _, dir := range []string{e.root, root} {
		if out := output(t, dir, "stash", "list"); out != "" {
			t.Fatalf("the stash should be empty in %s:\n%s", dir, out)
		}
	}
}

func TestAFailedHandOffPutsTheChangesBack(t *testing.T) {
	e := mainRepo(t)
	onBranch(t, e)
	e.cfg.AuthorPaths = []string{"NOTES.md"}
	git(t, e.root, "switch", "-q", "pass")
	write(t, e.root, "README.md", "half done\n")
	write(t, e.root, "half.go", "package half\n")
	write(t, e.root, "gone.txt", "gone\n")
	write(t, e.root, "old.txt", "old\n")
	git(t, e.root, "add", "gone.txt", "old.txt")
	git(t, e.root, "commit", "-q", "-m", "files")
	git(t, e.root, "rm", "-q", "gone.txt")
	git(t, e.root, "mv", "old.txt", "new.txt")
	write(t, e.root, "NOTES.md", "mine\n")
	if err := os.MkdirAll(worktreesDir(e.root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), worktreePath(e.root, "pass")); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	c, err := e.find(ctx, "PASS")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.place(ctx, c); err == nil {
		t.Fatal("a worktree cannot be made where the path is already taken")
	}
	if got := output(t, e.root, "branch", "--show-current"); got != "pass" {
		t.Fatalf("the main checkout should be back on pass, it is on %q", got)
	}
	for path, want := range map[string]string{"README.md": "half done\n", "half.go": "package half\n", "new.txt": "old\n"} {
		if got, _ := os.ReadFile(filepath.Join(e.root, path)); string(got) != want {
			t.Fatalf("%s was not put back in the main checkout: %q", path, got)
		}
	}
	if exists(filepath.Join(e.root, "gone.txt")) || exists(filepath.Join(e.root, "old.txt")) {
		t.Fatal("the staged deletion and the old name should be back as staged deletions")
	}
	if out := output(t, e.root, "stash", "list"); out != "" {
		t.Fatalf("the stash should be empty:\n%s", out)
	}
}

func TestAReviewFromMasterIsWrittenInTheBranchsWorktree(t *testing.T) {
	e := mainRepo(t)
	onBranch(t, e)

	cmd := newReview(e)
	cmd.SetArgs([]string{"PASS", "--write-only", "--triage-agent", "claude", "--triage-model", "opus", "--triage-effort", "high"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	tree := worktreePath(e.root, "pass")
	if !exists(filepath.Join(tree, "docs/plans/PASS_REVIEW.md")) {
		t.Fatal("the review should be written beside the plan, in pass's worktree")
	}
	if got := output(t, tree, "log", "-1", "--format=%s"); got != "plans: review PASS" {
		t.Fatalf("the review should be committed on pass, last commit %q", got)
	}
	if exists(filepath.Join(e.root, "docs/plans")) {
		t.Fatal("master's checkout should be untouched")
	}
}

func TestClosingAPlanOnlyOnABranchWorksFromMaster(t *testing.T) {
	e := mainRepo(t)
	git(t, e.root, "switch", "-q", "-c", "pass")
	write(t, e.root, "docs/plans/PASS.md", finishedPlan)
	write(t, e.root, "docs/plans/PASS.state.json", finishedState)
	git(t, e.root, "add", "-A")
	git(t, e.root, "commit", "-q", "-m", "plans")
	git(t, e.root, "switch", "-q", "master")

	if err := closePlan(context.Background(), e, []string{"PASS"}, false, false); err != nil {
		t.Fatal(err)
	}
	if got := output(t, e.root, "log", "-1", "--format=%s", "pass"); got != "plans: close PASS" {
		t.Fatalf("the plan should be closed on pass, last commit %q", got)
	}
	if exists(worktreePath(e.root, "pass")) {
		t.Fatal("the worktree made to close it should go again")
	}
}

func TestBranchNamesComeFromThePlan(t *testing.T) {
	for name, want := range map[string]string{
		"docs/plans/PLAYTEST_FIXES.md":   "playtest-fixes",
		"docs/plans/ENGINE_EXECUTION.md": "engine",
		"docs/plans/Crowd Pass (v2).md":  "crowd-pass-v2",
		"docs/plans/renderer-2.md":       "renderer-2",
	} {
		p := planAt(t, name)
		if got := branchFor(p); got != want {
			t.Errorf("%s runs on %q, want %q", name, got, want)
		}
	}
}

func planAt(t *testing.T, path string) *plan.Plan {
	t.Helper()
	return plan.Parse(openPlan, path)
}
