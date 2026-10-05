package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/catalog"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/stats"
	"github.com/Torwalt/ploopy/internal/ui"
)

func newStatus(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "status [PLAN]",
		Short: "Progress of every plan, wherever it runs, or of one plan's units",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return statusAll(cmd.Context(), e)
			}
			return statusOne(cmd.Context(), e, args[0])
		},
	}
}

func statusAll(ctx context.Context, e *env) error {
	entries, err := e.plans(ctx)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Printf("no plan under %s on any branch\n", e.cfg.Plans)
	}
	for _, entry := range entries {
		fmt.Println(e.label(entry.Best))
	}
	return nil
}

func statusOne(ctx context.Context, e *env, name string) error {
	c, err := e.find(ctx, name)
	if err != nil {
		return err
	}
	showCopy(e, c)
	return nil
}

// showCopy prints a plan's units as the copy that has its progress holds them.
func showCopy(e *env, c catalog.Copy) {
	if !c.Here {
		fmt.Printf("%s is on %s\n", c.Plan.Path, c.Where(e.root))
	}
	if c.Live != nil {
		fmt.Printf("running unit %s · %s\n", c.Live.Unit, c.Live.Wanted.Describe())
	}
	ui.NewReporter(os.Stdout, os.Stderr).Summary(c.Plan, c.State, nil)
}

func newReport(e *env) *cobra.Command {
	var stamp bool
	cmd := &cobra.Command{
		Use:   "report [PLAN]",
		Short: "What a plan took: time, checks, slowest units and commands, cost",
		Long: "What a plan took, read from its state file and, while its checkout\n" +
			"keeps it, its stats log. The run that lands a plan's last unit stamps\n" +
			"this into that unit's state commit; find past ones with\n" +
			"`git log --grep '^" + stats.Heading + "'`.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := e.planArg(ctx, args, "Report on which plan?", false)
			if err != nil {
				return err
			}
			text := reportOf(c)
			fmt.Print(text)
			if !stamp {
				return nil
			}
			if c.Root == "" {
				return fmt.Errorf("%s is not checked out anywhere; check it out to stamp its report", c.Branch)
			}
			if err := repo.New(c.Root, nil).CommitEmpty(ctx, strings.TrimRight(text, "\n")); err != nil {
				return err
			}
			fmt.Printf("\nstamped into a commit on %s\n", c.Branch)
			return nil
		},
	}
	cmd.Flags().BoolVar(&stamp, "commit", false, "also stamp the report into an empty commit on the plan's branch")
	return cmd
}

// reportOf renders a plan's report, with the stats log its checkout keeps.
func reportOf(c catalog.Copy) string {
	var records []stats.Record
	if c.Root != "" {
		records, _ = stats.Load(stats.Dir(c.Root, c.Plan.Name()))
	}
	return stats.Report(c.Plan, c.State, records, nil)
}

func newShow(e *env) *cobra.Command {
	var whole bool
	cmd := &cobra.Command{
		Use:   "show PLAN UNIT",
		Short: "Print a unit's work order",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := e.resolve(args[0])
			if err != nil {
				return err
			}
			u := p.Unit(args[1])
			if u == nil {
				return fmt.Errorf("%s has no unit %s", p.Path, args[1])
			}
			if !whole {
				fmt.Print(p.WorkOrder(*u))
				return nil
			}
			return showPrompt(cmd, e, p, *u)
		},
	}
	cmd.Flags().BoolVar(&whole, "prompt", false, "print the whole session prompt instead")
	return cmd
}

func newLint(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "lint [PLAN...]",
		Short: "Check that plans parse the way the loop needs",
		RunE: func(_ *cobra.Command, args []string) error {
			var targets []*plan.Plan
			for _, name := range args {
				p, err := e.resolve(name)
				if err != nil {
					return err
				}
				targets = append(targets, p)
			}
			if len(targets) == 0 {
				found, err := e.discover()
				if err != nil {
					return err
				}
				targets = found
			}

			failed := false
			for _, p := range targets {
				errs := lintReport(e, e.root, p, os.Stdout, true)
				failed = failed || len(errs) > 0
				if len(errs) == 0 {
					fmt.Printf("%s: %d units, ok\n", p.Path, len(p.Units))
				}
			}
			if failed {
				return fmt.Errorf("lint found errors")
			}
			return nil
		},
	}
}

