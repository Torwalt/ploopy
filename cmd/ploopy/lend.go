package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/history"
	"github.com/Torwalt/ploopy/internal/lease"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/ui"
)

type lendFlags struct {
	until   string
	nightly string
	open    bool
	keep    int
	model   string
	effort  string
}

func newLend(e *env) *cobra.Command {
	var f lendFlags
	cmd := &cobra.Command{
		Use:   "lend [HARNESS]",
		Short: "Lend a subscription to every run, or list what is lent",
		Long: "Lend one of the machine's subscriptions to every run on it, or list\n" +
			"what is lent now. A lease lasts until a clock time, every day inside a\n" +
			"span, or until reclaimed; --keep says how much of its week stays yours.\n\n" +
			"  ploopy lend\n" +
			"  ploopy lend codex --open [--keep 0]\n" +
			"  ploopy lend claude --until 09:00 [--keep 20] [--model sonnet] [--effort high]\n" +
			"  ploopy lend claude --nightly 21:00-09:00\n\n" +
			"--until takes the next such clock time from now. --keep defaults to the\n" +
			"lease's current keep, else 20; the model and effort default to its\n" +
			"current ones, else the harness's first model and preferred effort.\n" +
			"A harness without usage windows has no subscription to lend. It works\n" +
			"from any directory.",
		Args: cobra.MaximumNArgs(1),
		PersistentPreRunE: func(*cobra.Command, []string) error {
			e.harnesses = allHarnesses()
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error { return lend(cmd, e, &f, args) },
	}
	flags := cmd.Flags()
	flags.StringVar(&f.until, "until", "", "lend until the next such clock time, such as 09:00")
	flags.StringVar(&f.nightly, "nightly", "", "lend every day inside a span, such as 21:00-09:00")
	flags.BoolVar(&f.open, "open", false, "lend until reclaimed")
	flags.IntVar(&f.keep, "keep", 0, "percent of the week kept for you; the lease's own, else 20")
	flags.StringVar(&f.model, "model", "", "model the subscription runs as")
	flags.StringVar(&f.effort, "effort", "", "effort the subscription runs as")
	return cmd
}

func lend(cmd *cobra.Command, e *env, f *lendFlags, args []string) error {
	flags := cmd.Flags()
	if len(args) == 0 {
		if flags.Changed("until") || flags.Changed("nightly") || f.open ||
			flags.Changed("keep") || flags.Changed("model") || flags.Changed("effort") {
			return fmt.Errorf("name a harness to lend")
		}
		return listLeases(cmd, e, time.Now())
	}
	name := args[0]
	h := e.harnesses.Get(name)
	if h == nil {
		return fmt.Errorf("%s has no subscription to lend", name)
	}
	if _, metered := h.(harness.Metered); !metered {
		return fmt.Errorf("%s has no subscription to lend", name)
	}
	kinds := 0
	if flags.Changed("until") {
		kinds++
	}
	if flags.Changed("nightly") {
		kinds++
	}
	if f.open {
		kinds++
	}
	if kinds != 1 {
		return fmt.Errorf("say how to lend %s: --until, --nightly or --open", name)
	}

	file, err := lease.Load()
	if err != nil {
		return err
	}
	keep, model, effort := 20, "", ""
	for _, old := range file.Leases {
		if old.Harness == h.Name() {
			keep, model, effort = old.Keep, old.Model, old.Effort
		}
	}
	if flags.Changed("keep") {
		keep = f.keep
	}
	if keep < 0 || keep > 100 {
		return fmt.Errorf("--keep is a percentage, not %d", keep)
	}
	if f.model != "" {
		model = f.model
	}
	if f.effort != "" {
		effort = f.effort
	}
	if model == "" {
		model = h.Models()[0]
	}
	if effort == "" {
		effort = preferredEffort(h)
	}
	if !contains(h.Efforts(), effort) {
		return fmt.Errorf("--effort for %s must be one of %s", h.Name(), strings.Join(h.Efforts(), ", "))
	}

	now := time.Now()
	l := lease.Lease{Harness: h.Name(), Model: model, Effort: effort, Keep: keep}
	switch {
	case flags.Changed("until"):
		until, err := nextClock(f.until, now)
		if err != nil {
			return err
		}
		l.Kind, l.Until = lease.Until, until
	case flags.Changed("nightly"):
		span, err := loop.ParseAway(f.nightly)
		if err != nil {
			return err
		}
		l.Kind, l.Span = lease.Nightly, span.String()
	case f.open:
		l.Kind = lease.Open
	}
	if err := lease.Save(file.Set(l)); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "lent "+leaseLine(h, l, nil, now))
	return nil
}

func newReclaim(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reclaim <harness>|all",
		Short: "Take a lent subscription back",
		Long: "Take a subscription back from every run on the machine, or take them\n" +
			"all back. The next session goes to the bedrock, or to another lent\n" +
			"subscription with room. It works from any directory.",
		Args: cobra.ExactArgs(1),
		PersistentPreRunE: func(*cobra.Command, []string) error {
			e.harnesses = allHarnesses()
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error { return reclaim(cmd, args[0]) },
	}
	return cmd
}

func reclaim(cmd *cobra.Command, harnessName string) error {
	file, err := lease.Load()
	if err != nil {
		return err
	}
	if harnessName == "all" {
		file = lease.File{}
	} else {
		file = file.Reclaim(harnessName)
	}
	if err := lease.Save(file); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "reclaimed %s\n", harnessName)
	return nil
}

