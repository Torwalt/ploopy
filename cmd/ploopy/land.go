package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	ploopy "github.com/Torwalt/ploopy"
	"github.com/Torwalt/ploopy/internal/catalog"
	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/control"
	"github.com/Torwalt/ploopy/internal/guard"
	"github.com/Torwalt/ploopy/internal/land"
	"github.com/Torwalt/ploopy/internal/loop"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/review"
	"github.com/Torwalt/ploopy/internal/state"
	"github.com/Torwalt/ploopy/internal/ui"
)

type landFlags struct {
	agent, model, effort string
	budget               float64
	attempts             int
	noAgent              bool
	noPush               bool
	keepBranch           bool
}

func newLand(e *env) *cobra.Command {
	var f landFlags
	cmd := &cobra.Command{
		Use:   "land [BRANCH]",
		Short: "Land a finished branch: close its plans, rebase, check, fast-forward",
		Long: "Put a branch whose plans are all finished on the default branch.\n\n" +
			"Every plan the branch holds is closed, each in a commit carrying its\n" +
			"report. The branch is rebased onto the default branch, and the verify\n" +
			"and test commands run on the result. A conflict, or a check that fails\n" +
			"on the rebased branch, goes to a session with the land-repair skill;\n" +
			"git and the checks decide whether it repaired it. Once the checks pass,\n" +
			"the default branch is fast-forwarded to the branch. It is never pushed.\n\n" +
			"After landing, a branch that was pushed before is pushed again, with a\n" +
			"lease; ploopy's worktree for it is removed and the branch deleted.\n\n" +
			"When a repair fails, the default branch is left as it was: the branch\n" +
			"is back where it was after a conflict, and rebased after a failing check.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return landBranch(cmd, e, args, &f) },
	}
	flags := cmd.Flags()
	flags.StringVar(&f.agent, "agent", "", "harness that repairs a conflict or a failing check")
	flags.StringVar(&f.model, "model", "", "model for the repair")
	flags.StringVar(&f.effort, "effort", "", "effort for the repair")
	flags.Float64Var(&f.budget, "budget", 0, "spending cap per repair session in dollars; 0 is none")
	flags.IntVar(&f.attempts, "attempts", 3, "sessions per repair before giving up")
	flags.BoolVar(&f.noAgent, "no-agent", false, "stop at a conflict or a failing check instead of repairing it")
	flags.BoolVar(&f.noPush, "no-push", false, "do not push the rebased branch")
	flags.BoolVar(&f.keepBranch, "keep-branch", false, "keep the branch after landing it")
	return cmd
}

