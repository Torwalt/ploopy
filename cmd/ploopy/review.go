package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

// triageFlags is who weighs the findings, on `review` and on a run that
// follows up with one.
type triageFlags struct {
	reviewMode   string
	triageAgent  string
	triageModel  string
	triageEffort string
	triageBudget float64
}

type reviewFlags struct {
	run       runFlags
	since     string
	writeOnly bool
	triageFlags
}

func newReview(e *env) *cobra.Command {
	var f reviewFlags
	cmd := &cobra.Command{
		Use:   "review [PLAN]",
		Short: "Review a plan's work: required fixes and optional follow-ups",
		Long: "Review a plan's landed work, as <PLAN>_REVIEW.md beside it.\n\n" +
			"Collected mode reviews each landed unit and its seams, then uses a frontier\n" +
			"triage. Direct mode uses one frontier session to review the complete branch\n" +
			"against the plan. Both write <PLAN>_FIXES.md only for required fixes, and\n" +
			"collect optional improvements in one issue for the review pass.\n\n" +
			"The board chooses the mode, frontier reviewer and implementation settings.\n" +
			"A written review keeps its mode and resumes where it stopped. --follow-up\n" +
			"executes the fix plan when there is one, using the implementation agent.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return reviewPlan(cmd, e, args, &f) },
	}
	addRunFlags(cmd, &f.run)
	_ = cmd.Flags().MarkHidden("plan")
	_ = cmd.Flags().MarkHidden("all")

	flags := cmd.Flags()
	flags.StringVar(&f.since, "since", "", "review only the landed units from this id on")
	flags.BoolVar(&f.writeOnly, "write-only", false, "write and commit the review without running it")
	addTriageFlags(cmd, &f)
	return cmd
}

// addTriageFlags is who weighs the findings, as flags.
func addTriageFlags(cmd *cobra.Command, f *reviewFlags) {
	addTriageFlagsTo(cmd, &f.triageFlags)
}

// addTriageFlagsTo adds the triage flags to a command, for review and for a
// run that may follow up with one.
func addTriageFlagsTo(cmd *cobra.Command, f *triageFlags) {
	flags := cmd.Flags()
	flags.StringVar(&f.reviewMode, "review-mode", "", "review approach: collected (default) or direct")
	flags.StringVar(&f.triageAgent, "triage-agent", "", "harness for frontier review or triage; claude by default")
	flags.StringVar(&f.triageModel, "triage-model", "", "model for frontier review or triage; opus by default")
	flags.StringVar(&f.triageEffort, "triage-effort", "", "effort for frontier review or triage")
	flags.Float64Var(&f.triageBudget, "triage-budget", 0, "spending cap for frontier review or triage in dollars; 0 is none")
}

