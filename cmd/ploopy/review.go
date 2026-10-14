package main

import (
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
)

type reviewFlags struct {
	run          runFlags
	since        string
	writeOnly    bool
	triageAgent  string
	triageModel  string
	triageEffort string
	triageBudget float64
}

func newReview(e *env) *cobra.Command {
	var f reviewFlags
	cmd := &cobra.Command{
		Use:   "review [PLAN]",
		Short: "Review a plan's landed work: findings, a triage, a fix plan",
		Long: "Write and run the review of a plan's landed work, as a plan of its own\n" +
			"beside it, <PLAN>_REVIEW.md. A fresh session per landed unit collects\n" +
			"findings on that unit's commits without changing anything, and one more\n" +
			"looks at the seams between the units. Then one triage session, usually\n" +
			"a stronger model, weighs every finding: it writes <PLAN>_FIXES.md, an\n" +
			"ordinary plan for every finding that holds, applies its recommended\n" +
			"design choices, and shows the author those choices and alternatives at\n" +
			"the end of the review plan.\n\n" +
			"The collecting sessions run with the agent the run settings choose. Who\n" +
			"weighs the findings is asked on a page of its own and remembered apart;\n" +
			"the --triage flags answer it. A review already written runs on from where\n" +
			"it stopped, and its triage can still be changed until it has run.",
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
	flags := cmd.Flags()
	flags.StringVar(&f.triageAgent, "triage-agent", "", "harness that runs the triage; claude when nothing else says")
	flags.StringVar(&f.triageModel, "triage-model", "", "model for the triage; opus when nothing else says")
	flags.StringVar(&f.triageEffort, "triage-effort", "", "effort for the triage")
	flags.Float64Var(&f.triageBudget, "triage-budget", 0, "spending cap for the triage session in dollars; 0 is none")
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
	if c.Root == "" {
		return fmt.Errorf("%s is only on branch %s; check it out to review it", c.Plan.Path, c.Branch)
	}

	root := c.Root
	reviewPath := review.Path(c.Plan.Path)
	if isFile(filepath.Join(root, reviewPath)) {
		existing, err := plan.Load(filepath.Join(root, reviewPath), reviewPath)
		if err != nil {
			return err
		}
		s, err := state.Load(filepath.Join(root, state.PathFor(reviewPath)))
		if err != nil {
			return err
		}
		if loop.Complete(existing, s) {
			return fmt.Errorf("%s is complete; read its *For the author*, then close it with "+
				"`ploopy close %s` before reviewing %s again", reviewPath, existing.Name(), c.Plan.Name())
		}
		fmt.Printf("%s is already written; running it on\n", reviewPath)
		if err := retriage(cmd, e, root, existing, s, f); err != nil {
			return err
		}
	} else if err := writeReview(cmd, e, root, c.Plan, c.State, f); err != nil {
		return err
	}

	if f.writeOnly {
		fmt.Printf("run it with `ploopy run --plan %s`\n", reviewPath)
		return nil
	}
	f.run.planName = reviewPath
	return runPlan(cmd, e, &f.run)
}

// writeReview writes the review plan beside the reviewed one and commits it,
// so that a run in a worktree of the branch finds it.
func writeReview(cmd *cobra.Command, e *env, root string, reviewed *plan.Plan, s *state.State, f *reviewFlags) error {
	ctx := cmd.Context()
	fixes := review.FixesPath(reviewed.Path)
	if isFile(filepath.Join(root, fixes)) {
		return fmt.Errorf("%s from an earlier review still exists; run or close it before reviewing again", fixes)
	}
	triage, err := chooseTriage(ctx, cmd, e, f, nil)
	if err != nil {
		return err
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
	if len(head) > 8 {
		head = head[:8]
	}
	text, err := review.Generate(reviewed, s, review.Options{
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
	fmt.Printf("wrote %s: %d sessions to collect findings, then the triage with %s\n",
		path, len(written.Units)-1, triage.Label())
	return nil
}

// retriage offers to change who weighs the findings of a review that is
// written but whose triage has not run, and commits the change.
func retriage(cmd *cobra.Command, e *env, root string, p *plan.Plan, s *state.State, f *reviewFlags) error {
	ctx := cmd.Context()
	u, current := review.TriageOf(p)
	if u == nil {
		return nil
	}
	if status := s.Status(u.ID); status == state.Landed || status == state.Skipped {
		return nil
	}
	if live, err := e.find(ctx, p.Path); err == nil && live.Live != nil {
		return nil
	}
	chosen, err := chooseTriage(ctx, cmd, e, f, &current)
	if err != nil || chosen == current {
		return err
	}

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
// the triage wrote for them, and the fix plan to run.
func reviewOutcome(root string, p *plan.Plan) {
	if text, err := os.ReadFile(filepath.Join(root, p.Path)); err == nil {
		if section := review.AuthorSection(string(text)); section != "" {
			fmt.Printf("\n%s\n", section)
		}
	}
	if fixes := review.FixesPath(p.Reviews()); isFile(filepath.Join(root, fixes)) {
		fmt.Printf("\nthe fix plan is %s: run it with `ploopy run --plan %s`\n", fixes, fixes)
	} else {
		fmt.Println("\nno fix plan: no finding held for the loop to fix")
	}
	fmt.Printf("close the review with `ploopy close %s` once you have read it\n", p.Name())
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