func landBranch(cmd *cobra.Command, e *env, args []string, f *landFlags) error {
	ctx := cmd.Context()
	r := e.repo()
	onto, err := r.DefaultBranch(ctx)
	if err != nil {
		return err
	}
	trees, err := r.Worktrees(ctx)
	if err != nil {
		return err
	}
	if len(trees) == 0 {
		return errors.New("git lists no checkout of this repository")
	}
	main := trees[0].Path

	branch, err := landTarget(ctx, e, args, onto, trees)
	if err != nil {
		return err
	}
	if branch == onto {
		return fmt.Errorf("%s is the default branch; land another branch onto it", onto)
	}
	if _, err := r.Resolve(ctx, "refs/heads/"+branch); err != nil {
		return fmt.Errorf("no branch %s", branch)
	}

	root, own := "", worktreePath(main, branch)
	for _, tree := range trees {
		if tree.Branch == branch {
			root = tree.Path
		}
	}
	fresh := root == ""
	if fresh {
		if own, err = e.addWorktree(ctx, main, trees, branch); err != nil {
			return err
		}
		root = own
		fmt.Printf("checked %s out at %s\n", branch, root)
	}
	if lives, _ := control.List(); len(lives) > 0 {
		for _, live := range lives {
			if live.Root == root {
				return fmt.Errorf("%s is running in %s; land the branch once the run has ended", live.Plan, root)
			}
		}
	}

	var agent *land.Agent
	if !f.noAgent {
		if agent, err = chooseLander(ctx, cmd, e, f); err != nil {
			return err
		}
	}

	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	branchRepo := repo.New(root, cfg.AuthorPaths)
	if branchRepo.Rebasing(ctx) {
		return fmt.Errorf("a rebase is already going on in %s; finish or abort it first", root)
	}
	if err := closeAll(ctx, root, branch, onto, cfg); err != nil {
		return err
	}
	if dirt, err := branchRepo.DirtLines(ctx); err != nil {
		return err
	} else if len(dirt) > 0 {
		return fmt.Errorf("%s has changes a rebase would trip over; commit or stash them first:\n%s",
			root, strings.Join(dirt, "\n"))
	}
	if fresh && cfg.Setup != "" {
		fmt.Printf("readying the checkout: `%s`\n", cfg.Setup)
		setup := exec.CommandContext(ctx, "sh", "-c", cfg.Setup)
		setup.Dir, setup.Stdout, setup.Stderr = root, os.Stdout, os.Stderr
		if err := setup.Run(); err != nil {
			return fmt.Errorf("`%s` failed: %w", cfg.Setup, err)
		}
	}

	logs, err := landLogs(ctx, root, branch)
	if err != nil {
		return err
	}

	report := ui.NewReporter(os.Stdout, os.Stderr)
	result, err := land.Run(ctx, land.Options{
		Root:        root,
		Branch:      branch,
		Onto:        onto,
		AuthorPaths: cfg.AuthorPaths,
		Verify:      cfg.Verify,
		Test:        cfg.Test,
		Agent:       agent,
		Skill:       ownOr(root, config.RepoLandSkill, ploopy.LandRepairSkill),
		Context:     cfg.Context,
		Deny:        guard.DeniedTools,
		Extra:       e.extra,
		Attempts:    f.attempts,
		Logs:        logs,
		Report:      report,
	})
	spent := ""
	if result.Sessions > 0 {
		spent = fmt.Sprintf(" · %d repair session(s), $%.2f", result.Sessions, result.CostUSD)
	}
	if err != nil {
		report.Fail("%s did not land: %v%s", branch, err, spent)
		fmt.Printf("%s is untouched; the branch is at %s, and was at %s before. Logs: %s\n",
			onto, root, short(result.Before), logs)
		return errors.New("not landed")
	}
	tip, _ := r.Resolve(ctx, "refs/heads/"+onto)
	report.Say("landed %s: %s is now at %s%s; push it when you are ready", branch, onto, short(tip), spent)

	if !f.noPush && branchRepo.Upstream(ctx, branch) != "" {
		pushCtx, cancel := context.WithTimeout(ctx, pushTimeout)
		where, err := branchRepo.PushRewritten(pushCtx, branch)
		cancel()
		if err != nil {
			report.Fail("pushing the branch failed: %v", err)
		} else {
			report.Say("pushed %s", where)
		}
	}
	tidyLanded(ctx, main, root, own, branch, onto, f.keepBranch, report)
	return nil
}

// landTarget is the branch named, else the one checked out here, else one the
// author picks from the branches checked out somewhere.
func landTarget(ctx context.Context, e *env, args []string, onto string, trees []repo.Worktree) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	if here, err := e.repo().Branch(ctx); err == nil && here != "" && here != onto {
		return here, nil
	}
	var choices []ui.Choice
	for _, tree := range trees {
		if tree.Branch != "" && tree.Branch != onto {
			choices = append(choices, ui.Choice{Label: tree.Branch + "  " + tree.Path, Value: tree.Branch})
		}
	}
	if len(choices) == 0 || !ui.Interactive() {
		return "", errors.New("name the branch to land")
	}
	return ui.Pick("Land which branch on "+onto+"?", "branch", choices)
}

// closeAll closes every plan of the branch, each in its own commit. A plan
// with units still open stops the landing before anything is closed.
func closeAll(ctx context.Context, root, branch, onto string, cfg config.Config) error {
	plans, err := plan.Discover(root, cfg.Plans)
	if err != nil {
		return err
	}
	r := repo.New(root, cfg.AuthorPaths)
	var copies []catalog.Copy
	var open []string
	for _, p := range plans {
		if inherited(ctx, r, p.Path, onto) {
			continue
		}
		s, err := state.Load(filepath.Join(root, state.PathFor(p.Path)))
		if err != nil {
			return err
		}
		if !loop.Complete(p, s) {
			open = append(open, p.Path)
			continue
		}
		copies = append(copies, catalog.Copy{Plan: p, State: s, Branch: branch, Root: root})
	}
	if len(open) > 0 {
		return fmt.Errorf("%s still has open units in %s; finish it, or `ploopy close --force` it, first",
			branch, strings.Join(open, ", "))
	}
	for _, c := range copies {
		if _, err := closeCommit(ctx, c, cfg); err != nil {
			return err
		}
		fmt.Printf("closed %s\n", c.Plan.Path)
	}
	return nil
}

// inherited reports whether a plan came with the branch from onto rather than
// being the branch's: the branch never touched it or its state since, so onto
// decides it, and the rebase carries what onto did to it.
func inherited(ctx context.Context, r *repo.Repo, path, onto string) bool {
	last := r.LastTouch(ctx, "HEAD", path, state.PathFor(path))
	return last != "" && r.IsAncestor(ctx, last, onto)
}

