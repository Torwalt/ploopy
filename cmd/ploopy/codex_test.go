package main

import (
	"context"
	"testing"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/plan"
)

func TestCodexCanRunAndTakeOver(t *testing.T) {
	e := &env{harnesses: allHarnesses(), cfg: config.Default()}
	chosen, err := unattended(e, runSettings{Harness: "codex", Effort: "xhigh"})
	if err != nil {
		t.Fatal(err)
	}
	if chosen.agent.Harness.Name() != "codex" || chosen.agent.Model != "gpt-6.1-sol" || chosen.agent.Effort != "xhigh" {
		t.Fatalf("agent %+v", chosen.agent)
	}
	fallback, err := resolveAgent(e.harnesses, "--secondary", "codex", "", "", "high")
	if err != nil {
		t.Fatal(err)
	}
	if fallback.Harness.Name() != "codex" || fallback.Effort != "high" {
		t.Fatalf("fallback %+v", fallback)
	}
	p := plan.Parse("---\nagent: codex\neffort: high\n---\n\n# Pass\n\n## Stage 1 — Work\n\n### 1.1 One\n\nAgent: codex\nEffort: max\n\n**Goal:** work.\n\n**Done when:** green.\n", "docs/plans/PASS.md")
	if errs, _ := plan.Lint(p, t.TempDir(), e.harnesses, nil); len(errs) != 0 {
		t.Fatalf("lint %v", errs)
	}
}

func TestCodexTriageIsRemembered(t *testing.T) {
	e, _ := worktreeRepo(t, nil)
	cmd, f := triageCommand(t, "--triage-agent", "codex", "--triage-effort", "max")
	got, err := chooseTriage(context.Background(), cmd, e, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent != "codex" || got.Model != "gpt-6.1-sol" || got.Effort != "max" || got.Budget != 0 {
		t.Fatalf("triage %+v", got)
	}
	cmd, f = triageCommand(t)
	remembered, err := chooseTriage(context.Background(), cmd, e, f, nil)
	if err != nil || remembered != got {
		t.Fatalf("triage %+v, error %v", remembered, err)
	}
}
