package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/history"
	"github.com/Torwalt/ploopy/internal/lease"
	"github.com/Torwalt/ploopy/internal/ui"
)

func lendEnv(t *testing.T) *env {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	return &env{harnesses: allHarnesses()}
}

func runCommand(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestLendThenList(t *testing.T) {
	e := lendEnv(t)
	if _, err := runCommand(t, newLend(e), "codex", "--open"); err != nil {
		t.Fatal(err)
	}
	file, err := lease.Load()
	if err != nil || len(file.Leases) != 1 {
		t.Fatalf("leases %+v, error %v", file.Leases, err)
	}
	l := file.Leases[0]
	if l.Harness != "codex" || l.Kind != lease.Open || l.Model != "gpt-6.1-sol" || l.Effort != "high" || l.Keep != 20 {
		t.Fatalf("lease %+v", l)
	}

	out, err := runCommand(t, newLend(e))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "codex gpt-6.1-sol high") || !strings.Contains(out, "until reclaimed") {
		t.Fatalf("list:\n%s", out)
	}
}

func TestLendDefaultsFromAnActiveLeaseOnly(t *testing.T) {
	e := lendEnv(t)
	expired := lease.Lease{
		Harness: "claude", Model: "haiku", Effort: "high",
		Kind: lease.Until, Until: time.Now().Add(-time.Hour), Keep: 0,
	}
	if err := lease.Save(lease.File{Leases: []lease.Lease{expired}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(t, newLend(e), "claude", "--open"); err != nil {
		t.Fatal(err)
	}
	file, err := lease.Load()
	if err != nil || len(file.Leases) != 1 {
		t.Fatalf("leases %+v, error %v", file.Leases, err)
	}
	l := file.Leases[0]
	first := e.harnesses.Get("claude").Models()[0]
	if l.Keep != 20 || l.Model != first {
		t.Fatalf("lease %+v, want keep 20 and model %q", l, first)
	}
}

func TestLendConfirmationShowsTheShrunkKeep(t *testing.T) {
	e := lendEnv(t)
	_, tree := worktreeRepo(t, nil)
	t.Chdir(tree)
	now := time.Now()
	path := historyPath(context.Background(), tree)
	if err := history.Append(path, history.Record{Harness: "claude", Before: &harness.Budget{
		At:     now,
		Weekly: harness.Allowance{Used: 0.9, ResetAt: now.Add(24 * time.Hour)},
	}}); err != nil {
		t.Fatal(err)
	}
	out, err := runCommand(t, newLend(e), "claude", "--open")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "keep 20% → ") {
		t.Fatalf("confirmation should show the shrunk keep:\n%s", out)
	}
}

func TestUntilTakesTheNextSuchClockTime(t *testing.T) {
	zone := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, zone)
	got, err := nextClock("09:00", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 18, 9, 0, 0, 0, zone); !got.Equal(want) {
		t.Fatalf("next 09:00 from %v is %v, want %v", now, got, want)
	}
	got, _ = nextClock("18:30", now)
	if want := time.Date(2026, 10, 17, 18, 30, 0, 0, zone); !got.Equal(want) {
		t.Fatalf("next 18:30 from %v is %v, want %v", now, got, want)
	}
	if _, err := nextClock("9", now); err == nil {
		t.Fatal("a bare hour was accepted")
	}
}

func TestAHarnessWithoutASubscriptionCannotBeLent(t *testing.T) {
	e := lendEnv(t)
	if _, err := runCommand(t, newLend(e), "opencode", "--open"); err == nil ||
		!strings.Contains(err.Error(), "has no subscription to lend") {
		t.Fatalf("error %v", err)
	}
	if file, _ := lease.Load(); len(file.Leases) != 0 {
		t.Fatalf("leases %+v", file.Leases)
	}
}

func TestABadNightlySpanIsRefused(t *testing.T) {
	e := lendEnv(t)
	if _, err := runCommand(t, newLend(e), "claude", "--nightly", "09:00"); err == nil {
		t.Fatal("a span without an end was accepted")
	}
	if file, _ := lease.Load(); len(file.Leases) != 0 {
		t.Fatalf("leases %+v", file.Leases)
	}
}

func TestReclaimOneLeavesTheOther(t *testing.T) {
	e := lendEnv(t)
	for _, name := range []string{"claude", "codex"} {
		if _, err := runCommand(t, newLend(e), name, "--open"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runCommand(t, newReclaim(e), "claude"); err != nil {
		t.Fatal(err)
	}
	file, err := lease.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Leases) != 1 || file.Leases[0].Harness != "codex" {
		t.Fatalf("leases %+v", file.Leases)
	}
}

func TestReclaimRefusesANameThatIsNotASubscription(t *testing.T) {
	e := lendEnv(t)
	if _, err := runCommand(t, newLend(e), "claude", "--open"); err != nil {
		t.Fatal(err)
	}
	before, err := lease.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(t, newReclaim(e), "claud"); err == nil ||
		!strings.Contains(err.Error(), "claud has no subscription to lend") {
		t.Fatalf("error %v", err)
	}
	after, err := lease.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Leases) != 1 || after.Leases[0] != before.Leases[0] {
		t.Fatalf("leases changed from %+v to %+v", before.Leases, after.Leases)
	}
}

func TestReclaimAll(t *testing.T) {
	e := lendEnv(t)
	for _, name := range []string{"claude", "codex"} {
		if _, err := runCommand(t, newLend(e), name, "--open"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runCommand(t, newReclaim(e), "all"); err != nil {
		t.Fatal(err)
	}
	if file, _ := lease.Load(); len(file.Leases) != 0 {
		t.Fatalf("leases %+v", file.Leases)
	}
}

func TestSteeringReadsLeasesAsTheyChangeAndLeavesTheBedrockOut(t *testing.T) {
	e := lendEnv(t)
	if err := lease.Save(lease.File{Leases: []lease.Lease{{Harness: "claude", Kind: lease.Open}}}); err != nil {
		t.Fatal(err)
	}
	report := ui.NewReporter(io.Discard, io.Discard)
	s := &steering{harnesses: e.harnesses, leases: newLendingLoader(e.harnesses, "opencode", report)}

	lending := s.Steer().Lending
	if lending == nil || len(lending.Leases) != 1 || lending.Leases[0].Harness.Name() != "claude" {
		t.Fatalf("lending %+v", lending)
	}

	file, err := lease.Load()
	if err != nil {
		t.Fatal(err)
	}
	file = file.Set(lease.Lease{Harness: "codex", Kind: lease.Open})
	file = file.Set(lease.Lease{Harness: "opencode", Kind: lease.Open})
	if err := lease.Save(file); err != nil {
		t.Fatal(err)
	}
	lending = s.Steer().Lending
	names := map[string]bool{}
	for _, l := range lending.Leases {
		names[l.Harness.Name()] = true
	}
	if len(lending.Leases) != 2 || !names["claude"] || !names["codex"] || names["opencode"] {
		t.Fatalf("lending %+v", lending)
	}
}
