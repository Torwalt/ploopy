package main

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/lease"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
)

func subscriptionsEnv(t *testing.T) *env {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return &env{harnesses: allHarnesses(), cfg: config.Default()}
}

func saveOpenLeases(t *testing.T, names ...string) {
	t.Helper()
	var file lease.File
	for _, name := range names {
		file = file.Set(lease.Lease{Harness: name, Kind: lease.Open})
	}
	if err := lease.Save(file); err != nil {
		t.Fatal(err)
	}
}

func settingsCommand(t *testing.T, args ...string) (*cobra.Command, *runFlags) {
	t.Helper()
	f := &runFlags{}
	cmd := &cobra.Command{Use: "run"}
	addRunFlags(cmd, f)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd, f
}

func lendingFor(t *testing.T, e *env, frontMatter string, args ...string) (*loop.Lending, error) {
	t.Helper()
	p := plan.Parse("---\n"+frontMatter+"\n---\n", "docs/plans/PASS.md")
	cmd, f := settingsCommand(t, args...)
	s, _, err := givenSettings(cmd, e, p, f)
	if err != nil {
		return nil, err
	}
	chosen, err := resolveSettings(e, s)
	if err != nil {
		return nil, err
	}
	file, err := lease.Load()
	if err != nil {
		return nil, err
	}
	return lendingFromLeases(e.harnesses, chosen.agent.Harness.Name(),
		file.Active(time.Now()), chosen.subscriptions, chosen.spendAll)
}

func TestTheSubscriptionSetFiltersTheLentLeases(t *testing.T) {
	e := subscriptionsEnv(t)
	saveOpenLeases(t, "claude", "codex")
	bedrock := []string{"--agent", "opencode", "--effort", "high", "--model", "deepseek/deepseek-flash"}

	lending, err := lendingFor(t, e, "subscriptions: none", bedrock...)
	if err != nil || lending != nil {
		t.Fatalf("bedrock only got %+v, error %v", lending, err)
	}

	lending, err = lendingFor(t, e, "subscriptions: codex", bedrock...)
	if err != nil {
		t.Fatal(err)
	}
	if lending == nil || len(lending.Leases) != 1 || lending.Leases[0].Harness.Name() != "codex" {
		t.Fatalf("codex only got %+v", lending)
	}

	args := append([]string{"--subscriptions", "claude,codex"}, bedrock...)
	lending, err = lendingFor(t, e, "", args...)
	if err != nil {
		t.Fatal(err)
	}
	if lending == nil || len(lending.Leases) != 2 ||
		lending.Leases[0].Harness.Name() != "claude" || lending.Leases[1].Harness.Name() != "codex" {
		t.Fatalf("both subscriptions got %+v", lending)
	}
}

func TestTheSubscriptionSetRefusesANamedHarnessWithoutOne(t *testing.T) {
	e := subscriptionsEnv(t)
	saveOpenLeases(t, "claude", "codex")
	_, err := lendingFor(t, e, "subscriptions: opencode",
		"--agent", "opencode", "--effort", "high", "--model", "deepseek/deepseek-flash")
	if err == nil || !strings.Contains(err.Error(), "has no subscription to lend") {
		t.Fatalf("error %v", err)
	}
}

func TestSpendAllReachesTheLending(t *testing.T) {
	e := subscriptionsEnv(t)
	saveOpenLeases(t, "claude")
	lending, err := lendingFor(t, e, "", "--agent", "opencode", "--effort", "high",
		"--model", "deepseek/deepseek-flash", "--spend-all")
	if err != nil {
		t.Fatal(err)
	}
	if lending == nil || !lending.SpendAll {
		t.Fatalf("lending %+v", lending)
	}
}

func TestAPlansSubscriptionsAreGivenAndNotAsked(t *testing.T) {
	e := subscriptionsEnv(t)
	p := plan.Parse("---\nsubscriptions: none\n---\n", "docs/plans/PASS.md")
	cmd, f := settingsCommand(t)
	_, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	if !g.subscriptions {
		t.Fatal("the plan's subscriptions were not treated as given")
	}
}

func TestAnEmptySubscriptionsFlagMeansAll(t *testing.T) {
	e := subscriptionsEnv(t)
	p := plan.Parse("---\nsubscriptions: none\n---\n", "docs/plans/PASS.md")
	cmd, f := settingsCommand(t, "--subscriptions=")
	s, g, err := givenSettings(cmd, e, p, f)
	if err != nil {
		t.Fatal(err)
	}
	if s.Subscriptions != "all" {
		t.Fatalf("subscriptions %q, want all", s.Subscriptions)
	}
	if !g.subscriptions || g.why("subscriptions") != "from a flag" {
		t.Fatalf("given %+v, want from a flag", g)
	}
}