func reviewPlan(cmd *cobra.Command, e *env, args []string, f *reviewFlags) error {
	ctx := cmd.Context()
	c, err := e.planArg(ctx, args, "Review which plan's work?", false)
	if err != nil {
		return err
	}
	if c.Plan.Reviews() != "" {
		return fmt.Errorf("%s is a review itself; review the fix plan it led to instead", c.Plan.Path)
	}
	if c.Live != nil {
		return fmt.Errorf("%s is running in %s; review it once the run has ended", c.Plan.Path, c.Live.Root)
	}
	root, err := e.place(ctx, c)
	if err != nil {
		return err
	}
	fresh := false
	reviewPath := review.Path(c.Plan.Path)
	var p *plan.Plan
	var s *state.State
	var current *review.Triage
	var triage review.Triage
	var givens map[string]bool
	var sources map[string]string
	var triageRan bool
	if isFile(filepath.Join(root, reviewPath)) {
		existing, err := plan.Load(filepath.Join(root, reviewPath), reviewPath)
		if err != nil {
			return err
		}
		s, err = state.Load(filepath.Join(root, state.PathFor(reviewPath)))
		if err != nil {
			return err
		}
		if loop.Complete(existing, s) {
			return fmt.Errorf("%s is complete; read its *For the author*, then close it with "+
				"`ploopy close %s` before reviewing %s again", reviewPath, existing.Name(), c.Plan.Name())
		}
		fmt.Printf("%s is already written; running it on\n", reviewPath)
		p = existing
		if f.reviewMode != "" && f.reviewMode != review.ModeOf(p) {
			return fmt.Errorf("%s already uses %s review; a written review keeps its mode", p.Path, review.ModeOf(p))
		}
		triage, givens, sources, triageRan, err = reviewTriage(ctx, cmd, e, f, p, s)
		if err != nil {
			return err
		}
		if u, cur := review.TriageOf(p); u != nil {
			current = &cur
			if triageRan {
				current = &triage
			}
		}
		if !triageRan && current != nil && (!ui.Interactive() || f.writeOnly) {
			changed, err := changeTriage(ctx, e, root, p, s, *current, triage)
			if err != nil {
				return err
			}
			if changed {
				current = &triage
			}
		}
	} else {
		fresh = true
		triage, givens, sources, triageRan, err = reviewTriage(ctx, cmd, e, f, nil, nil)
		if err != nil {
			return err
		}
		if err := writeReview(cmd, e, root, c.Plan, c.State, f, triage); err != nil {
			return err
		}
		p, err = plan.Load(filepath.Join(root, reviewPath), reviewPath)
		if err != nil {
			return err
		}
		s, err = state.Load(filepath.Join(root, state.PathFor(reviewPath)))
		if err != nil {
			return err
		}
		current = &triage
	}

	if f.writeOnly {
		fmt.Printf("run it with `ploopy run --plan %s`\n", reviewPath)
		return nil
	}
	f.run.planName = reviewPath
	if !ui.Interactive() {
		return runPlan(cmd, e, &f.run, nil)
	}

	c, err = choosePlan(ctx, e, &f.run)
	if err != nil {
		return err
	}
	t, err := prepareTarget(ctx, e, &f.run, c)
	if err != nil {
		return err
	}
	if errs := lintReport(e, t.root, c.Plan, os.Stderr, false); len(errs) > 0 {
		return fmt.Errorf("%s does not lint; fix it before running it", c.Plan.Path)
	}
	settings, g, err := givenSettings(cmd, e, c.Plan, &f.run)
	if err != nil {
		return err
	}
	if e.repo().CanPush(ctx, t.branch) != nil {
		g.push = true
	}
	fill(e, &settings, g, loadLast(ctx, e.root))
	locked := triageLock(ctx, e, p, s)
	board := newReviewBoard(ctx, e, c.Plan, g, &settings, t.branch, openUnits(c.Plan, c.State), triage, givens, sources, locked)
	if fresh {
		board.reviewModeLock = ""
		if f.reviewMode != "" {
			board.reviewModeLock = "from a flag"
		}
		board.rebuild()
	}
	chosen, err := board.start(func() error {
		_, err := board.board.Run()
		return err
	})
	if err != nil {
		return err
	}
	final, err := board.triageValue()
	if err != nil {
		return err
	}
	if chosen.reviewMode != review.ModeOf(p) {
		if err := rewriteReviewMode(ctx, e, root, p, chosen.reviewMode, f.since, final); err != nil {
			return err
		}
	} else if !triageRan && current != nil {
		if _, err := changeTriage(ctx, e, root, p, s, *current, final); err != nil {
			return err
		}
	}
	return runPlan(cmd, e, &f.run, &chosen)
}

// reviewTriage resolves the triage a review's board shows. A triage that has
// run is shown as it is and locked; otherwise flags, the review's own triage,
// the last one and the default decide, and the board may change it.
func reviewTriage(ctx context.Context, cmd *cobra.Command, e *env, f *reviewFlags, p *plan.Plan, s *state.State) (review.Triage, map[string]bool, map[string]string, bool, error) {
	var current *review.Triage
	if p != nil {
		if u, cur := review.TriageOf(p); u != nil {
			current = &cur
			status := s.Status(u.ID)
			if status == state.Landed || status == state.Skipped {
				return cur, nil, nil, true, nil
			}
		}
	}
	t, sources, err := chooseTriageWithSources(ctx, cmd, e, f, current)
	return t, triageGivens(cmd), sources, false, err
}

