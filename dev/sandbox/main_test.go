package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestPrepareBuildsTheSandbox(t *testing.T) {
	dir := t.TempDir()
	if err := prepare(dir); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(dir, "bin")
	for _, name := range []string{"ploopy", "claude", "codex", "opencode"} {
		if info, err := os.Stat(filepath.Join(bin, name)); err != nil || info.IsDir() {
			t.Fatalf("%s is not in bin: %v", name, err)
		}
	}
	for _, name := range []string{"claude", "codex", "opencode"} {
		target, err := os.Readlink(filepath.Join(bin, name))
		if err != nil {
			t.Fatalf("%s is not a link: %v", name, err)
		}
		if target != "fakeagent" {
			t.Fatalf("%s links to %s, want fakeagent", name, target)
		}
	}

	repo := filepath.Join(dir, "repo")
	if dirt := gitOutput(t, repo, "status", "--porcelain"); dirt != "" {
		t.Fatalf("the toy repository is not committed:\n%s", dirt)
	}
	if head := strings.TrimSpace(gitOutput(t, repo, "log", "-1", "--format=%s")); head != "Set up the sandbox" {
		t.Fatalf("the toy repository's head is %q", head)
	}

	lint := exec.Command(filepath.Join(bin, "ploopy"), "lint", "TOY")
	lint.Dir = repo
	lint.Env = testEnv(dir)
	if output, err := lint.CombinedOutput(); err != nil {
		t.Fatalf("the toy plan does not lint: %v\n%s", err, output)
	} else if !strings.Contains(string(output), "ok") {
		t.Fatalf("lint said %s", output)
	}

	var scenario map[string]any
	if _, err := toml.DecodeFile(filepath.Join(dir, "scenario.toml"), &scenario); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex", "opencode"} {
		if _, ok := scenario[name]; !ok {
			t.Fatalf("scenario.toml has no [%s] table", name)
		}
	}
}

func TestRunLandsTwoUnitsInTheSandbox(t *testing.T) {
	dir := t.TempDir()
	if err := prepare(dir); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run := exec.CommandContext(ctx, filepath.Join(dir, "bin", "ploopy"),
		"run", "--plan", "TOY", "--agent", "opencode", "--effort", "high", "--units", "2")
	run.Dir = filepath.Join(dir, "repo")
	run.Env = testEnv(dir)
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("the run failed: %v\n%s", err, output)
	}

	repo := filepath.Join(dir, "repo")
	log := gitOutput(t, repo, "log", "--format=%s", "toy")
	if got := strings.Count(log, "Do unit "); got != 2 {
		t.Fatalf("the branch has %d unit commits, want 2:\n%s", got, log)
	}
	for _, id := range []string{"1.1", "1.2"} {
		if !strings.Contains(log, "Do unit "+id) {
			t.Fatalf("unit %s did not land:\n%s", id, log)
		}
	}

	work := gitOutput(t, repo, "show", "toy:work.txt")
	for _, id := range []string{"1.1", "1.2"} {
		if !strings.Contains(work, "unit "+id+" by opencode") {
			t.Fatalf("work.txt has no line for %s:\n%s", id, work)
		}
	}

	var state struct {
		Units map[string]struct {
			Status string `json:"status"`
		} `json:"units"`
	}
	if err := json.Unmarshal([]byte(gitOutput(t, repo, "show", "toy:docs/plans/TOY.state.json")), &state); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1.1", "1.2"} {
		if state.Units[id].Status != "landed" {
			t.Fatalf("unit %s is %q, want landed", id, state.Units[id].Status)
		}
	}
	if _, ok := state.Units["1.3"]; ok {
		t.Fatal("the run landed a third unit")
	}
}

// testEnv is the sandbox's environment, with a home of its own so a developer's
// git and ploopy settings cannot reach the test.
func testEnv(dir string) []string {
	home := filepath.Join(dir, "home")
	_ = os.MkdirAll(home, 0o755)
	return setEnv(sandboxEnv(dir), "HOME", home)
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
