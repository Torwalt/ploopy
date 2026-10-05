// Package harness is the contract between the loop and a coding agent.
//
// One unattended session is a typed event stream and an outcome. An adapter
// turns a harness's own output into that; the loop never learns which adapter
// it holds. What an adapter cannot observe it leaves zero.
package harness

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Marker is how a session says it finished.
type Marker string

const (
	None        Marker = ""
	Done        Marker = "DONE"
	NothingToDo Marker = "NOTHING-TO-DO"
	Blocked     Marker = "BLOCKED"
)

// EventKind distinguishes what a session just did.
type EventKind int

const (
	EventText EventKind = iota
	EventToolUse
	EventUsage
	EventNotice
	EventRaw
)

// Event is one observable step of a session. On an EventUsage, CostUSD and
// Tokens are the session's running totals, not an increment.
type Event struct {
	Kind    EventKind
	Text    string
	Tool    string
	CostUSD float64
	Tokens  int
}

// RateLimit is a session that ended because the account ran out of room, not
// because the work is impossible. It is never a block.
type RateLimit struct {
	Reason  string
	ResetAt time.Time // zero when the harness gave no time
}

// Tokens is what a session consumed. Reasoning is counted apart from output,
// and cache reads apart from input, so the fields add up to the total.
type Tokens struct {
	Input      int `json:"input,omitempty"`
	Output     int `json:"output,omitempty"`
	Reasoning  int `json:"reasoning,omitempty"`
	CacheRead  int `json:"cache_read,omitempty"`
	CacheWrite int `json:"cache_write,omitempty"`
}

// Add sums two counts.
func (t Tokens) Add(other Tokens) Tokens {
	return Tokens{
		Input:      t.Input + other.Input,
		Output:     t.Output + other.Output,
		Reasoning:  t.Reasoning + other.Reasoning,
		CacheRead:  t.CacheRead + other.CacheRead,
		CacheWrite: t.CacheWrite + other.CacheWrite,
	}
}

// Prompt is every token the model read, cached or not.
func (t Tokens) Prompt() int { return t.Input + t.CacheRead + t.CacheWrite }

// Generated is every token the model wrote, reasoning included.
func (t Tokens) Generated() int { return t.Output + t.Reasoning }

// Total is everything.
func (t Tokens) Total() int { return t.Prompt() + t.Generated() }

// Outcome is how a session ended. What an adapter cannot observe stays zero.
type Outcome struct {
	Marker    Marker
	Reason    string
	Exit      int
	TimedOut  bool
	SessionID string
	CostUSD   float64
	Tokens    Tokens
	Turns     int
	RateLimit *RateLimit

	// Exhausted is set when the run's own spending cap was reached. It is a
	// deliberate stop, not a limit to wait out and not a block.
	Exhausted string
}

// Spec is one session to start.
type Spec struct {
	Prompt       string
	Model        string
	Effort       string
	Title        string
	SessionID    string
	Dir          string
	Deny         []string
	Extra        []string
	Timeout      time.Duration
	MaxBudgetUSD float64
	LogPath      string // the raw transcript, written verbatim
}

// Session is one running agent.
type Session interface {
	Events() <-chan Event
	Wait() (Outcome, error) // returns once Events() is closed
	Kill()
}

// Harness starts unattended sessions of one coding agent.
type Harness interface {
	Name() string
	Efforts() []string
	Models() []string // the default first
	PeakWindows() []Window
	Start(context.Context, Spec) (Session, error)
}

// Set is the harnesses ploopy can drive, in offer order.
type Set []Harness

// Get finds a harness by name.
func (s Set) Get(name string) Harness {
	for _, h := range s {
		if h.Name() == name {
			return h
		}
	}
	return nil
}

// Known reports whether a plan may name this harness.
func (s Set) Known(name string) bool { return s.Get(name) != nil }

// Efforts a harness offers, or nothing when it is unknown.
func (s Set) Efforts(name string) []string {
	if h := s.Get(name); h != nil {
		return h.Efforts()
	}
	return nil
}

// Names of every harness.
func (s Set) Names() []string {
	out := make([]string, 0, len(s))
	for _, h := range s {
		out = append(out, h.Name())
	}
	return out
}

