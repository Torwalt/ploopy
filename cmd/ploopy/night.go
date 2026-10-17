package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/history"
	"github.com/Torwalt/ploopy/internal/repo"
)

// historyPath is the repository's history, in its git directory, which its
// worktrees share and nothing commits.
func historyPath(ctx context.Context, root string) string {
	dir, err := repo.New(root, nil).CommonDir(ctx)
	if err != nil {
		return ""
	}
	return history.Path(dir)
}

func loadHistory(ctx context.Context, root string) []history.Record {
	path := historyPath(ctx, root)
	if path == "" {
		return nil
	}
	records, _ := history.Load(path)
	return records
}

// windowsNow is a subscription's usage windows: read live where the harness
// can tell, else the last a session reported. seen says which.
func windowsNow(ctx context.Context, h harness.Harness, records []history.Record) (*harness.Budget, string) {
	if metered, ok := h.(harness.Metered); ok {
		if budget, err := metered.Budget(ctx); err == nil && budget != nil {
			return budget, " · now"
		}
	}
	budget := history.Latest(records, h.Name())
	if budget == nil {
		return nil, ""
	}
	return budget, " · seen " + budget.At.Local().Format("Mon 15:04")
}

// printEstimates says what units took each agent in this repository before
// the author picks one.
func printEstimates(records []history.Record, open int) {
	var lines []string
	for _, est := range history.Estimates(records) {
		if !est.Known() {
			continue
		}
		line := fmt.Sprintf("  %-38s %s", est.Agent, est.Describe())
		if plan := est.Plan(open); plan != "" {
			line += "\n  " + strings.Repeat(" ", 38) + " " + plan
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return
	}
	fmt.Println("What a landed unit took here:")
	fmt.Println(strings.Join(lines, "\n"))
	fmt.Println()
}
