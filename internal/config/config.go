// Package config is the per-repository settings file, `.ploopy.toml` at the
// repository root.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Torwalt/ploopy/internal/harness"
)

// File is the settings file's name.
const File = ".ploopy.toml"

// Config is what a repository tells the loop about itself.
type Config struct {
	Plans       string           `toml:"plans"`            // where task documents live
	Context     []string         `toml:"context"`          // read first by every session
	Verify      string           `toml:"verify"`           // run after every unit
	Test        string           `toml:"test"`             // run after units that want tests
	Setup       string           `toml:"setup"`            // readies a fresh worktree before its first check; $PLOOPY_MAIN is the main checkout
	AuthorPaths []string         `toml:"author_paths"`     // the author's files, never the session's dirt
	Skill       string           `toml:"skill"`            // overrides the bundled plan-unit skill
	Checklist   string           `toml:"review_checklist"` // what review sessions check beside their skill
	StateCommit string           `toml:"state_commit"`     // message template for the progress commit
	CloseCommit string           `toml:"close_commit"`     // message template for the commit that closes a plan
	Push        bool             `toml:"push"`             // push the branch when a run ends
	PushSet     bool             `toml:"-"`                // push was written in the file, at either level
	Launch      string           `toml:"launch"`           // where a run goes: here, or tmux for a window of its own
	Agents      map[string]Agent `toml:"agents"`           // picker choices and usage-limit routes
	Run         Run              `toml:"run"`              // how a run starts
	Review      Review           `toml:"review"`           // who weighs a review's findings
	Automation  Automation       `toml:"automation"`       // what a run does on its own
}

// Run mirrors the flags that start a run.
type Run struct {
	Agent           string `toml:"agent"`
	Model           string `toml:"model"`
	Effort          string `toml:"effort"`
	Secondary       string `toml:"secondary"`
	SecondaryModel  string `toml:"secondary_model"`
	SecondaryEffort string `toml:"secondary_effort"`
	Peak            string `toml:"peak"`
	Subscriptions   string `toml:"subscriptions"`
	SpendAll        *bool  `toml:"spend_all"`
	Push            *bool  `toml:"push"`
	Finish          string `toml:"finish"`
}

// Review mirrors the flags that choose a review's triage.
type Review struct {
	TriageAgent  string   `toml:"triage_agent"`
	TriageModel  string   `toml:"triage_model"`
	TriageEffort string   `toml:"triage_effort"`
	TriageBudget *float64 `toml:"triage_budget"`
}

// Automation is what a run does without the author: follow-up for now, and
// the rescue later.
type Automation struct {
	FollowUp *bool `toml:"follow_up"`
}

type Agent struct {
	Models    []string          `toml:"models"`
	Efforts   []string          `toml:"efforts"`
	Fallbacks map[string]string `toml:"fallbacks"`
}