// Window is a recurring span of expensive hours, in UTC. It does not wrap
// midnight: a provider whose peak does must declare two windows.
type Window struct {
	Days  []time.Weekday
	Start int // minutes after UTC midnight, inclusive
	End   int // minutes after UTC midnight, exclusive
}

func (w Window) onDay(day time.Weekday) bool {
	for _, d := range w.Days {
		if d == day {
			return true
		}
	}
	return false
}

// Peak reports whether an instant falls in a window and, if so, when the
// window closes.
func Peak(windows []Window, at time.Time) (bool, time.Time) {
	utc := at.UTC()
	minutes := utc.Hour()*60 + utc.Minute()
	for _, w := range windows {
		if !w.onDay(utc.Weekday()) || minutes < w.Start || minutes >= w.End {
			continue
		}
		midnight := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
		return true, midnight.Add(time.Duration(w.End) * time.Minute)
	}
	return false, time.Time{}
}

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	// A marker may be wrapped in the list bullets, emphasis or quoting a
	// harness adds around a final message.
	markerRe = regexp.MustCompile(`^[\s*\x60>#-]*(DONE|NOTHING-TO-DO|BLOCKED)\b[\s:—*\x60-]*(.*?)[\s*\x60]*$`)
	limitRe  = regexp.MustCompile(`(?i)\b(?:hit|reached|exceeded) (?:your|the)\b.*?\blimit\b`)
	resetRe  = regexp.MustCompile(`(?i)\bresets?(?: at)?\s+(\d{1,2})(?::(\d{2}))?\s*([ap]m)?(?:\s*\(([^)]+)\))?`)
)

const (
	markerWindow = 15
	limitWindow  = 12
)

// StripANSI removes the escape sequences a harness colours its output with.
func StripANSI(text string) string { return ansiRe.ReplaceAllString(text, "") }

// Summarise reduces a tool's input to the one value worth watching scroll by.
func Summarise(input json.RawMessage) string {
	var fields map[string]any
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	for _, key := range []string{"command", "file_path", "filePath", "path", "pattern", "url", "description"} {
		if value, ok := fields[key].(string); ok && value != "" {
			return truncate(value, 120)
		}
	}
	return ""
}

func truncate(text string, limit int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= limit {
		return text
	}
	return text[:limit-1] + "…"
}

func liveLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(StripANSI(text), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// ParseMarker reads how a session said it finished out of its text. The last
// marker near the end wins, and prose that merely mentions one does not count.
func ParseMarker(text string) (Marker, string) {
	lines := liveLines(text)
	if len(lines) > markerWindow {
		lines = lines[len(lines)-markerWindow:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if match := markerRe.FindStringSubmatch(lines[i]); match != nil {
			return Marker(match[1]), strings.TrimSpace(match[2])
		}
	}
	return None, ""
}

// RateLimitFromText recognises a usage limit in a harness that reports one
// only in prose, and reads the reset time where it names one.
func RateLimitFromText(text string, now time.Time) *RateLimit {
	lines := liveLines(text)
	if len(lines) > limitWindow {
		lines = lines[len(lines)-limitWindow:]
	}
	limited := ""
	for _, line := range lines {
		if limitRe.MatchString(line) {
			limited = line
		}
	}
	if limited == "" {
		return nil
	}

	limit := &RateLimit{Reason: strings.TrimSpace(limited)}
	match := resetRe.FindStringSubmatch(limited)
	if match == nil {
		return limit
	}

	zone := now.Location()
	if match[4] != "" {
		if named, err := time.LoadLocation(strings.TrimSpace(match[4])); err == nil {
			zone = named
		}
	}
	local := now.In(zone)
	hour, _ := strconv.Atoi(match[1])
	minute := 0
	if match[2] != "" {
		minute, _ = strconv.Atoi(match[2])
	}
	switch strings.ToLower(match[3]) {
	case "pm":
		if hour < 12 {
			hour += 12
		}
	case "am":
		if hour == 12 {
			hour = 0
		}
	}
	if hour > 23 || minute > 59 {
		return limit
	}

	reset := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, zone)
	if !reset.After(local) {
		reset = reset.AddDate(0, 0, 1)
	}
	// A reset time is the earliest the limit lifts; start a little after it.
	limit.ResetAt = reset.Add(2 * time.Minute)
	return limit
}