// writeReview writes the review plan beside the reviewed one and commits it,
// so that a run in a worktree of the branch finds it.
func writeReview(cmd *cobra.Command, e *env, root string, reviewed *plan.Plan, s *state.State, f *reviewFlags, triage review.Triage) error {
	ctx := cmd.Context()
	fixes := review.FixesPath(reviewed.Path)
	if isFile(filepath.Join(root, fixes)) {
		return fmt.Errorf("%s from an earlier review still exists; run or close it before reviewing again", fixes)
	}

	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	r := repo.New(root, cfg.AuthorPaths)
	head, err := r.Head(ctx)
	if err != nil {
		return err
	}
	mode := f.reviewMode
	if mode == "" {
		mode = cfg.Review.Mode
	}
	if mode == "" {
		mode = review.Collected
	}
	text, err := review.Generate(reviewed, s, review.Options{
		Mode:   mode,
		Date:   time.Now().Format("2006-01-02"),
		Head:   head,
		Since:  f.since,
		Triage: triage,
	})
	if err != nil {
		return err
	}

	path := review.Path(reviewed.Path)
	if errs := lintReport(e, root, plan.Parse(text, path), os.Stderr, false); len(errs) > 0 {
		return fmt.Errorf("the review ploopy wrote for %s does not lint; this is a bug in ploopy", reviewed.Path)
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0o644); err != nil {
		return err
	}
	if err := r.CommitOnly(ctx, path, "plans: review "+reviewed.Name()); err != nil {
		return err
	}
	written := plan.Parse(text, path)
	if review.ModeOf(written) == review.Direct {
		fmt.Printf("wrote %s: one direct review with %s\n", path, triage.Label())
	} else {
		fmt.Printf("wrote %s: %d sessions to collect findings, then the triage with %s\n", path, len(written.Units)-1, triage.Label())
	}
	return nil
}

// triageLock says why a written review's triage cannot change; empty means it
// may. A triage that has run is final, and a review being run keeps its own.
func triageLock(ctx context.Context, e *env, p *plan.Plan, s *state.State) string {
	if u, _ := review.TriageOf(p); u != nil {
		if status := s.Status(u.ID); status == state.Landed || status == state.Skipped {
			return triageRanLock
		}
	}
	if live, err := e.find(ctx, p.Path); err == nil && live.Live != nil {
		return triageLiveLock
	}
	return ""
}

// changeTriage writes a written review's triage when it may change, and
// reports whether it did.
func changeTriage(ctx context.Context, e *env, root string, p *plan.Plan, s *state.State, current, chosen review.Triage) (bool, error) {
	if chosen == current || triageLock(ctx, e, p, s) != "" {
		return false, nil
	}
	return true, writeTriage(ctx, e, root, p, chosen)
}

// writeTriage rewrites a written review's triage and commits it.
func writeTriage(ctx context.Context, e *env, root string, p *plan.Plan, chosen review.Triage) error {
	full := filepath.Join(root, p.Path)
	text, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	changed, err := review.SetTriage(string(text), chosen)
	if err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(changed), 0o644); err != nil {
		return err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	if err := repo.New(root, cfg.AuthorPaths).CommitOnly(ctx, p.Path, "plans: triage "+p.Name()+" with "+chosen.Agent); err != nil {
		return err
	}
	fmt.Printf("the triage of %s now runs with %s\n", p.Path, chosen.Label())
	return nil
}

// reviewOutcome shows what a finished review left for the author: the section
// the triage wrote for them, and the fix plan to run. runsNext says the chain
// takes the fix plan now, so there is nothing for the author to start.
func reviewOutcome(root string, p *plan.Plan, runsNext bool) {
	if text, err := os.ReadFile(filepath.Join(root, p.Path)); err == nil {
		if section := review.AuthorSection(string(text)); section != "" {
			fmt.Printf("\n%s\n", section)
		}
	}
	if fixes := review.FixesPath(p.Reviews()); isFile(filepath.Join(root, fixes)) {
		if runsNext {
			fmt.Printf("\nthe fix plan is %s: running it next\n", fixes)
		} else {
			fmt.Printf("\nthe fix plan is %s: run it with `ploopy run --plan %s`\n", fixes, fixes)
		}
	} else {
		fmt.Println("\nno fix plan: no required fixes")
	}
	fmt.Printf("close the review with `ploopy close %s` once you have read it\n", p.Name())
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func rewriteReviewMode(ctx context.Context, e *env, root string, p *plan.Plan, mode, since string, t review.Triage) error {
	rp, err := plan.Load(filepath.Join(root, p.Reviews()), p.Reviews())
	if err != nil {
		return err
	}
	rs, err := state.Load(filepath.Join(root, state.PathFor(rp.Path)))
	if err != nil {
		return err
	}
	text, err := review.Generate(rp, rs, review.Options{Mode: mode, Since: since, Base: p.Settings["review-base"], Head: p.Settings["review-head"], Date: time.Now().Format("2006-01-02"), Triage: t})
	if err != nil {
		return err
	}
	if errs := lintReport(e, root, plan.Parse(text, p.Path), os.Stderr, false); len(errs) > 0 {
		return fmt.Errorf("the new review does not lint")
	}
	if err := os.WriteFile(filepath.Join(root, p.Path), []byte(text), 0o644); err != nil {
		return err
	}
	return repo.New(root, nil).CommitOnly(ctx, p.Path, "plans: change review mode")
}
