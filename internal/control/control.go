// Package control is how a running run is found and changed from outside it.
//
// A run announces itself in the user's runtime directory, one file per
// process. Another ploopy writes the settings the author wants changed into a
// second file beside it, and the run reads them where they matter. Each file
// has one writer. A reboot empties the directory, so a powered-off run leaves
// nothing behind.
package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Agent names a harness and the model and effort it runs at.
type Agent struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// Label names the agent the way the state file records it.
func (a Agent) Label() string {
	return strings.Join(strings.Fields(a.Harness+" "+a.Model+" "+a.Effort), " ")
}

// Settings are what can change while a run goes on. In a change, an unset
// field leaves the run's own setting alone.
type Settings struct {
	Finish    string `json:"finish,omitempty"` // none, suspend or poweroff
	Peak      string `json:"peak,omitempty"`   // wait or run
	Push      *bool  `json:"push,omitempty"`
	Stop      *bool  `json:"stop,omitempty"`      // stop once the current unit is done
	Secondary *Agent `json:"secondary,omitempty"` // an empty harness means none
}

// Over lays a change over base settings.
func (s Settings) Over(base Settings) Settings {
	out := base
	if s.Finish != "" {
		out.Finish = s.Finish
	}
	if s.Peak != "" {
		out.Peak = s.Peak
	}
	if s.Push != nil {
		out.Push = s.Push
	}
	if s.Stop != nil {
		out.Stop = s.Stop
	}
	if s.Secondary != nil {
		out.Secondary = s.Secondary
	}
	return out
}

// Pushes reports whether the settings push when the run ends.
func (s Settings) Pushes() bool { return s.Push != nil && *s.Push }

// Stops reports whether the run is to stop once its current unit is done.
func (s Settings) Stops() bool { return s.Stop != nil && *s.Stop }

// SecondaryAgent is the agent that takes over on a usage limit, or nil.
func (s Settings) SecondaryAgent() *Agent {
	if s.Secondary == nil || s.Secondary.Harness == "" {
		return nil
	}
	return s.Secondary
}

// Describe renders the settings for a person.
func (s Settings) Describe() string {
	var parts []string
	if s.Finish != "" && s.Finish != "none" {
		parts = append(parts, "then "+s.Finish)
	} else {
		parts = append(parts, "no end action")
	}
	if s.Pushes() {
		parts = append(parts, "push at the end")
	}
	if s.Peak == "wait" {
		parts = append(parts, "wait for off-peak")
	}
	if agent := s.SecondaryAgent(); agent != nil {
		parts = append(parts, agent.Label()+" on a usage limit")
	}
	if s.Stops() {
		parts = append(parts, "stop after the current unit")
	}
	return strings.Join(parts, " · ")
}

// Status is a run as it announced itself.
type Status struct {
	PID      int       `json:"pid"`
	Root     string    `json:"root"`
	Plan     string    `json:"plan"`
	Branch   string    `json:"branch,omitempty"`
	Agent    string    `json:"agent"`
	Unit     string    `json:"unit,omitempty"`
	Started  time.Time `json:"started"`
	Settings Settings  `json:"settings"` // what the run started with
}

// Live is a running run and what it is set to do now.
type Live struct {
	Status
	Wanted Settings
}

// Name is how a person tells runs apart.
func (l Live) Name() string {
	plan := strings.TrimSuffix(filepath.Base(l.Plan), filepath.Ext(l.Plan))
	return plan + " in " + filepath.Base(l.Root)
}

// Dir is where runs announce themselves.
func Dir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "ploopy")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("ploopy-%d", os.Getuid()))
}

func statusPath(dir string, pid int) string { return filepath.Join(dir, strconv.Itoa(pid)+".json") }
func adjustPath(dir string, pid int) string {
	return filepath.Join(dir, strconv.Itoa(pid)+".adjust.json")
}

// Run is this process's own announcement.
type Run struct {
	dir    string
	mu     sync.Mutex
	status Status
}

// Register announces a run. The pid and the start time are filled in.
func Register(status Status) (*Run, error) {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	status.PID = os.Getpid()
	if status.Started.IsZero() {
		status.Started = time.Now()
	}
	r := &Run{dir: dir, status: status}
	_ = os.Remove(adjustPath(dir, status.PID)) // a previous process's, by pid reuse
	return r, r.save()
}

func (r *Run) save() error {
	return writeJSON(statusPath(r.dir, r.status.PID), r.status)
}

// SetUnit records the unit the run is on.
func (r *Run) SetUnit(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.Unit = id
	_ = r.save()
}

// Wanted is what the run is set to do now: what it started with, with the
// author's changes laid over.
func (r *Run) Wanted() Settings {
	r.mu.Lock()
	base := r.status.Settings
	r.mu.Unlock()
	change, _ := readAdjust(r.dir, r.status.PID)
	return change.Over(base)
}

// Watch calls changed with the new settings whenever the author changes them,
// until stop is closed.
func (r *Run) Watch(stop <-chan struct{}, every time.Duration, changed func(Settings)) {
	last := r.Wanted()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			now := r.Wanted()
			if !same(now, last) {
				last = now
				changed(now)
			}
		}
	}
}

// Close withdraws the announcement.
func (r *Run) Close() {
	_ = os.Remove(statusPath(r.dir, r.status.PID))
	_ = os.Remove(adjustPath(r.dir, r.status.PID))
}

// List is every run going on, oldest first. Announcements of processes that
// are gone are cleared away.
func List() ([]Live, error) {
	dir := Dir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Live
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".adjust.json") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		if !alive(pid) {
			_ = os.Remove(statusPath(dir, pid))
			_ = os.Remove(adjustPath(dir, pid))
			continue
		}
		live, err := load(dir, pid)
		if err != nil {
			continue
		}
		out = append(out, live)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out, nil
}

// Adjust lays a change over what a run was already asked to do, and returns
// what it is set to do now.
func Adjust(pid int, change Settings) (Live, error) {
	dir := Dir()
	if !alive(pid) {
		return Live{}, fmt.Errorf("no run with pid %d", pid)
	}
	if _, err := load(dir, pid); err != nil {
		return Live{}, err
	}
	previous, err := readAdjust(dir, pid)
	if err != nil {
		return Live{}, err
	}
	if err := writeJSON(adjustPath(dir, pid), change.Over(previous)); err != nil {
		return Live{}, err
	}
	return load(dir, pid)
}

func load(dir string, pid int) (Live, error) {
	raw, err := os.ReadFile(statusPath(dir, pid))
	if err != nil {
		return Live{}, err
	}
	var status Status
	if err := json.Unmarshal(raw, &status); err != nil {
		return Live{}, fmt.Errorf("%s: %w", statusPath(dir, pid), err)
	}
	change, err := readAdjust(dir, pid)
	if err != nil {
		return Live{}, err
	}
	return Live{Status: status, Wanted: change.Over(status.Settings)}, nil
}

func readAdjust(dir string, pid int) (Settings, error) {
	var change Settings
	raw, err := os.ReadFile(adjustPath(dir, pid))
	if os.IsNotExist(err) {
		return change, nil
	}
	if err != nil {
		return change, err
	}
	if err := json.Unmarshal(raw, &change); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", adjustPath(dir, pid), err)
	}
	return change, nil
}

// writeJSON replaces a file whole, so a reader never sees half of it.
func writeJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func same(a, b Settings) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