// lintReport prints what is wrong with a plan and returns its errors.
func lintReport(e *env, root string, p *plan.Plan, out io.Writer, showWarnings bool) []string {
	var errs, warnings []string
	settled := map[string]bool{}

	s, err := state.Load(filepath.Join(root, state.PathFor(p.Path)))
	if err != nil {
		errs = append(errs, err.Error())
	} else {
		for _, u := range p.Units {
			if status := s.Status(u.ID); status == state.Landed || status == state.Skipped {
				settled[u.ID] = true
			}
		}
		for _, id := range state.Orphans(p, s) {
			warnings = append(warnings,
				fmt.Sprintf("the state file records unit %s, which the plan no longer has", id))
		}
	}

	found, foundWarnings := plan.Lint(p, root, e.harnesses, settled)
	errs = append(found, errs...)
	warnings = append(foundWarnings, warnings...)

	for _, message := range errs {
		fmt.Fprintf(out, "%s: error: %s\n", p.Path, message)
	}
	if showWarnings {
		for _, message := range warnings {
			fmt.Fprintf(out, "%s: warning: %s\n", p.Path, message)
		}
	}
	return errs
}

func newMark(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "mark PLAN UNIT (landed|blocked|skipped|pending)",
		Short: "Record a unit's status by hand and commit it",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := e.resolve(args[0])
			if err != nil {
				return err
			}
			u := p.Unit(args[1])
			if u == nil {
				return fmt.Errorf("%s has no unit %s", p.Path, args[1])
			}
			status := args[2]
			switch status {
			case state.Landed, state.Blocked, state.Skipped, state.Pending:
			default:
				return fmt.Errorf("status must be landed, blocked, skipped or pending, not %s", status)
			}

			s, err := e.state(p)
			if err != nil {
				return err
			}
			if status == state.Pending {
				s.Clear(u.ID)
			} else {
				s.Set(u.ID, &state.Entry{
					Status:  status,
					Outcome: "marked",
					Date:    time.Now().Format(time.RFC3339),
				})
			}
			if err := s.Save(); err != nil {
				return err
			}
			path := plan.Relative(s.Path, e.root)
			if err := repo.New(e.root, e.cfg.AuthorPaths).CommitOnly(
				cmd.Context(), path, fmt.Sprintf(e.cfg.StateCommit, p.Name())); err != nil {
				return err
			}
			fmt.Printf("unit %s marked %s\n", u.ID, status)
			return nil
		},
	}
}

func newReplay(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "replay PLAN UNIT",
		Short: "What a unit's session did, and how to reopen it",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			p, err := e.resolve(args[0])
			if err != nil {
				return err
			}
			s, err := e.state(p)
			if err != nil {
				return err
			}
			entry := s.Entry(args[1])
			if entry == nil {
				return fmt.Errorf("%s has no record of unit %s", p.Path, args[1])
			}

			fmt.Printf("unit %s  %s  %s\n", args[1], entry.Status, entry.Date)
			if entry.Agent != "" {
				fmt.Printf("agent    %s\n", entry.Agent)
			}
			if entry.Attempts > 0 {
				fmt.Printf("attempts %d\n", entry.Attempts)
			}
			if entry.CostUSD > 0 {
				fmt.Printf("cost     $%.2f\n", entry.CostUSD)
			}
			for _, commit := range entry.Commits {
				fmt.Printf("commit   %s\n", commit)
			}
			for _, note := range entry.Notes {
				fmt.Printf("note     %s\n", note)
			}
			if entry.Reason != "" {
				fmt.Printf("reason   %s\n", entry.Reason)
			}
			if entry.SessionID != "" {
				switch entry.Harness {
				case "claude":
					fmt.Printf("\nreopen with:\n    claude --resume %s\n", entry.SessionID)
				case "opencode":
					fmt.Printf("\nreopen with:\n    opencode --session %s\n", entry.SessionID)
				}
			}
			if entry.Handover != "" {
				fmt.Printf("\n%s\n", strings.TrimSpace(entry.Handover))
			}
			return nil
		},
	}
}
