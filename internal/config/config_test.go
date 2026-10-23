package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/claude"
	"github.com/Torwalt/ploopy/internal/harness/codex"
	"github.com/Torwalt/ploopy/internal/harness/opencode"
)

func TestDefaultsWithoutAFile(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plans != "docs/plans" {
		t.Fatalf("plans %q", cfg.Plans)
	}
	if cfg.Verify != "" || cfg.Test != "" {
		t.Fatal("without a file, nothing is checked after a unit")
	}
	if len(cfg.Context) != 1 || cfg.Context[0] != "AGENTS.md" {
		t.Fatalf("context %v", cfg.Context)
	}
}

func TestAFileOverridesTheDefaults(t *testing.T) {
	root := t.TempDir()
	write(t, root, File, "plans = \"tasks\"\nverify = \"make lint\"\nauthor_paths = [\"NOTES.md\"]\npush = true\n")

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plans != "tasks" || cfg.Verify != "make lint" || !cfg.Push {
		t.Fatalf("config %+v", cfg)
	}
	if len(cfg.AuthorPaths) != 1 || cfg.AuthorPaths[0] != "NOTES.md" {
		t.Fatalf("author paths %v", cfg.AuthorPaths)
	}
}

func TestAnUnknownKeyIsRefused(t *testing.T) {
	root := t.TempDir()
	write(t, root, File, "nonsense = true\n")

	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "nonsense") {
		t.Fatalf("error %v", err)
	}
}

func TestBrokenTomlIsRefused(t *testing.T) {
	root := t.TempDir()
	write(t, root, File, "plans = \n")

	if _, err := Load(root); err == nil {
		t.Fatal("broken TOML should not load")
	}
}

