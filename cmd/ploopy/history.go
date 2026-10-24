package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Torwalt/ploopy/internal/config"
	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/harness/claude"
	"github.com/Torwalt/ploopy/internal/history"
	"github.com/Torwalt/ploopy/internal/plan"
	"github.com/Torwalt/ploopy/internal/repo"
	"github.com/Torwalt/ploopy/internal/stats"
)

func newHistory(e *env) *cobra.Command {
	var importLogs bool
	cmd := &cobra.Command{
		Use:   "history",
		Short: "What a landed unit took each agent here, and the subscriptions' usage windows",
		Long: "What a landed unit took each agent in this repository, from every session\n" +
			"ploopy ran here, and where Claude's and Codex's usage windows stand.\n\n" +
			"The history lives in the repository's git directory; --import first adds\n" +
			"the sessions the stats logs of this repository's checkouts still hold.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return showHistory(cmd.Context(), e, importLogs)
		},
	}
	cmd.Flags().BoolVar(&importLogs, "import", false, "first add the sessions the checkouts' stats logs hold")
	return cmd
}

func showHistory(ctx context.Context, e *env, importLogs bool) error {
	path := historyPath(ctx, e.root)
	if path == "" {
		return fmt.Errorf("no git directory for %s", e.root)
	}
	if importLogs {
		added, err := importHistory(ctx, e, path)
		if err != nil {
			return err
		}
		fmt.Printf("imported %d session(s) from the checkouts' stats logs\n\n", added)
	}
	records, err := history.Load(path)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Println("Nothing recorded yet. `ploopy history --import` adds the sessions this repository's checkouts still hold.")
	} else {
		fmt.Printf("%d session(s) in %s\n\n", len(records), path)
		for _, est := range history.Estimates(records) {
			fmt.Printf("%-40s %s\n", est.Agent, est.Describe())
		}
	}

	fmt.Println()
	now := time.Now()
	for _, h := range e.harnesses {
		if _, ok := h.(harness.Metered); !ok {
			continue
		}
		budget, seen := windowsNow(ctx, h, records)
		fmt.Printf("%-8s %s%s\n", h.Name(), budget.Describe(now), seen)
	}
	return nil
}

// importHistory adds the sessions every checkout's stats logs hold and the
// history lacks. Claude's transcripts still show its usage windows.
func importHistory(ctx context.Context, e *env, path string) (int, error) {
	existing, err := history.Load(path)
	if err != nil {
		return 0, err
	}
	// Sessions are told apart by their plan's directory, run and stem. A plan
	// closed since is named as the history knows it.
	known, names := map[string]bool{}, map[string]string{}
	for _, r := range existing {
		dir := filepath.Base(stats.Dir("", r.Plan))
		known[dir+"\x00"+r.Run+"\x00"+r.Stem] = true
		names[dir] = r.Plan
	}
	trees, err := e.repo().Worktrees(ctx)
	if err != nil {
		return 0, err
	}
	added := 0
	for _, tree := range trees {
		logs, _ := filepath.Glob(filepath.Join(tree.Path, repo.WorkDir, "*", "stats.jsonl"))
		for dir, name := range planNames(tree.Path) {
			names[dir] = name
		}
		for _, log := range logs {
			dir := filepath.Dir(log)
			records, err := stats.Load(dir)
			if err != nil {
				return added, err
			}
			base := filepath.Base(dir)
			name := base
			if original, ok := names[base]; ok {
				name = original
			}
			for _, rec := range records {
				key := base + "\x00" + rec.Run + "\x00" + rec.Stem
				if rec.Kind != "session" || known[key] {
					continue
				}
				known[key] = true
				r := history.Record{
					ReviewMode: rec.ReviewMode, ReviewBase: rec.ReviewBase, ReviewHead: rec.ReviewHead,
					Plan: name, Role: rec.Role, Branch: tree.Branch, Run: rec.Run, Unit: rec.Unit, Stem: rec.Stem,
					Agent: rec.Agent, Harness: strings.Fields(rec.Agent + " ?")[0],
					Started: rec.Started, Seconds: rec.Seconds, Verdict: rec.Verdict,
					Tokens: rec.Tokens, CostUSD: rec.CostUSD, After: rec.Budget, Degraded: rec.Degraded,
				}
				if r.Harness == "claude" {
					r.Before, r.After = transcriptBudgets(filepath.Join(dir, "logs", rec.Stem+".log"), rec)
				}
				if err := history.Append(path, r); err != nil {
					return added, err
				}
				added++
			}
		}
	}
	return added, nil
}

// planNames maps the directory each plan's files live under to the plan's name.
func planNames(root string) map[string]string {
	cfg, err := config.Load(root)
	if err != nil {
		return nil
	}
	plans, _ := plan.Discover(root, cfg.Plans)
	names := map[string]string{}
	for _, p := range plans {
		names[filepath.Base(stats.Dir(root, p.Name()))] = p.Name()
	}
	return names
}

// transcriptBudgets reads a Claude session's windows out of its transcript,
// dated by when the session started and ended.
func transcriptBudgets(path string, rec stats.Record) (*harness.Budget, *harness.Budget) {
	file, err := os.Open(path)
	if err != nil {
		return nil, rec.Budget
	}
	defer file.Close()
	first, last := claude.LogBudgets(file)
	if first != nil {
		first.At = rec.Started
	}
	if last != nil {
		last.At = rec.Started.Add(time.Duration(rec.Seconds * float64(time.Second)))
	}
	return first, last
}
