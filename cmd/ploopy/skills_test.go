package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ploopy "github.com/Torwalt/ploopy"
	"github.com/Torwalt/ploopy/internal/config"
)

func read(t *testing.T, path string) string {
	t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func TestSkillsAreWrittenBesideTheLocalOne(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, config.SkillsDir)
	if err := writeSkills(dir, "", false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ploopy", "plan-writing", "plan-unit", "plan-review", "review-triage", "land-repair"} {
		text := read(t, filepath.Join(dir, name, "SKILL.md"))
		if !strings.HasPrefix(text, "---\nname: "+name+"\n") || !generated(text) {
			t.Fatalf("%s should keep its front matter first and be marked as ploopy's:\n%.300s", name, text)
		}
		if !strings.Contains(text, "`## "+name+"` section of the ploopy-local skill") {
			t.Fatalf("%s should point at its local section", name)
		}
	}
	if local := read(t, filepath.Join(dir, "ploopy-local", "SKILL.md")); !strings.Contains(local, "## plan-unit") {
		t.Fatalf("the local skill should have a section per skill:\n%s", local)
	}
}

func TestWritingSkillsAgainKeepsWhatTheRepositoryWrote(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, config.SkillsDir)
	write(t, dir, "plan-writing/SKILL.md", "our own\n")
	write(t, dir, "plan-unit/SKILL.md", written+" stale -->\n")
	write(t, dir, "ploopy-local/SKILL.md", "## plan-unit\n\nours\n")

	if err := writeSkills(dir, "", false); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "plan-writing", "SKILL.md")); got != "our own\n" {
		t.Fatalf("the repository's own skill should be kept, got %.100s", got)
	}
	if got := read(t, filepath.Join(dir, "plan-unit", "SKILL.md")); strings.Contains(got, "stale") {
		t.Fatal("ploopy's own copy should be rewritten")
	}
	if got := read(t, filepath.Join(dir, "ploopy-local", "SKILL.md")); got != "## plan-unit\n\nours\n" {
		t.Fatalf("the local skill should be kept, got %q", got)
	}

	if err := writeSkills(dir, "", true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "plan-writing", "SKILL.md")); !generated(got) {
		t.Fatal("--force should overwrite the repository's own")
	}
}

func TestSkillsAreLinkedForClaude(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, config.SkillsDir)
	link := filepath.Join(root, ".claude", "skills")
	write(t, link, "plan-review/keep", "theirs\n")

	for range 2 {
		if err := writeSkills(dir, link, false); err != nil {
			t.Fatal(err)
		}
	}
	if got := read(t, filepath.Join(link, "plan-unit", "SKILL.md")); !generated(got) {
		t.Fatal("the link should lead to the written skill")
	}
	if target, _ := os.Readlink(filepath.Join(link, "ploopy-local")); target != "../../.agents/skills/ploopy-local" {
		t.Fatalf("the link should be relative, got %q", target)
	}
	if _, err := os.Readlink(filepath.Join(link, "plan-review")); err == nil {
		t.Fatal("a directory in the way should stay")
	}
}

func TestSessionsGetTheBinarysSkillAndTheRepositorysRules(t *testing.T) {
	root := t.TempDir()
	if err := writeSkills(filepath.Join(root, config.SkillsDir), "", false); err != nil {
		t.Fatal(err)
	}
	write(t, root, config.LocalSkillPaths[0], strings.Replace(localTemplate,
		"## plan-unit\n", "## plan-unit\n\nRun `just test-one`.\n\n```\n## not a heading\n```\n", 1))

	got, err := skillAt(root, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, ploopy.PlanUnitSkill[:200]) || generated(got) {
		t.Fatal("a skill ploopy wrote should give way to the binary's")
	}
	if !strings.HasSuffix(got, "Run `just test-one`.\n\n```\n## not a heading\n```\n") {
		t.Fatalf("the repository's rules should follow the skill:\n%s", got[len(got)-300:])
	}
	if review := ownOr(root, config.RepoReviewSkill, ploopy.PlanReviewSkill); review != ploopy.PlanReviewSkill {
		t.Fatal("an empty section adds nothing")
	}

	write(t, root, config.RepoSkill, "our own unit skill\n")
	if got, _ := skillAt(root, config.Default()); !strings.HasPrefix(got, "our own unit skill\n\n## This repository's rules") {
		t.Fatalf("the repository's own skill wins, with its rules after it:\n%s", got)
	}
}
