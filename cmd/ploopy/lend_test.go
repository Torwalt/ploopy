package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/history"
	"github.com/Torwalt/ploopy/internal/lease"
	"github.com/Torwalt/ploopy/internal/loop"
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

func TestLendUntilWritesTheNextClockInstant(t *testing.T) {
	e := lendEnv(t)
	before := time.Now()
	if _, err := runCommand(t, newLend(e), "claude", "--until", "09:00"); err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	file, err := lease.Load()
	if err != nil || len(file.Leases) != 1 {
		t.Fatalf("leases %+v, error %v", file.Leases, err)
	}
	l := file.Leases[0]
	if l.Kind != lease.Until {
		t.Fatalf("kind %q, want until", l.Kind)
	}
	wantBefore, _ := nextClock("09:00", before)
	wantAfter, _ := nextClock("09:00", after)
	if !l.Until.Equal(wantBefore) && !l.Until.Equal(wantAfter) {
		t.Fatalf("until %v, want %v", l.Until, wantBefore)
	}
}

func TestLendNightlyWritesTheSpan(t *testing.T) {
	e := lendEnv(t)
	if _, err := runCommand(t, newLend(e), "claude", "--nightly", "21:00-09:00"); err != nil {
		t.Fatal(err)
	}
	file, err := lease.Load()
	if err != nil || len(file.Leases) != 1 {
		t.Fatalf("leases %+v, error %v", file.Leases, err)
	}
	l := file.Leases[0]
	if l.Kind != lease.Nightly || l.Span != "21:00-09:00" {
		t.Fatalf("lease %+v, want nightly 21:00-09:00", l)
	}
}

func TestLendingMapsEveryLeaseKind(t *testing.T) {
	e := lendEnv(t)
	until := time.Now().Add(2 * time.Hour).Round(0)
	cases := []struct {
		name string
		in   lease.Lease
		want loop.LeaseKind
	}{
		{"until", lease.Lease{Harness: "claude", Kind: lease.Until, Until: until, Keep: 20}, loop.LeaseUntil},
		{"nightly", lease.Lease{Harness: "claude", Kind: lease.Nightly, Span: "21:00-09:00", Keep: 20}, loop.LeaseNightly},
		{"open", lease.Lease{Harness: "claude", Kind: lease.Open, Keep: 20}, loop.LeaseOpen},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lending, err := lendingFromLeases(e.harnesses, "opencode", []lease.Lease{c.in}, "", false)
			if err != nil {
				t.Fatal(err)
			}
			if lending == nil || len(lending.Leases) != 1 {
				t.Fatalf("lending %+v", lending)
			}
			got := lending.Leases[0]
			if got.Kind != c.want {
				t.Fatalf("kind %q, want %q", got.Kind, c.want)
			}
			switch c.want {
			case loop.LeaseUntil:
				if !got.Until.Equal(until) {
					t.Fatalf("until %v, want %v", got.Until, until)
				}
			case loop.LeaseNightly:
				if got.Span.String() != "21:00-09:00" {
					t.Fatalf("span %q", got.Span.String())
				}
			}
		})
	}
}

func TestLendDefaultsToTheCurrentLeasesValuesAndFlagsOverride(t *testing.T) {
	e := lendEnv(t)
	current := lease.Lease{
		Harness: "claude", Model: "opus", Effort: "medium",
		Kind: lease.Open, Keep: 35,
	}
	if err := lease.Save(lease.File{Leases: []lease.Lease{current}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(t, newLend(e), "claude", "--open"); err != nil {
		t.Fatal(err)
	}
	file, err := lease.Load()
	if err != nil || len(file.Leases) != 1 {
		t.Fatalf("leases %+v, error %v", file.Leases, err)
	}
	kept := file.Leases[0]
	if kept.Keep != current.Keep || kept.Model != current.Model || kept.Effort != current.Effort {
		t.Fatalf("lease %+v, want the current lease's values %+v", kept, current)
	}

	if _, err := runCommand(t, newLend(e), "claude", "--open",
		"--keep", "7", "--model", "haiku", "--effort", "xhigh"); err != nil {
		t.Fatal(err)
	}
	file, err = lease.Load()
	if err != nil || len(file.Leases) != 1 {
		t.Fatalf("leases %+v, error %v", file.Leases, err)
	}
	over := file.Leases[0]
	if over.Keep != 7 || over.Model != "haiku" || over.Effort != "xhigh" {
		t.Fatalf("lease %+v, want the flags' values", over)
	}
}

func TestTheSteeringLoaderSaysOnceAndLendsNothingOnABrokenFile(t *testing.T) {
	e := lendEnv(t)
	if err := os.MkdirAll(filepath.Dir(lease.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lease.Path(), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	loader := newLendingLoader(e.harnesses, "opencode", ui.NewReporter(&out, io.Discard))
	for i := 0; i < 2; i++ {
		if lending := loader.load(time.Now(), "", false); lending != nil {
			t.Fatalf("load %d lent %+v from a broken file", i, lending)
		}
	}
	if n := strings.Count(out.String(), "the lease file cannot be read"); n != 1 {
		t.Fatalf("reported %d times, want once:\n%s", n, out.String())
	}
}

func TestTheSteeringLoaderLeavesOutAPassedUntilLease(t *testing.T) {
	e := lendEnv(t)
	now := time.Now()
	if err := lease.Save(lease.File{Leases: []lease.Lease{{
		Harness: "claude", Kind: lease.Until, Until: now.Add(-time.Hour), Keep: 20,
	}}}); err != nil {
		t.Fatal(err)
	}
	loader := newLendingLoader(e.harnesses, "opencode", ui.NewReporter(io.Discard, io.Discard))
	if lending := loader.load(now, "", false); lending != nil {
		t.Fatalf("an expired lease was lent: %+v", lending)
	}
}

func TestLendingListRefusesAMalformedNightlySpan(t *testing.T) {
	e := lendEnv(t)
	if err := lease.Save(lease.File{Leases: []lease.Lease{{
		Harness: "claude", Kind: lease.Nightly, Span: "09:00", Keep: 20,
	}}}); err != nil {
		t.Fatal(err)
	}
	out, err := runCommand(t, newLend(e))
	if err == nil {
		t.Fatalf("a malformed nightly span was listed:\n%s", out)
	}
	if !strings.Contains(err.Error(), lease.Path()) {
		t.Fatalf("error %v does not name %s", err, lease.Path())
	}
}
