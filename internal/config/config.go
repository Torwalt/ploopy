// Package config is the per-repository settings file, `.ploopy.toml` at the
// repository root.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

// File is the settings file's name.
const File = ".ploopy.toml"

// Config is what a repository tells the loop about itself.
type Config struct {
	Plans       string   `toml:"plans"`        // where task documents live
	Context     []string `toml:"context"`      // read first by every session
	Verify      string   `toml:"verify"`       // run after every unit
	Test        string   `toml:"test"`         // run after units that want tests
	Setup       string   `toml:"setup"`        // readies a fresh worktree before its first check
	AuthorPaths []string `toml:"author_paths"` // the author's files, never the session's dirt
	Skill       string   `toml:"skill"`        // overrides the bundled plan-unit skill
	StateCommit string   `toml:"state_commit"` // message template for the progress commit
}

// Default is what a repository without a settings file gets.
func Default() Config {
	return Config{
		Plans:       "docs/plans",
		Context:     []string{"AGENTS.md"},
		StateCommit: "plans: record %s progress",
	}
}

// Load reads the settings file at the repository root. A missing file is the
// default, not an error.
func Load(root string) (Config, error) {
	cfg := Default()
	path := filepath.Join(root, File)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}

	meta, err := toml.Decode(string(raw), &cfg)
	if err != nil {
		return cfg, fmt.Errorf("%s is not valid TOML: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		var unknown []string
		for _, key := range undecoded {
			unknown = append(unknown, key.String())
		}
		sort.Strings(unknown)
		return cfg, fmt.Errorf("%s: unknown key(s) %v", path, unknown)
	}
	if cfg.Plans == "" {
		cfg.Plans = Default().Plans
	}
	if cfg.StateCommit == "" {
		cfg.StateCommit = Default().StateCommit
	}
	return cfg, nil
}

// RepoSkill is where a repository may keep its own plan-unit skill.
const RepoSkill = ".agents/skills/plan-unit/SKILL.md"

// SkillPath is the plan-unit skill every session is given: the one the
// settings name, else the repository's own, else the bundled one.
func (c Config) SkillPath(root, bundled string) string {
	if c.Skill != "" {
		return filepath.Join(root, c.Skill)
	}
	if own := filepath.Join(root, RepoSkill); isFile(own) {
		return own
	}
	return bundled
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
