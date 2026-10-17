package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Torwalt/ploopy/internal/review"
)

var (
	implementRe = regexp.MustCompile(`unit (\d+\.\d+)`)
	fixesRe     = regexp.MustCompile("\\* `([^`]+)`, the fix plan")
	planRe      = regexp.MustCompile("at the end of `([^`]+)`")
)

// role is what a prompt asks the fake to do, read from its opening line.
func role(prompt string) string {
	text := strings.TrimSpace(prompt)
	switch {
	case strings.HasPrefix(text, "You are running unattended under the plan loop. You implement exactly one unit"):
		return "implement"
	case strings.HasPrefix(text, "You are running unattended under the plan loop, reviewing work"):
		return "collect"
	case strings.Contains(text, "it weighs the findings"):
		return "triage"
	}
	return ""
}

func quotedAfter(text, marker string) string {
	i := strings.LastIndex(text, marker)
	if i < 0 {
		return ""
	}
	rest := text[i+len(marker):]
	start := strings.Index(rest, "`")
	if start < 0 {
		return ""
	}
	rest = rest[start+1:]
	end := strings.Index(rest, "`")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// quotedInSection reads the marker from the named section. The skill text
// before the session block and the previous handover after it may quote the
// same phrase, and only the session block names this session's path.
func quotedInSection(text, heading, marker string) string {
	head := "\n" + heading + "\n"
	i := strings.Index(text, head)
	if i < 0 {
		return quotedAfter(text, marker)
	}
	rest := text[i+len(head):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return quotedAfter(rest, marker)
}

func act(role, harness, prompt, outcome string) error {
	switch role {
	case "implement":
		return implement(prompt, harness, outcome)
	case "collect":
		return collect(prompt)
	case "triage":
		return triage(prompt)
	}
	return nil
}

func implement(prompt, harness, outcome string) error {
	match := implementRe.FindStringSubmatch(prompt)
	if match == nil {
		return fmt.Errorf("the implementing prompt names no unit")
	}
	id := match[1]

	handover := quotedInSection(prompt, "## This session", "Write the handover to ")
	if handover == "" {
		return fmt.Errorf("the prompt names no handover path")
	}
	if err := writeFile(handover, "Implemented unit "+id+".\n"); err != nil {
		return err
	}
	if err := appendLine("work.txt", "unit "+id+" by "+harness); err != nil {
		return err
	}
	if outcome == "dirty" {
		return nil
	}

	files := []string{"work.txt"}
	if outcome == "red" {
		if err := writeFile("BROKEN", "the fake scenario asked for a red check\n"); err != nil {
			return err
		}
		files = append(files, "BROKEN")
	}
	return commit("Do unit "+id, files...)
}

func collect(prompt string) error {
	findings := quotedAfter(prompt, "Write your findings to ")
	if findings == "" {
		return fmt.Errorf("the prompt names no findings path")
	}
	return writeFile(findings, "No findings.\n")
}

func triage(prompt string) error {
	fixes := ""
	if match := fixesRe.FindStringSubmatch(prompt); match != nil {
		fixes = match[1]
	}
	plan := ""
	if match := planRe.FindStringSubmatch(prompt); match != nil {
		plan = match[1]
	}
	if fixes == "" || plan == "" {
		return fmt.Errorf("the triage prompt names no fix plan or review plan")
	}
	handover := quotedAfter(prompt, "Write a short handover to ")
	if handover == "" {
		return fmt.Errorf("the prompt names no handover path")
	}
	if err := writeFile(handover, "Wrote the fix plan.\n"); err != nil {
		return err
	}
	if err := writeFile(fixes, fixesDocument()); err != nil {
		return err
	}

	text, err := os.ReadFile(plan)
	if err != nil {
		return err
	}
	section := "\n\n" + review.AuthorHeading + "\n\nNo design choices needed a recommendation.\n"
	if err := writeFile(plan, strings.TrimRight(string(text), "\n")+section); err != nil {
		return err
	}
	return commit("Triage the findings", plan, fixes)
}

func fixesDocument() string {
	return `# Fix plan

## Stage 1 — Fixes

### 1.1 Fix the finding

**Goal:** resolve the finding this plan came from.
**Done when:** it no longer holds.

### 1.2 Check the fix

**Goal:** check the fix against the finding.
**Done when:** the finding is gone and the checks are green.
`
}

func appendLine(path, line string) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintln(file, line)
	return err
}

func writeFile(path, text string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func commit(message string, paths ...string) error {
	add := []string{"-c", "user.name=ploopy", "-c", "user.email=ploopy@example.invalid", "add", "--"}
	add = append(add, paths...)
	if out, err := exec.Command("git", add...).CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w: %s", err, out)
	}
	commit := []string{"-c", "user.name=ploopy", "-c", "user.email=ploopy@example.invalid",
		"commit", "-q", "-m", message}
	if out, err := exec.Command("git", commit...).CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w: %s", err, out)
	}
	return nil
}