func TestAgentChoicesAndRoutesLoad(t *testing.T) {
	root := t.TempDir()
	write(t, root, File, `[agents.codex]
models = ["gpt-6.1-sol", "gpt-6-luna"]
efforts = ["high", "xhigh"]
[agents.opencode.fallbacks]
"opencode-go/deepseek-v4.1-flash" = "deepseek/deepseek-flash"
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents["codex"].Models) != 2 || cfg.Agents["codex"].Efforts[1] != "xhigh" ||
		cfg.Agents["opencode"].Fallbacks["opencode-go/deepseek-v4.1-flash"] != "deepseek/deepseek-flash" {
		t.Fatalf("agents %+v", cfg.Agents)
	}
}

func TestInvalidAgentPreferencesAreRefused(t *testing.T) {
	for _, raw := range []string{
		"[agents.typo]\nmodels = ['x']",
		"[agents.codex]\nmodels = []",
		"[agents.codex]\nefforts = []",
		"[agents.codex]\nmodels = ['']",
		"[agents.codex]\nmodels = ['x', 'x']",
		"[agents.codex]\nmodels = [' x']",
		"[agents.opencode.fallbacks]\na = 'a'",
		"[agents.opencode.fallbacks]\na = 'b'\nb = 'a'",
		"[agents.opencode.fallbacks]\na = ''",
		"[agents.opencode]\nmodel = ['typo']",
	} {
		t.Run(raw, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, File, raw)
			if _, err := Load(root); err == nil {
				t.Fatal("invalid preferences loaded")
			}
		})
	}
}

func TestAnEmptyFallbackTableDisablesTheDefault(t *testing.T) {
	root := t.TempDir()
	write(t, root, File, "[agents.opencode]\nfallbacks = {}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents["opencode"].Fallbacks == nil {
		t.Fatal("empty table lost its override")
	}
}

func TestARepositorySkillWinsOverTheBundledOne(t *testing.T) {
	root := t.TempDir()
	cfg := Default()
	if got := cfg.SkillPath(root, "/bundled/SKILL.md"); got != "/bundled/SKILL.md" {
		t.Fatalf("skill %q", got)
	}

	write(t, root, RepoSkill, "# the repository's own\n")
	if got := cfg.SkillPath(root, "/bundled/SKILL.md"); got != filepath.Join(root, RepoSkill) {
		t.Fatalf("skill %q", got)
	}

	cfg.Skill = "custom/SKILL.md"
	if got := cfg.SkillPath(root, "/bundled/SKILL.md"); got != filepath.Join(root, "custom/SKILL.md") {
		t.Fatalf("skill %q", got)
	}
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

func fileHarnesses() harness.Set {
	return harness.Set{claude.New(), opencode.New(), codex.New()}
}

func TestTheRunAndReviewTablesLoad(t *testing.T) {
	root := t.TempDir()
	write(t, root, File, `push = true

[run]
agent = "opencode"
model = "deepseek/deepseek-flash"
effort = "max"
secondary = "claude"
secondary_model = "sonnet"
secondary_effort = "high"
peak = "run"
subscriptions = "claude,codex"
spend_all = true
push = false
finish = "suspend"

[review]
triage_agent = "claude"
triage_model = "opus"
triage_effort = "high"
triage_budget = 2.5
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	run := cfg.Run
	if run.Agent != "opencode" || run.Model != "deepseek/deepseek-flash" || run.Effort != "max" ||
		run.Secondary != "claude" || run.SecondaryModel != "sonnet" || run.SecondaryEffort != "high" ||
		run.Peak != "run" || run.Subscriptions != "claude,codex" || run.Finish != "suspend" {
		t.Fatalf("run %+v", run)
	}
	if run.SpendAll == nil || !*run.SpendAll {
		t.Fatalf("spend_all %v", run.SpendAll)
	}
	if run.Push == nil || *run.Push {
		t.Fatalf("run push %v", run.Push)
	}
	if !cfg.PushSet || cfg.Push {
		t.Fatalf("push %v set %v; [run] push must win over the top-level key", cfg.Push, cfg.PushSet)
	}
	review := cfg.Review
	if review.TriageAgent != "claude" || review.TriageModel != "opus" || review.TriageEffort != "high" {
		t.Fatalf("review %+v", review)
	}
	if review.TriageBudget == nil || *review.TriageBudget != 2.5 {
		t.Fatalf("triage budget %v", review.TriageBudget)
	}
	if err := cfg.Validate(fileHarnesses()); err != nil {
		t.Fatal(err)
	}
}

func TestTheOldTopLevelPushStillLoads(t *testing.T) {
	root := t.TempDir()
	write(t, root, File, "push = true\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Push || !cfg.PushSet {
		t.Fatalf("push %v set %v", cfg.Push, cfg.PushSet)
	}
}

func TestTheNewTablesAreValidated(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"an unknown run agent", "[run]\nagent = 'nope'\n", "[run] agent"},
		{"an effort the run agent does not offer", "[run]\nagent = 'opencode'\neffort = 'medium'\n", "[run] effort"},
		{"an unknown secondary", "[run]\nsecondary = 'nope'\n", "[run] secondary"},
		{"an effort the secondary does not offer", "[run]\nsecondary = 'claude'\nsecondary_effort = 'max'\n", "[run] secondary_effort"},
		{"a bad peak", "[run]\npeak = 'later'\n", "[run] peak"},
		{"a bad finish", "[run]\nfinish = 'sleep'\n", "[run] finish"},
		{"a bad subscription set", "[run]\nsubscriptions = 'nope'\n", "[run] subscriptions"},
		{"an unknown triage agent", "[review]\ntriage_agent = 'nope'\n", "[review] triage_agent"},
		{"an effort the triage agent does not offer", "[review]\ntriage_agent = 'claude'\ntriage_effort = 'max'\n", "[review] triage_effort"},
		{"a negative triage budget", "[review]\ntriage_budget = -1\n", "[review] triage_budget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, File, tc.raw)
			cfg, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			err = cfg.Validate(fileHarnesses())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want it to name %s", err, tc.want)
			}
		})
	}
}
