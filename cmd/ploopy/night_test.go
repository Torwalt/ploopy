package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Torwalt/ploopy/internal/history"
	"github.com/Torwalt/ploopy/internal/stats"
)

func TestTheHistoryImportsWhatTheCheckoutsHold(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	dir := stats.Dir(tree, "PASS")
	started := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	for _, r := range []stats.Record{
		{Kind: "session", Run: "r1", Unit: "1.1", Stem: "1.1-0", Agent: "claude sonnet high", Started: started,
			Seconds: 600, CostUSD: 2, Verdict: "landed"},
		{Kind: "check", Run: "r1", Unit: "1.1", Started: started, Seconds: 7, Reason: "verify"},
		{Kind: "session", Run: "r1", Unit: "1.2", Stem: "1.2-0", Agent: "opencode deepseek/deepseek-flash high",
			Started: started, Seconds: 300, CostUSD: 0.05, Verdict: "landed"},
	} {
		if err := stats.Append(dir, r); err != nil {
			t.Fatal(err)
		}
	}
	event := func(used float64) string {
		line, _ := json.Marshal(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{
			"status": "allowed", "unifiedWindows": map[string]any{
				"five_hour": map[string]any{"utilization": used, "resetsAt": 1792066200},
				"seven_day": map[string]any{"utilization": 0.4, "resetsAt": 1792504800},
			}}})
		return string(line)
	}
	write(t, dir, "logs/1.1-0.log", event(0.49)+"\n"+`{"type":"assistant"}`+"\n"+event(0.59)+"\n")

	path := historyPath(context.Background(), e.root)
	for range 2 {
		if _, err := importHistory(context.Background(), e, path); err != nil {
			t.Fatal(err)
		}
	}
	records, err := history.Load(path)
	if err != nil || len(records) != 2 {
		t.Fatalf("records %+v, error %v", records, err)
	}
	first := records[0]
	if first.Plan != "PASS" || first.Branch != "pass" || first.Harness != "claude" ||
		first.Before == nil || first.Before.Session.Used != 0.49 || first.After.Session.Used != 0.59 ||
		!first.After.At.Equal(started.Add(10*time.Minute)) {
		t.Fatalf("record %+v", first)
	}
	if records[1].Harness != "opencode" || records[1].Before != nil {
		t.Fatalf("record %+v", records[1])
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "history.jsonl")); err != nil {
		t.Fatal(err)
	}
}

// A plan closed since keeps the name the history knows it by, and two plans
// whose runs started in the same second keep their own sessions.
func TestTheImportKeepsPlansApart(t *testing.T) {
	e, tree := worktreeRepo(t, nil)
	path := historyPath(context.Background(), e.root)
	if err := history.Append(path, history.Record{Plan: "GONE", Run: "r0", Unit: "1.1", Stem: "1.1-0", Agent: "claude sonnet high", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"PASS", "GONE"} {
		if err := stats.Append(stats.Dir(tree, name), stats.Record{Kind: "session", Run: "r1", Unit: "1.1", Stem: "1.1-0",
			Agent: "opencode deepseek/deepseek-flash high", Seconds: 60, Verdict: "landed"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := importHistory(context.Background(), e, path); err != nil {
		t.Fatal(err)
	}
	records, _ := history.Load(path)
	plans := map[string]bool{}
	for _, r := range records[1:] {
		plans[r.Plan] = true
	}
	if len(records) != 3 || !plans["PASS"] || !plans["GONE"] {
		t.Fatalf("records %+v", records)
	}
}
