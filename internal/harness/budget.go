package harness

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Budget is how much of a subscription's usage windows is spent, as the
// harness reported it at one instant.
type Budget struct {
	At      time.Time `json:"at"`
	Session Allowance `json:"session"` // the rolling five-hour window
	Weekly  Allowance `json:"weekly"`
}

// Allowance is one usage window: the share of it spent, from 0 to 1, and when
// it resets.
type Allowance struct {
	Used    float64       `json:"used"`
	ResetAt time.Time     `json:"reset_at"`
	Length  time.Duration `json:"length"`
}

// Window lengths both subscriptions use.
const (
	SessionWindow = 5 * time.Hour
	WeeklyWindow  = 7 * 24 * time.Hour
)

// Active reports whether the window is still running at an instant. A window
// whose reset passed is spent from zero by the next request.
func (a Allowance) Active(at time.Time) bool { return a.ResetAt.After(at) }

// UsedAt is the share spent at an instant: nothing once the window reset.
func (a Allowance) UsedAt(at time.Time) float64 {
	if !a.Active(at) {
		return 0
	}
	return a.Used
}

// Started is when the window began.
func (a Allowance) Started() time.Time { return a.ResetAt.Add(-a.length()) }

func (a Allowance) length() time.Duration {
	if a.Length > 0 {
		return a.Length
	}
	return SessionWindow
}

// Describe renders the budget for a person, in local time.
func (b *Budget) Describe(now time.Time) string {
	if b == nil {
		return "no usage windows seen"
	}
	return fmt.Sprintf("5h %s · weekly %s", b.Session.describe(now), b.Weekly.describe(now))
}

func (a Allowance) describe(now time.Time) string {
	if a.ResetAt.IsZero() {
		return "unknown"
	}
	if !a.Active(now) {
		return "free"
	}
	reset := a.ResetAt.In(now.Location())
	layout := "15:04"
	switch {
	case reset.Sub(now) > 6*24*time.Hour:
		layout = "Mon 2 Jan 15:04"
	case reset.YearDay() != now.YearDay():
		layout = "Mon 15:04"
	}
	return fmt.Sprintf("%.0f%% → %s", a.Used*100, reset.Format(layout))
}

// Metered is a harness paid for by subscription, in usage windows. Budget
// reads them without starting a session; it is nil when the harness reports
// them only from inside one.
type Metered interface {
	Budget(ctx context.Context) (*Budget, error)
}

// Drift is what an adapter could not read from a harness's output. A harness
// that changed its format leaves the run judged from git as usual, but its
// numbers incomplete, so the loss is reported rather than swallowed.
type Drift struct {
	undecoded int
	first     string
	missing   []string
}

// Undecoded records a structured line that did not decode.
func (d *Drift) Undecoded(what string, err error) {
	d.undecoded++
	if d.first == "" {
		d.first = what + ": " + err.Error()
	}
}

// Missing records something a finished session should have reported.
func (d *Drift) Missing(what string) {
	for _, known := range d.missing {
		if known == what {
			return
		}
	}
	d.missing = append(d.missing, what)
}

// Notes is the drift for a person, or nothing when the adapter read it all.
func (d *Drift) Notes() []string {
	notes := append([]string(nil), d.missing...)
	switch {
	case d.undecoded == 1:
		notes = append(notes, "1 line did not decode: "+truncate(d.first, 160))
	case d.undecoded > 1:
		notes = append(notes, fmt.Sprintf("%d lines did not decode; first: %s", d.undecoded, truncate(d.first, 160)))
	}
	return notes
}

// Structured tells a line meant as an event from what a harness printed
// beside its event stream, such as warnings on stderr.
func Structured(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "{") }