// tidyLanded removes what the landed branch needed: ploopy's worktree for it,
// or the main checkout's hold on it, then the branch. A worktree the author
// made stays theirs, and with it the branch.
func tidyLanded(ctx context.Context, main, root, own, branch, onto string, keep bool, report *ui.Reporter) {
	r := repo.New(main, nil)
	switch root {
	case own:
		if err := r.RemoveWorktree(ctx, root, worktreesDir(main)); err != nil {
			report.Fail("the worktree stays at %s: %v", root, err)
			return
		}
		report.Say("removed the worktree at %s", root)
	case main:
		if err := r.Switch(ctx, onto); err != nil {
			report.Fail("could not switch %s to %s: %v", main, onto, err)
			return
		}
		report.Say("switched %s to %s", main, onto)
	default:
		report.Say("%s is your worktree; it stays, and so does %s", root, branch)
		return
	}
	if keep {
		return
	}
	if err := r.DeleteBranch(ctx, branch); err != nil {
		report.Fail("%s stays: %v", branch, err)
		return
	}
	report.Say("deleted the branch %s", branch)
}

// landLogs keeps a landing's logs in the git directory every worktree shares,
// so they outlive the worktree the landing removes.
func landLogs(ctx context.Context, root, branch string) (string, error) {
	dir, err := repo.New(root, nil).CommonDir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ploopy", "land", strings.ReplaceAll(branch, "/", "-")), nil
}

// defaultLander repairs a landing when nothing else says: a repair is
// judgement, so the strongest model.
var defaultLander = defaultTriage

// chooseLander settles who repairs a landing. Flags win; then the last
// landing's choice, else the last triage's, else the default; and whatever no
// flag gave is asked on one page.
func chooseLander(ctx context.Context, cmd *cobra.Command, e *env, f *landFlags) (*land.Agent, error) {
	t := defaultLander
	path := lastLandPath(ctx, e.root)
	if last := loadAgentChoice(path); last != nil && e.harnesses.Get(last.Agent) != nil {
		t = *last
	} else if last := loadLastTriage(ctx, e.root); last != nil && e.harnesses.Get(last.Agent) != nil {
		t = *last
	}

	flags := cmd.Flags()
	given := map[string]bool{}
	for _, name := range []string{"agent", "model", "effort", "budget"} {
		given[name] = flags.Changed(name)
	}
	if given["agent"] && f.agent != t.Agent {
		t = review.Triage{Agent: f.agent, Budget: t.Budget}
		if h := e.harnesses.Get(f.agent); h != nil {
			t.Model, t.Effort = h.Models()[0], preferredEffort(h)
		}
	}
	if given["model"] {
		t.Model = f.model
	}
	if given["effort"] {
		t.Effort = f.effort
	}
	if given["budget"] {
		t.Budget = f.budget
	}

	all := given["agent"] && given["model"] && given["effort"] && given["budget"]
	if ui.Interactive() && !all {
		picked, err := ui.Pick("Who repairs a conflict or a failing check, if there is one?", "agent", []ui.Choice{
			{Label: "like last time: " + t.Label(), Value: "keep"},
			{Label: "change who repairs", Value: "change"},
		})
		if err != nil {
			return nil, err
		}
		if picked == "change" {
			if err := askAgent(e, "Who repairs a landing?", "Spending cap per repair session", &t, given); err != nil {
				return nil, err
			}
		}
	}

	h := e.harnesses.Get(t.Agent)
	if h == nil {
		return nil, fmt.Errorf("the agent must be one of %s, not %s", strings.Join(e.harnesses.Names(), ", "), t.Agent)
	}
	if t.Effort != "" && !contains(h.Efforts(), t.Effort) {
		return nil, fmt.Errorf("the effort for %s must be one of %s", h.Name(), strings.Join(h.Efforts(), ", "))
	}
	if t.Budget < 0 {
		return nil, errors.New("the budget must not be negative")
	}
	saveAgentChoice(path, t)
	return &land.Agent{Harness: h, Model: t.Model, Effort: t.Effort, Budget: t.Budget}, nil
}

func lastLandPath(ctx context.Context, root string) string {
	if run := lastRunPath(ctx, root); run != "" {
		return filepath.Join(filepath.Dir(run), "last-land.json")
	}
	return ""
}

func loadAgentChoice(path string) *review.Triage {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var t review.Triage
	if json.Unmarshal(raw, &t) != nil || t.Agent == "" {
		return nil
	}
	return &t
}

func saveAgentChoice(path string, t review.Triage) {
	if path == "" {
		return
	}
	body, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		_ = os.WriteFile(path, append(body, '\n'), 0o644)
	}
}

func short(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}
