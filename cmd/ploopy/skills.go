package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	ploopy "github.com/Torwalt/ploopy"
	"github.com/Torwalt/ploopy/internal/config"
)

// written marks a skill file `ploopy skills` wrote. Such a file is ploopy's,
// not the repository's own, so a run uses the binary's newer copy over it.
const written = "<!-- Written by `ploopy skills`, which rewrites it."

const localTemplate = `---
name: ploopy-local
description: This repository's own rules for ploopy's skills — writing plans, carrying out units, reviewing, triage, landing, and working with ploopy here. Read the section named after the ploopy skill in use, alongside that skill.
---

# ploopy in this repository

ploopy's skills, which ` + "`ploopy skills`" + ` writes beside this one, are the same
in every repository. This file adds what is particular to this one, under a
heading per skill. ploopy hands each section to the sessions it runs with that
skill, after the skill; an agent working by hand reads it alongside the skill.
A rule here wins over the skill. A section left empty adds nothing.

## ploopy

## plan-writing

## plan-unit

## plan-review

## review-triage

## land-repair
`

func newSkills(e *env) *cobra.Command {
	var force, noLink bool
	cmd := &cobra.Command{
		Use:   "skills [DIR]",
		Short: "Write ploopy's skills into the repository, beside the skill that extends them",
		Long: "Write every skill ploopy ships into DIR (" + config.SkillsDir + " unless given),\n" +
			"a directory each, and " + config.LocalSkill + ", where the repository keeps its own\n" +
			"rules for them, a section per skill.\n\n" +
			"ploopy's skills are rewritten on every call, so they follow the binary.\n" +
			"A skill file ploopy did not write is the repository's own and is kept\n" +
			"unless --force; so is " + config.LocalSkill + " once it exists. Runs give each\n" +
			"session the binary's skill and the matching section of " + config.LocalSkill + ".\n\n" +
			"When the repository has a .claude directory, each skill is linked into\n" +
			".claude/skills as well.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := config.SkillsDir
			if len(args) > 0 {
				dir = args[0]
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(e.root, dir)
			}
			link := ""
			if info, err := os.Stat(filepath.Join(e.root, ".claude")); err == nil && info.IsDir() && !noLink {
				link = filepath.Join(e.root, ".claude", "skills")
			}
			return writeSkills(dir, link, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite skill files the repository wrote itself")
	cmd.Flags().BoolVar(&noLink, "no-link", false, "do not link the skills into .claude/skills")
	return cmd
}

// writeSkills writes ploopy's skills and the local one into dir, and links
// each into link when it is given.
func writeSkills(dir, link string, force bool) error {
	names, err := shippedSkills()
	if err != nil {
		return err
	}
	for _, name := range names {
		text, err := ploopy.Skills.ReadFile("skills/" + name + "/SKILL.md")
		if err != nil {
			return err
		}
		path := filepath.Join(dir, name, "SKILL.md")
		if old, err := os.ReadFile(path); err == nil && !generated(string(old)) && !force {
			fmt.Printf("kept %s: the repository wrote it; move its rules into %s and pass --force\n", path, config.LocalSkill)
			continue
		}
		if err := writeFile(path, stamped(string(text), name)); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", path)
	}

	local := filepath.Join(dir, config.LocalSkill, "SKILL.md")
	if _, err := os.Stat(local); os.IsNotExist(err) {
		if err := writeFile(local, localTemplate); err != nil {
			return err
		}
		fmt.Printf("wrote %s; add this repository's rules there\n", local)
	} else {
		fmt.Printf("kept %s\n", local)
	}

	if link == "" {
		return nil
	}
	for _, name := range append(names, config.LocalSkill) {
		if err := linkSkill(filepath.Join(dir, name), filepath.Join(link, name)); err != nil {
			fmt.Printf("did not link %s: %v\n", name, err)
		}
	}
	return nil
}

func shippedSkills() ([]string, error) {
	entries, err := fs.ReadDir(ploopy.Skills, "skills")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// stamped is a shipped skill as written into a repository: marked as ploopy's,
// and pointing at the repository's own rules for it.
func stamped(text, name string) string {
	head, body := "", text
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[4:], "\n---\n"); end >= 0 {
			head, body = text[:4+end+5], text[4+end+5:]
		}
	}
	return head + written + " Put this repository's own rules in the " + config.LocalSkill + " skill. -->\n\n" +
		"Read the `## " + name + "` section of the " + config.LocalSkill + " skill (`" + config.LocalSkillPaths[0] +
		"`) first, when it has one: its rules for this repository win over this skill.\n" + body
}

func generated(text string) bool { return strings.Contains(text, written) }

func writeFile(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// linkSkill links a skill's directory from where an agent looks for skills.
// Whatever is there already stays, unless it is that very link.
func linkSkill(dir, at string) error {
	target, err := filepath.Rel(filepath.Dir(at), dir)
	if err != nil {
		return err
	}
	if existing, err := os.Readlink(at); err == nil {
		if existing == target {
			return nil
		}
		return fmt.Errorf("%s links to %s", at, existing)
	}
	if _, err := os.Lstat(at); err == nil {
		return fmt.Errorf("%s is in the way", at)
	}
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(target, at); err != nil {
		return err
	}
	fmt.Printf("linked %s\n", at)
	return nil
}

// localRules is the repository's own rules for a skill: its section of the
// local skill, or empty.
func localRules(root, name string) string {
	for _, path := range config.LocalSkillPaths {
		if text, err := os.ReadFile(filepath.Join(root, path)); err == nil {
			return section(string(text), name)
		}
	}
	return ""
}

// section is the body under a `## name` heading, up to the next heading of
// that level. Headings inside code fences do not count.
func section(text, name string) string {
	var out []string
	in, fenced := false, false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		}
		if !fenced && strings.HasPrefix(line, "## ") {
			in = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == name
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// withLocalRules adds the repository's own rules for a skill after it.
func withLocalRules(root, name, skill string) string {
	rules := localRules(root, name)
	if rules == "" {
		return skill
	}
	return strings.TrimRight(skill, "\n") + "\n\n## This repository's rules\n\nThey win over the skill above.\n\n" + rules + "\n"
}
