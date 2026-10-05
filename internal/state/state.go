// Package state is the loop's record of a plan's progress, committed beside
// the plan.
//
// `<PLAN>.state.json` is written only by the loop. A unit absent from it is
// pending. The format is frozen: files already committed beside plans must
// keep loading.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/plan"
)

// Statuses a unit can be recorded with. Pending is the absence of a record.
const (
	Landed  = "landed"
	Blocked = "blocked"
	Skipped = "skipped"
	Pending = "pending"
)

func known(status string) bool {
	return status == Landed || status == Blocked || status == Skipped
}

// Entry is one unit's record. The first eight fields are the frozen format;
// the rest are additive and omitted when empty.
type Entry struct {
	Status   string   `json:"status"`
	Outcome  string   `json:"outcome"`
	Date     string   `json:"date"`
	Agent    string   `json:"agent,omitempty"`
	Base     string   `json:"base,omitempty"`
	Commits  []string `json:"commits,omitempty"`
	Attempts int      `json:"attempts,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Notes    []string `json:"notes,omitempty"`
	Handover string   `json:"handover,omitempty"`

	SessionID string  `json:"session_id,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"` // every session of the unit, failed ones too
	Harness   string  `json:"harness,omitempty"`

	// What the unit took, retries and checks included.
	ElapsedS int             `json:"elapsed_s,omitempty"`
	SessionS int             `json:"session_s,omitempty"`
	VerifyS  int             `json:"verify_s,omitempty"`
	TestS    int             `json:"test_s,omitempty"`
	Tokens   *harness.Tokens `json:"tokens,omitempty"`
}

type file struct {
	Units map[string]*Entry `json:"units"`
}

// State is a plan's progress.
type State struct {
	Path  string
	Units map[string]*Entry
	order []string
}

// PathFor names the state file beside a plan.
func PathFor(planPath string) string {
	stem := strings.TrimSuffix(filepath.Base(planPath), filepath.Ext(planPath))
	return filepath.Join(filepath.Dir(planPath), stem+".state.json")
}

// Load reads a state file. A missing file is empty progress, not an error.
func Load(path string) (*State, error) {
	s := &State{Path: path, Units: map[string]*Entry{}}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}

	var parsed file
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	if parsed.Units == nil {
		return nil, fmt.Errorf("%s has no `units` object", path)
	}
	for id, entry := range parsed.Units {
		if entry == nil || !known(entry.Status) {
			return nil, fmt.Errorf("%s: unit %s has no valid status", path, id)
		}
	}
	s.Units = parsed.Units
	s.order = keyOrder(raw)
	return s, nil
}

// keyOrder recovers the order units appear in the file, so rewriting it does
// not reshuffle what a reader has already seen.
func keyOrder(raw []byte) []string {
	var probe struct {
		Units json.RawMessage `json:"units"`
	}
	if json.Unmarshal(raw, &probe) != nil || len(probe.Units) == 0 {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(probe.Units)))
	if _, err := decoder.Token(); err != nil { // opening brace
		return nil
	}
	var order []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return order
		}
		name, _ := key.(string)
		order = append(order, name)
		var skip json.RawMessage
		if decoder.Decode(&skip) != nil {
			return order
		}
	}
	return order
}

// Status of a unit, which is Pending when it has no record.
func (s *State) Status(id string) string {
	if entry, ok := s.Units[id]; ok {
		return entry.Status
	}
	return Pending
}

// Entry for a unit, or nil.
func (s *State) Entry(id string) *Entry {
	return s.Units[id]
}

// Set records a unit.
func (s *State) Set(id string, entry *Entry) {
	if _, seen := s.Units[id]; !seen {
		s.order = append(s.order, id)
	}
	s.Units[id] = entry
}

// Clear makes a unit pending again.
func (s *State) Clear(id string) {
	delete(s.Units, id)
	for i, name := range s.order {
		if name == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// Save writes the file, preserving the order units were first recorded in.
func (s *State) Save() error {
	var builder strings.Builder
	builder.WriteString("{\n  \"units\": {")

	written := 0
	for _, id := range s.ordered() {
		entry, ok := s.Units[id]
		if !ok {
			continue
		}
		body, err := json.MarshalIndent(entry, "    ", "  ")
		if err != nil {
			return err
		}
		if written > 0 {
			builder.WriteString(",")
		}
		key, err := json.Marshal(id)
		if err != nil {
			return err
		}
		builder.WriteString("\n    " + string(key) + ": " + string(body))
		written++
	}
	if written > 0 {
		builder.WriteString("\n  ")
	}
	builder.WriteString("}\n}\n")

	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.Path, []byte(builder.String()), 0o644)
}

func (s *State) ordered() []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range s.order {
		if _, ok := s.Units[id]; ok && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	var rest []string
	for id := range s.Units {
		if !seen[id] {
			rest = append(rest, id)
		}
	}
	sortIDs(rest)
	return append(out, rest...)
}

func sortIDs(ids []string) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}

// Open lists the units still to do: pending or blocked, in plan order.
func Open(p *plan.Plan, s *State) []plan.Unit {
	var out []plan.Unit
	for _, u := range p.Units {
		if status := s.Status(u.ID); status == Pending || status == Blocked {
			out = append(out, u)
		}
	}
	return out
}

// Resume is the first open unit, or nil when the plan is complete.
func Resume(p *plan.Plan, s *State) *plan.Unit {
	if open := Open(p, s); len(open) > 0 {
		return &open[0]
	}
	return nil
}

// NextOpen is the first open unit after this one, or nil.
func NextOpen(p *plan.Plan, s *State, after plan.Unit) *plan.Unit {
	cut := p.Index(after.ID)
	for _, u := range Open(p, s) {
		if p.Index(u.ID) > cut {
			found := u
			return &found
		}
	}
	return nil
}

// Orphans are units the state records that the plan no longer has.
func Orphans(p *plan.Plan, s *State) []string {
	var out []string
	for _, id := range s.ordered() {
		if p.Unit(id) == nil {
			out = append(out, id)
		}
	}
	return out
}
