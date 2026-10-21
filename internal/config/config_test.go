package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
