package catalog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const pass = `# Pass

## Stage 1 — Only

### 1.1 One

**Goal:** g.

**Done when:** d.

### 1.2 Two

**Goal:** g.

**Done when:** d.
`

func progress(units ...string) string {
	var entries []string
	for i, id := range units {
		entries = append(entries, `"`+id+`": {"status": "landed", "outcome": "done", "date": "2026-10-0`+
			string(rune('1'+i))+`T22:00:00+02:00"}`)
	}
	return "{\"units\": {" + strings.Join(entries, ",") + "}}\n"
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func write(t *testing.T, root, path, text string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, root, message string) {
	t.Helper()
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", message)
}

// repository is a main checkout on master holding PASS with no progress.
func repository(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "master")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	write(t, root, "docs/plans/PASS.md", pass)
	commitAll(t, root, "plan")
	return root
}

func find(t *testing.T, root string) []Entry {
	t.Helper()
	entries, err := Find(context.Background(), root, "docs/plans")
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestProgressInAWorktreeWinsOverThisCheckout(t *testing.T) {
	root := repository(t)
	tree := filepath.Join(t.TempDir(), "work")
	git(t, root, "worktree", "add", "-q", "-b", "work", tree)
	write(t, tree, "docs/plans/PASS.state.json", progress("1.1"))

	entries := find(t, root)
	if len(entries) != 1 {
		t.Fatalf("entries %+v", entries)
	}
	best := entries[0].Best
	if done, total := best.Done(); done != 1 || total != 2 || best.Branch != "work" || best.Here {
		t.Fatalf("best is %d/%d on %q (here %v)", done, total, best.Branch, best.Here)
	}
	if len(entries[0].Others) != 1 || !entries[0].Others[0].Here {
		t.Fatalf("others %+v", entries[0].Others)
	}
	if where := best.Where(root); !strings.HasPrefix(where, "work in ") {
		t.Fatalf("where %q", where)
	}
}

func TestAPlanOnlyOnABranchIsFound(t *testing.T) {
	root := repository(t)
	git(t, root, "switch", "-q", "-c", "feature")
	write(t, root, "docs/plans/OTHER.md", strings.Replace(pass, "# Pass", "# Other", 1))
	write(t, root, "docs/plans/OTHER.state.json", progress("1.1", "1.2"))
	commitAll(t, root, "other plan")
	git(t, root, "switch", "-q", "master")

	var other *Entry
	entries := find(t, root)
	for i := range entries {
		if entries[i].Path() == "docs/plans/OTHER.md" {
			other = &entries[i]
		}
	}
	if other == nil {
		t.Fatal("the plan on the feature branch was not found")
	}
	if done, _ := other.Best.Done(); done != 2 || other.Best.Branch != "feature" || other.Best.Root != "" {
		t.Fatalf("best %+v", other.Best)
	}
	if where := other.Best.Where(root); where != "branch feature" {
		t.Fatalf("where %q", where)
	}
}

// A branch merged into the default one is done with; its plans were closed.
func TestAMergedBranchIsLeftOut(t *testing.T) {
	root := repository(t)
	git(t, root, "switch", "-q", "-c", "closed")
	write(t, root, "docs/plans/OLD.md", strings.Replace(pass, "# Pass", "# Old", 1))
	commitAll(t, root, "old plan")
	git(t, root, "switch", "-q", "master")
	git(t, root, "merge", "-q", "--ff-only", "closed")
	git(t, root, "rm", "-q", "docs/plans/OLD.md")
	commitAll(t, root, "close the old plan")

	for _, entry := range find(t, root) {
		if entry.Path() == "docs/plans/OLD.md" {
			t.Fatalf("a closed plan came back from a merged branch: %+v", entry.Best)
		}
	}
}

func TestWithNoProgressAnywhereThisCheckoutIsShown(t *testing.T) {
	root := repository(t)
	git(t, root, "branch", "idle")
	git(t, root, "switch", "-q", "idle")
	write(t, root, "x.txt", "x\n")
	commitAll(t, root, "unrelated")
	git(t, root, "switch", "-q", "master")

	entries := find(t, root)
	if len(entries) != 1 || !entries[0].Best.Here {
		t.Fatalf("entries %+v", entries)
	}
}

// A branch forked before a plan was closed still holds the plan; it is closed
// all the same.
func TestAPlanClosedOnOneBranchStaysClosedOnItsForks(t *testing.T) {
	root := repository(t)
	git(t, root, "switch", "-q", "-c", "feature")
	write(t, root, "docs/plans/OTHER.md", strings.Replace(pass, "# Pass", "# Other", 1))
	write(t, root, "docs/plans/OTHER.state.json", progress("1.1", "1.2"))
	commitAll(t, root, "other plan")
	git(t, root, "branch", "fork")
	git(t, root, "rm", "-q", "docs/plans/OTHER.md", "docs/plans/OTHER.state.json")
	commitAll(t, root, "close the other plan")
	git(t, root, "switch", "-q", "master")

	for _, entry := range find(t, root) {
		if entry.Path() == "docs/plans/OTHER.md" {
			t.Fatalf("a closed plan came back from a fork: %+v", entry.Best)
		}
	}

	// Made again after it was closed, it is a new plan.
	git(t, root, "switch", "-q", "feature")
	write(t, root, "docs/plans/OTHER.md", strings.Replace(pass, "# Pass", "# Other", 1))
	commitAll(t, root, "the other plan again")
	git(t, root, "switch", "-q", "master")
	found := false
	for _, entry := range find(t, root) {
		if entry.Path() == "docs/plans/OTHER.md" && entry.Best.Branch == "feature" {
			found = true
		}
	}
	if !found {
		t.Fatal("a plan made again after it was closed was left out")
	}
}
