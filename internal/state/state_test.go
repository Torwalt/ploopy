package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Torwalt/ploopy/internal/plan"
)

func samplePlan() *plan.Plan {
	return plan.Parse("## Stage 1 — S\n\n### 1.1 One\n\n**Goal:** x\n\n### 1.2 Two\n\n"+
		"**Goal:** x\n\n### 1.3 Three\n\n**Goal:** x\n", "docs/plans/PASS.md")
}

func TestStateSitsBesideThePlan(t *testing.T) {
	if got := PathFor("docs/plans/PASS.md"); got != filepath.Join("docs/plans", "PASS.state.json") {
		t.Fatalf("state path %q", got)
	}
}

func TestAMissingFileIsEmptyProgress(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "none.state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Units) != 0 {
		t.Fatalf("units %v", s.Units)
	}
	if s.Status("1.1") != Pending {
		t.Fatalf("status %q", s.Status("1.1"))
	}
}

func TestResumeSkipsLandedAndSkippedUnits(t *testing.T) {
	p := samplePlan()
	s, _ := Load(filepath.Join(t.TempDir(), "s.json"))
	s.Set("1.1", &Entry{Status: Landed})
	s.Set("1.2", &Entry{Status: Skipped})

	next := Resume(p, s)
	if next == nil || next.ID != "1.3" {
		t.Fatalf("resume %v", next)
	}

	s.Set("1.2", &Entry{Status: Blocked})
	if next := Resume(p, s); next == nil || next.ID != "1.2" {
		t.Fatalf("a blocked unit is open again, got %v", next)
	}
}

func TestNextOpenLooksPastTheUnitJustDone(t *testing.T) {
	p := samplePlan()
	s, _ := Load(filepath.Join(t.TempDir(), "s.json"))
	s.Set("1.2", &Entry{Status: Skipped})

	next := NextOpen(p, s, p.Units[0])
	if next == nil || next.ID != "1.3" {
		t.Fatalf("next open %v", next)
	}
}

func TestStateRoundTripsAndRejectsABadStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "PASS.state.json")
	s, _ := Load(path)
	s.Set("1.1", &Entry{Status: Landed, Outcome: "done", Commits: []string{"abc subject"}, Attempts: 1})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Status("1.1") != Landed || again.Entry("1.1").Commits[0] != "abc subject" {
		t.Fatalf("round trip lost the entry: %+v", again.Entry("1.1"))
	}

	if err := os.WriteFile(path, []byte(`{"units":{"1.1":{"status":"nonsense"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("a bad status should not load")
	}
}

func TestAFileWithoutUnitsDoesNotLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("a file with no units object should not load")
	}
}

func TestACommittedFileLoadsEveryField(t *testing.T) {
	s, err := Load("testdata/sample.state.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Units) != 3 {
		t.Fatalf("got %d units, want the whole file", len(s.Units))
	}
	entry := s.Entry("1.1")
	if entry == nil || entry.Status != Landed || entry.Outcome != "done" {
		t.Fatalf("entry 1.1 is %+v", entry)
	}
	if len(entry.Notes) != 1 || entry.Handover == "" || entry.Base == "" ||
		entry.SessionID == "" || entry.CostUSD == 0 || entry.Harness != "claude" {
		t.Fatalf("entry 1.1 lost fields: %+v", entry)
	}
	if s.Status("1.2") != Blocked || s.Entry("1.2").Reason == "" || s.Status("1.3") != Skipped {
		t.Fatalf("units 1.2 and 1.3 are %+v and %+v", s.Entry("1.2"), s.Entry("1.3"))
	}
}

// The format is frozen: an object of units, each with a known status.
func TestASavedFileKeepsTheFrozenShape(t *testing.T) {
	loaded, err := Load("testdata/sample.state.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "PASS.state.json")
	loaded.Path = path
	if err := loaded.Save(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var shape struct {
		Units map[string]map[string]any `json:"units"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	if len(shape.Units) != len(loaded.Units) {
		t.Fatalf("saved %d units, loaded %d", len(shape.Units), len(loaded.Units))
	}
	for id, entry := range shape.Units {
		status, _ := entry["status"].(string)
		if status != Landed && status != Blocked && status != Skipped {
			t.Fatalf("unit %s saved status %q", id, status)
		}
	}
}

func TestOrphansAreUnitsThePlanNoLongerHas(t *testing.T) {
	p := samplePlan()
	s, _ := Load(filepath.Join(t.TempDir(), "s.json"))
	s.Set("1.1", &Entry{Status: Landed})
	s.Set("9.9", &Entry{Status: Landed})

	orphans := Orphans(p, s)
	if len(orphans) != 1 || orphans[0] != "9.9" {
		t.Fatalf("orphans %v", orphans)
	}
}

func TestClearMakesAUnitPendingAgain(t *testing.T) {
	s, _ := Load(filepath.Join(t.TempDir(), "s.json"))
	s.Set("1.1", &Entry{Status: Landed})
	s.Clear("1.1")
	if s.Status("1.1") != Pending {
		t.Fatalf("status %q", s.Status("1.1"))
	}
}
