package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "work"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write(t, root, "README.md", "start\n")
	commit(t, root, "first")
	return root
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

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func commit(t *testing.T, root, message string) {
	t.Helper()
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", message)
}

func TestDirtExcludesTheAuthorsPaths(t *testing.T) {
	root := newRepo(t)
	write(t, root, "NOTES.md", "the author is thinking\n")
	write(t, root, "src/main.go", "package main\n")

	r := New(root, []string{"NOTES.md"})
	dirt, err := r.DirtLines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(dirt, "\n")
	if strings.Contains(joined, "NOTES.md") {
		t.Fatalf("the author's file was counted as dirt: %q", joined)
	}
	if !strings.Contains(joined, "src/main.go") {
		t.Fatalf("the session's file is missing from the dirt: %q", joined)
	}
}

func TestAuthorPatternsMatchDirectoriesAndBaseNames(t *testing.T) {
	r := New("/nowhere", []string{"docs/notes", "*.local.md", "TODO.md"})
	for _, path := range []string{"docs/notes/one.md", "docs/notes", "x.local.md", "TODO.md"} {
		if !r.IsAuthors(path) {
			t.Fatalf("%q should belong to the author", path)
		}
	}
	for _, path := range []string{"docs/plans/PASS.md", "src/main.go", "TODO.md.bak"} {
		if r.IsAuthors(path) {
			t.Fatalf("%q should not belong to the author", path)
		}
	}
}

// Status is read with -z, so a rename and a path with a space survive it.
func TestARenameAndASpacedPathAreClassified(t *testing.T) {
	root := newRepo(t)
	write(t, root, "with space.md", "x\n")
	commit(t, root, "add a spaced path")
	git(t, root, "mv", "README.md", "READZZZ.md")
	write(t, root, "with space.md", "changed\n")

	changes, err := New(root, nil).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, change := range changes {
		paths = append(paths, change.Path)
	}
	for _, want := range []string{"READZZZ.md", "with space.md"} {
		if !containsString(paths, want) {
			t.Fatalf("status lost %q: %v", want, paths)
		}
	}
	for _, change := range changes {
		if change.Path == "READZZZ.md" && change.From != "README.md" {
			t.Fatalf("the rename lost its original path: %+v", change)
		}
	}
}

func TestARenamedAuthorFileStaysTheAuthors(t *testing.T) {
	root := newRepo(t)
	write(t, root, "NOTES.md", "x\n")
	commit(t, root, "add notes")
	git(t, root, "mv", "NOTES.md", "NOTES-renamed.md")

	dirt, err := New(root, []string{"NOTES-renamed.md"}).DirtLines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dirt) != 0 {
		t.Fatalf("the author's rename was counted as dirt: %v", dirt)
	}
}

func TestCommitsSinceABaseLeaveOutTheAuthors(t *testing.T) {
	root := newRepo(t)
	ctx := context.Background()
	r := New(root, []string{"NOTES.md"})

	base, err := r.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "src/main.go", "package main\n")
	commit(t, root, "the unit's work")
	write(t, root, "NOTES.md", "the author's thought\n")
	commit(t, root, "the author's note")

	commits, err := r.Commits(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0].Subject != "the unit's work" {
		t.Fatalf("commits %+v", commits)
	}
}

func TestContainsKnowsWhetherABaseStillApplies(t *testing.T) {
	root := newRepo(t)
	ctx := context.Background()
	r := New(root, nil)

	base, _ := r.Head(ctx)
	if !r.Contains(ctx, base) {
		t.Fatal("HEAD should contain itself")
	}
	if r.Contains(ctx, "0000000000000000000000000000000000000000") {
		t.Fatal("an unknown commit is not an ancestor")
	}
}

func TestCommitOnlyLeavesEverythingElseAlone(t *testing.T) {
	root := newRepo(t)
	ctx := context.Background()
	write(t, root, "state.json", "{}\n")
	write(t, root, "other.go", "package other\n")

	if err := New(root, nil).CommitOnly(ctx, "state.json", "record progress"); err != nil {
		t.Fatal(err)
	}
	status := git(t, root, "status", "--porcelain")
	if !strings.Contains(status, "other.go") {
		t.Fatalf("the other file was swept into the commit: %q", status)
	}
	if strings.Contains(status, "state.json") {
		t.Fatalf("the state file was not committed: %q", status)
	}
}

func TestRootFindsTheRepository(t *testing.T) {
	root := newRepo(t)
	write(t, root, "deep/inside/file.txt", "x\n")

	found, err := Root(context.Background(), filepath.Join(root, "deep/inside"))
	if err != nil {
		t.Fatal(err)
	}
	wanted, _ := filepath.EvalSymlinks(root)
	got, _ := filepath.EvalSymlinks(found)
	if got != wanted {
		t.Fatalf("root %q, want %q", got, wanted)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