func listLeases(cmd *cobra.Command, e *env, now time.Time) error {
	file, err := lease.Load()
	if err != nil {
		return err
	}
	active := file.Active(now)
	if len(active) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "nothing is lent")
		return nil
	}
	records := localHistory(cmd.Context())
	for _, l := range active {
		fmt.Fprintln(cmd.OutOrStdout(), leaseLine(e.harnesses.Get(l.Harness), l, history.Latest(records, l.Harness), now))
	}
	return nil
}

// localHistory is the history of the checkout the command runs in, if any.
func localHistory(ctx context.Context) []history.Record {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	root, err := repo.Root(ctx, cwd)
	if err != nil {
		return nil
	}
	return loadHistory(ctx, root)
}

func nextClock(text string, now time.Time) (time.Time, error) {
	hours, mins, ok := strings.Cut(strings.TrimSpace(text), ":")
	h, errH := strconv.Atoi(hours)
	m, errM := strconv.Atoi(mins)
	if !ok || errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return time.Time{}, fmt.Errorf("--until must look like 09:00, not %q", text)
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at, nil
}

// lendingLoader turns the lease file into what the loop may use, reloading it
// each time the author may have changed it. A broken file is said once.
type lendingLoader struct {
	harnesses harness.Set
	primary   string
	report    *ui.Reporter
	mu        sync.Mutex
	said      bool
}

func newLendingLoader(harnesses harness.Set, primary string, report *ui.Reporter) *lendingLoader {
	return &lendingLoader{harnesses: harnesses, primary: primary, report: report}
}

func (l *lendingLoader) load(now time.Time) *loop.Lending {
	lending, err := lendingAt(l.harnesses, l.primary, now)
	if err != nil {
		l.say(err)
		return nil
	}
	return lending
}

func (l *lendingLoader) say(err error) {
	l.mu.Lock()
	first := !l.said
	l.said = true
	l.mu.Unlock()
	if first && l.report != nil {
		l.report.Say("the lease file cannot be read: %v; nothing is lent", err)
	}
}

func lendingAt(harnesses harness.Set, primary string, now time.Time) (*loop.Lending, error) {
	file, err := lease.Load()
	if err != nil {
		return nil, err
	}
	return lendingFromLeases(harnesses, primary, file.Active(now))
}

func lendingFromLeases(harnesses harness.Set, primary string, leases []lease.Lease) (*loop.Lending, error) {
	lending := &loop.Lending{}
	for _, l := range leases {
		if l.Harness == primary {
			continue
		}
		h := harnesses.Get(l.Harness)
		if h == nil {
			continue
		}
		if _, metered := h.(harness.Metered); !metered {
			continue
		}
		out := loop.Lease{
			Agent: loop.Agent{Harness: h, Model: l.Model, Effort: l.Effort},
			Keep:  float64(l.Keep) / 100,
		}
		if out.Model == "" {
			out.Model = h.Models()[0]
		}
		if out.Effort == "" {
			out.Effort = preferredEffort(h)
		}
		switch l.Kind {
		case lease.Until:
			out.Kind, out.Until = loop.LeaseUntil, l.Until
		case lease.Nightly:
			span, err := loop.ParseAway(l.Span)
			if err != nil {
				return nil, fmt.Errorf("%s: lease for %s has span %q: %w", lease.Path(), l.Harness, l.Span, err)
			}
			out.Kind, out.Span = loop.LeaseNightly, span
		case lease.Open:
			out.Kind = loop.LeaseOpen
		}
		lending.Leases = append(lending.Leases, out)
	}
	if len(lending.Leases) == 0 {
		return nil, nil
	}
	return lending, nil
}

func leasesNote(ctx context.Context, e *env, now time.Time) string {
	file, err := lease.Load()
	var lines []string
	if err != nil {
		lines = append(lines, "the lease file cannot be read: "+err.Error())
	} else {
		records := loadHistory(ctx, e.root)
		for _, l := range file.Active(now) {
			lines = append(lines, leaseLine(e.harnesses.Get(l.Harness), l, history.Latest(records, l.Harness), now))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "nothing is lent")
	}
	return strings.Join(lines, "\n") + "\n\nchange them with `ploopy lend`"
}

// leaseLine is one lease for a person: who runs, on what terms, and how much
// of the week it now keeps.
func leaseLine(h harness.Harness, l lease.Lease, b *harness.Budget, now time.Time) string {
	model, effort := l.Model, l.Effort
	if h != nil {
		if model == "" {
			model = h.Models()[0]
		}
		if effort == "" {
			effort = preferredEffort(h)
		}
	}
	label := strings.Join(strings.Fields(l.Harness+" "+model+" "+effort), " ")
	term := "until reclaimed"
	switch l.Kind {
	case lease.Until:
		term = "until " + leaseClock(l.Until, now)
	case lease.Nightly:
		term = "nightly " + pretty(l.Span)
	}
	return fmt.Sprintf("%s · %s · keep %s", label, term, keepNote(l.Keep, b, now))
}

// keepNote renders the reserve as it has shrunk, when it has shrunk by at
// least one point.
func keepNote(keep int, b *harness.Budget, now time.Time) string {
	effective := int(math.Round(loop.EffectiveKeep(loop.Lease{Keep: float64(keep) / 100}, b, now, false) * 100))
	if keep-effective >= 1 {
		return fmt.Sprintf("%d%% → %d%%", keep, effective)
	}
	return fmt.Sprintf("%d%%", keep)
}

func leaseClock(at, now time.Time) string {
	local := at.In(now.Location())
	if local.YearDay() == now.YearDay() && local.Year() == now.Year() {
		return local.Format("15:04")
	}
	return local.Format("Mon 15:04")
}

func pretty(span string) string { return strings.Replace(span, "-", "–", 1) }