// Default is what a repository without a settings file gets.
func Default() Config {
	return Config{
		Plans:       "docs/plans",
		Context:     []string{"AGENTS.md"},
		StateCommit: "plans: record %s progress",
		CloseCommit: "plans: close %s",
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
	if meta.IsDefined("push") {
		cfg.PushSet = true
	}
	if cfg.Run.Push != nil {
		cfg.Push = *cfg.Run.Push
		cfg.PushSet = true
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		var unknown []string
		for _, key := range undecoded {
			unknown = append(unknown, key.String())
		}
		sort.Strings(unknown)
		return cfg, fmt.Errorf("%s: unknown key(s) %v", path, unknown)
	}
	if cfg.Launch != "" && cfg.Launch != LaunchHere && cfg.Launch != LaunchTmux {
		return cfg, fmt.Errorf("%s: launch must be %s or %s, not %s", path, LaunchHere, LaunchTmux, cfg.Launch)
	}
	for name, agent := range cfg.Agents {
		if name != "claude" && name != "codex" && name != "opencode" {
			return cfg, fmt.Errorf("%s: unknown agent %q", path, name)
		}
		for key, choices := range map[string][]string{"models": agent.Models, "efforts": agent.Efforts} {
			if choices != nil && len(choices) == 0 {
				return cfg, fmt.Errorf("%s: agents.%s.%s must not be empty", path, name, key)
			}
			seen := map[string]bool{}
			for _, choice := range choices {
				if strings.TrimSpace(choice) != choice || choice == "" || seen[choice] {
					return cfg, fmt.Errorf("%s: invalid or repeated agents.%s.%s choice %q", path, name, key, choice)
				}
				seen[choice] = true
			}
		}
		for source, target := range agent.Fallbacks {
			if source == "" || target == "" || strings.TrimSpace(source) != source || strings.TrimSpace(target) != target {
				return cfg, fmt.Errorf("%s: agents.%s.fallbacks needs nonempty models", path, name)
			}
			// A single fallback keeps an exhausted API from starting a routing loop.
			if _, chained := agent.Fallbacks[target]; chained {
				return cfg, fmt.Errorf("%s: agents.%s.fallbacks must not chain or cycle", path, name)
			}
		}
	}
	if cfg.Plans == "" {
		cfg.Plans = Default().Plans
	}
	if cfg.CloseCommit == "" {
		cfg.CloseCommit = Default().CloseCommit
	}
	if cfg.StateCommit == "" {
		cfg.StateCommit = Default().StateCommit
	}
	return cfg, nil
}

// Validate checks the new tables against the harnesses this build can drive.
// It runs after Load so that a repository's own agents preferences are known.
func (c Config) Validate(harnesses harness.Set) error {
	if err := validateRun(c.Run, harnesses); err != nil {
		return err
	}
	return validateReview(c.Review, harnesses)
}

func validateRun(r Run, harnesses harness.Set) error {
	if r.Agent != "" {
		h := harnesses.Get(r.Agent)
		if h == nil {
			return fmt.Errorf("[run] agent must be one of %s, not %s",
				strings.Join(harnesses.Names(), ", "), r.Agent)
		}
		if r.Effort != "" && !contains(h.Efforts(), r.Effort) {
			return fmt.Errorf("[run] effort for %s must be one of %s, not %s",
				h.Name(), strings.Join(h.Efforts(), ", "), r.Effort)
		}
	}
	switch r.Peak {
	case "", "wait", "run":
	default:
		return fmt.Errorf("[run] peak must be wait or run, not %s", r.Peak)
	}
	switch r.Finish {
	case "", "none", "suspend", "poweroff":
	default:
		return fmt.Errorf("[run] finish must be none, suspend or poweroff, not %s", r.Finish)
	}
	if r.Subscriptions != "" {
		if _, err := NormalizeSubscriptions(harnesses, r.Subscriptions); err != nil {
			return fmt.Errorf("[run] subscriptions: %w", err)
		}
	}
	if r.Secondary != "" && r.Secondary != "none" {
		h := harnesses.Get(r.Secondary)
		if h == nil {
			return fmt.Errorf("[run] secondary must be one of %s or none, not %s",
				strings.Join(harnesses.Names(), ", "), r.Secondary)
		}
		if r.SecondaryEffort != "" && !contains(h.Efforts(), r.SecondaryEffort) {
			return fmt.Errorf("[run] secondary_effort for %s must be one of %s, not %s",
				h.Name(), strings.Join(h.Efforts(), ", "), r.SecondaryEffort)
		}
	}
	return nil
}

func validateReview(r Review, harnesses harness.Set) error {
	if r.TriageAgent != "" {
		h := harnesses.Get(r.TriageAgent)
		if h == nil {
			return fmt.Errorf("[review] triage_agent must be one of %s, not %s",
				strings.Join(harnesses.Names(), ", "), r.TriageAgent)
		}
		if r.TriageEffort != "" && !contains(h.Efforts(), r.TriageEffort) {
			return fmt.Errorf("[review] triage_effort for %s must be one of %s, not %s",
				h.Name(), strings.Join(h.Efforts(), ", "), r.TriageEffort)
		}
	}
	if r.TriageBudget != nil && *r.TriageBudget < 0 {
		return fmt.Errorf("[review] triage_budget must not be negative")
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// NormalizeSubscriptions normalizes a subscription set. Empty means all;
// names may be separated by commas or spaces. A named harness must have a
// subscription.
func NormalizeSubscriptions(harnesses harness.Set, value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", " "))
	switch value {
	case "", "all":
		return "all", nil
	case "none":
		return "none", nil
	}
	seen := map[string]bool{}
	var names []string
	for _, name := range strings.Fields(value) {
		if seen[name] {
			continue
		}
		h := harnesses.Get(name)
		if h == nil {
			return "", fmt.Errorf("%s is not a harness; use all, none, or %s",
				name, strings.Join(harnesses.Names(), ", "))
		}
		if _, metered := h.(harness.Metered); !metered {
			return "", fmt.Errorf("%s has no subscription to lend", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	return strings.Join(names, " "), nil
}

// Where a run goes: the terminal it was started in, or a tmux window of its
// own.
const (
	LaunchHere = "here"
	LaunchTmux = "tmux"
)

// RepoSkill is where a repository may keep its own plan-unit skill.
const RepoSkill = ".agents/skills/plan-unit/SKILL.md"

// Where a repository may keep its own review and landing skills and checklist.
const (
	RepoReviewSkill = ".agents/skills/plan-review/SKILL.md"
	RepoTriageSkill = ".agents/skills/review-triage/SKILL.md"
	RepoLandSkill   = ".agents/skills/land-repair/SKILL.md"
	RepoChecklist   = ".agents/review-checklist.md"
)

// SkillsDir is where `ploopy skills` writes ploopy's skills by default.
const SkillsDir = ".agents/skills"

// LocalSkill is the skill holding a repository's own rules for ploopy's
// skills, a section per skill.
const LocalSkill = "ploopy-local"

// LocalSkillPaths are where a repository may keep its LocalSkill.
var LocalSkillPaths = []string{
	SkillsDir + "/" + LocalSkill + "/SKILL.md",
	".claude/skills/" + LocalSkill + "/SKILL.md",
}

// ChecklistPath is the review checklist collecting sessions are given: the
// one the settings name, else the repository's own, else none.
func (c Config) ChecklistPath(root string) string {
	if c.Checklist != "" {
		return filepath.Join(root, c.Checklist)
	}
	if own := filepath.Join(root, RepoChecklist); isFile(own) {
		return own
	}
	return ""
}

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
