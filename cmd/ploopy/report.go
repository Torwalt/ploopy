package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

func newStatus(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "status [PLAN]",
		Short: "Progress of every plan, or of one plan's units",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return statusAll(e)
			}
			return statusOne(e, args[0])
		},
	}
}

func statusAll(e *env) error {
	plans, err := e.discover()
	if err != nil {
		return err
	}
	for _, p := range plans {
		s, err := e.state(p)
		if err != nil {
			return err
		}
		done, total := progressOf(p, s)
		tail := "complete"
		if next := state.Resume(p, s); next != nil {
			tail = "next " + next.ID + " " + next.Title
		}
		if totals := ui.Totals(s); totals != "" {
			tail += "  (" + totals + ")"
		}
		fmt.Printf("%-48s %2d/%-2d  %s\n", p.Path, done, total, tail)
	}
	return nil
}

func statusOne(e *env, name string) error {
	p, err := e.resolve(name)
	if err != nil {
		return err
	}
	s, err := e.state(p)
	if err != nil {
		return err
	}
	ui.NewReporter(os.Stdout, os.Stderr).Summary(p, s, nil)
	return nil
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
				errs := lintReport(e, p, os.Stdout, true)
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
func lintReport(e *env, p *plan.Plan, out io.Writer, showWarnings bool) []string {
	var errs, warnings []string
	settled := map[string]bool{}

	s, err := e.state(p)
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

	found, foundWarnings := plan.Lint(p, e.root, e.harnesses, settled)
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
