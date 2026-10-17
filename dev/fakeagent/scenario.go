package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	sessionWindow = 5 * time.Hour
	weeklyWindow  = 7 * 24 * time.Hour
)

// Scenario is one harness's table in scenario.toml.
type Scenario struct {
	Seconds          float64 `toml:"seconds"`
	SessionUsed      float64 `toml:"session_used"`
	SessionResetsIn  string  `toml:"session_resets_in"`
	WeeklyUsed       float64 `toml:"weekly_used"`
	WeeklyResetsIn   string  `toml:"weekly_resets_in"`
	PerSession       float64 `toml:"per_session"`
	PerSessionWeekly float64 `toml:"per_session_weekly"`
	Cost             float64 `toml:"cost"`
	Outcome          string  `toml:"outcome"`
}

// Window is one usage window with an absolute reset instant.
type Window struct {
	Used    float64   `json:"used"`
	ResetAt time.Time `json:"reset_at"`
}

// HarnessState is the usage a harness has accumulated in the sandbox.
type HarnessState struct {
	Session Window `json:"session"`
	Weekly  Window `json:"weekly"`
}

// State is the whole state.json.
type State map[string]*HarnessState

func sandboxDir() (string, error) {
	dir := os.Getenv("PLOOPY_SANDBOX")
	if dir == "" {
		return "", fmt.Errorf("PLOOPY_SANDBOX is not set")
	}
	return dir, nil
}

// scenarios reads every table in the sandbox's scenario.toml.
func scenarios() (map[string]Scenario, time.Time, error) {
	dir, err := sandboxDir()
	if err != nil {
		return nil, time.Time{}, err
	}
	path := filepath.Join(dir, "scenario.toml")
	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	var all map[string]Scenario
	if _, err := toml.DecodeFile(path, &all); err != nil {
		return nil, time.Time{}, fmt.Errorf("%s: %w", path, err)
	}
	return all, info.ModTime(), nil
}

func seed(sc Scenario, now time.Time) *HarnessState {
	h := &HarnessState{
		Session: Window{Used: sc.SessionUsed},
		Weekly:  Window{Used: sc.WeeklyUsed},
	}
	if d, err := time.ParseDuration(sc.SessionResetsIn); err == nil && sc.SessionResetsIn != "" {
		h.Session.ResetAt = now.Add(d)
	}
	if d, err := time.ParseDuration(sc.WeeklyResetsIn); err == nil && sc.WeeklyResetsIn != "" {
		h.Weekly.ResetAt = now.Add(d)
	}
	return h
}

// openState reads the sandbox state, seeding it from the scenario on first
// use and again when the scenario changed.
func openState(name string) (State, *HarnessState, string, error) {
	all, scenarioAt, err := scenarios()
	if err != nil {
		return nil, nil, "", err
	}
	dir, _ := sandboxDir()
	path := filepath.Join(dir, "state.json")

	state := State{}
	reseed := true
	if info, err := os.Stat(path); err == nil {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, "", err
		}
		if err := json.Unmarshal(data, &state); err != nil {
			return nil, nil, "", fmt.Errorf("%s: %w", path, err)
		}
		reseed = scenarioAt.After(info.ModTime())
	}
	if reseed {
		state = State{}
	}
	now := time.Now()
	for harness, sc := range all {
		if _, ok := state[harness]; !ok || reseed {
			state[harness] = seed(sc, now)
		}
	}
	entry, ok := state[name]
	if !ok {
		return nil, nil, "", fmt.Errorf("scenario.toml has no [%s] table", name)
	}
	return state, entry, path, nil
}

func saveState(path string, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func (w *Window) refresh(now time.Time, length time.Duration) {
	if w.ResetAt.IsZero() || w.ResetAt.After(now) {
		return
	}
	w.Used = 0
	w.ResetAt = now.Add(length)
}

func (h *HarnessState) refresh(now time.Time) {
	h.Session.refresh(now, sessionWindow)
	h.Weekly.refresh(now, weeklyWindow)
}

// spent is a window that has run out: its reset and whether it is the weekly
// one. The five-hour window is the narrower limit, so it is reported first.
func (h *HarnessState) spent(now time.Time) (reset time.Time, weekly, ok bool) {
	if h.Session.ResetAt.After(now) && h.Session.Used >= 1 {
		return h.Session.ResetAt, false, true
	}
	if h.Weekly.ResetAt.After(now) && h.Weekly.Used >= 1 {
		return h.Weekly.ResetAt, true, true
	}
	return time.Time{}, false, false
}

// consume records a session's share, opening a fresh window when none runs.
func (h *HarnessState) consume(sc Scenario, now time.Time) {
	h.refresh(now)
	if h.Session.ResetAt.IsZero() {
		h.Session.ResetAt = now.Add(sessionWindow)
		h.Session.Used = 0
	}
	h.Session.Used = clamp(h.Session.Used + sc.PerSession)

	if h.Weekly.ResetAt.IsZero() && sc.PerSessionWeekly > 0 {
		h.Weekly.ResetAt = now.Add(weeklyWindow)
	}
	if !h.Weekly.ResetAt.IsZero() {
		h.Weekly.Used = clamp(h.Weekly.Used + sc.PerSessionWeekly)
	}
}

func clamp(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func copyState(h *HarnessState) HarnessState {
	return HarnessState{Session: h.Session, Weekly: h.Weekly}
}
