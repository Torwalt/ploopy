// Command sandbox prepares a throwaway repository and shell for trying ploopy
// without real sessions or real plans.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: sandbox [DIR]")
		os.Exit(2)
	}
	dir := ""
	if len(os.Args) == 2 {
		dir = os.Args[1]
	}
	var err error
	if dir == "" {
		dir, err = os.MkdirTemp("", "ploopy-sandbox-")
	} else {
		dir, err = filepath.Abs(dir)
	}
	if err != nil {
		fail(err)
	}
	if err := prepare(dir); err != nil {
		fail(err)
	}
	if err := enter(dir); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "sandbox:", err)
	os.Exit(1)
}

// prepare builds the sandbox into dir. Tests call it without entering a shell.
func prepare(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, state := range []string{"state", "run"} {
		if err := os.MkdirAll(filepath.Join(dir, state), 0o700); err != nil {
			return err
		}
	}
	if err := buildBinaries(dir); err != nil {
		return err
	}
	if err := writeScenario(dir); err != nil {
		return err
	}
	return createRepo(filepath.Join(dir, "repo"))
}

func buildBinaries(dir string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	build := func(name, pkg string) error {
		out := filepath.Join(bin, name)
		cmd := exec.Command("go", "build", "-o", out, pkg)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go build %s: %w\n%s", pkg, err, output)
		}
		return nil
	}
	if err := build("ploopy", "./cmd/ploopy"); err != nil {
		return err
	}
	if err := build("fakeagent", "./dev/fakeagent"); err != nil {
		return err
	}
	for _, name := range []string{"claude", "codex", "opencode"} {
		link := filepath.Join(bin, name)
		_ = os.Remove(link)
		if err := os.Symlink("fakeagent", link); err == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(bin, "fakeagent"))
		if err != nil {
			return err
		}
		if err := os.WriteFile(link, data, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// moduleRoot is the repository this command was built from.
func moduleRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot find the sandbox source")
	}
	dir := filepath.Dir(file)
	for {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no go.mod above %s", file)
}

const defaultScenario = `[claude]
seconds = 6                 # how long a session takes
session_used = 0.40         # the five-hour window now
session_resets_in = "2h"
weekly_used = 0.51
weekly_resets_in = "96h"
per_session = 0.15          # share of the five-hour window one session takes
per_session_weekly = 0.01
cost = 0.80                 # dollars a session reports
outcome = "done"            # done | blocked | nothing | red | dirty | limit | logged-out

[codex]
seconds = 6
weekly_used = 0.10
weekly_resets_in = "24h"    # no session window: the next session opens one
per_session = 0.20
per_session_weekly = 0.02
outcome = "done"

[opencode]
seconds = 4
cost = 0.05
outcome = "done"
`

func writeScenario(dir string) error {
	path := filepath.Join(dir, "scenario.toml")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(defaultScenario), 0o644)
}

const toyPlan = `# Toy plan

Eight units that only touch ` + "`work.txt`" + `, for trying ploopy in the sandbox.

## Stage 1 — Toy work

### 1.1 First line

**Goal:** append ` + "`unit 1.1`" + ` to ` + "`work.txt`" + `.
**Done when:** it is committed.

### 1.2 Second line

**Goal:** append ` + "`unit 1.2`" + ` to ` + "`work.txt`" + `.
**Done when:** it is committed.

### 1.3 Third line

**Goal:** append ` + "`unit 1.3`" + ` to ` + "`work.txt`" + `.
**Done when:** it is committed.

### 1.4 Fourth line

**Goal:** append ` + "`unit 1.4`" + ` to ` + "`work.txt`" + `.
**Done when:** it is committed.

### 1.5 Fifth line

**Goal:** append ` + "`unit 1.5`" + ` to ` + "`work.txt`" + `.
**Done when:** it is committed.

### 1.6 Sixth line

**Goal:** append ` + "`unit 1.6`" + ` to ` + "`work.txt`" + `.
**Done when:** it is committed.

### 1.7 Seventh line

**Goal:** append ` + "`unit 1.7`" + ` to ` + "`work.txt`" + `.
**Done when:** it is committed.

### 1.8 Eighth line

**Goal:** append ` + "`unit 1.8`" + ` to ` + "`work.txt`" + `.
**Done when:** it is committed.
`

func createRepo(repo string) error {
	if _, err := os.Stat(filepath.Join(repo, ".git")); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return err
	}
	if err := runGit(repo, "init", "-q"); err != nil {
		return err
	}
	if err := runGit(repo, "symbolic-ref", "HEAD", "refs/heads/master"); err != nil {
		return err
	}
	files := map[string]string{
		"AGENTS.md": `# Toy repository
Nothing here is real; ploopy may commit anything.
`,
		".ploopy.toml": `plans = "docs/plans"
verify = "test ! -e BROKEN"
`,
		"docs/plans/TOY.md": toyPlan,
	}
	for name, body := range files {
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	if err := runGit(repo, "add", "-A"); err != nil {
		return err
	}
	return runGit(repo,
		"-c", "user.name=ploopy", "-c", "user.email=ploopy@example.invalid",
		"commit", "-q", "-m", "Set up the sandbox")
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, output)
	}
	return nil
}

// sandboxEnv keeps the author's real leases, runs and shell state out of the
// sandbox.
func sandboxEnv(dir string) []string {
	env := os.Environ()
	env = setEnv(env, "PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	env = setEnv(env, "XDG_STATE_HOME", filepath.Join(dir, "state"))
	env = setEnv(env, "XDG_RUNTIME_DIR", filepath.Join(dir, "run"))
	env = setEnv(env, "PLOOPY_SANDBOX", dir)
	return env
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func enter(dir string) error {
	fmt.Printf(`ploopy sandbox: %s

  scenario: %s
  repo:     %s

  ploopy lend ...
  ploopy
  ploopy review TOY

The fake claude, codex and opencode are in %s; all state stays in %s.
`, dir, filepath.Join(dir, "scenario.toml"), filepath.Join(dir, "repo"),
		filepath.Join(dir, "bin"), dir)

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	cmd.Dir = filepath.Join(dir, "repo")
	cmd.Env = sandboxEnv(dir)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
