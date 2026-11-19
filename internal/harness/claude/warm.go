package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
)

// Tuning for keeping the five-minute cache warm. Variables so tests can shrink
// them; keep the ratios between them.
var (
	pingAfter      = 4 * time.Minute
	deadlineMargin = 4*time.Minute + 30*time.Second
	// A ping costs about a tenth of the context and a rewrite 1.25 times it, so
	// after this many writing anew would have been as cheap.
	pingCap    = 12
	answerWait = 60 * time.Second
	cacheTTL   = "5m"

	// What the tools the deadline is read from can run for.
	defaultBashTimeout    = 2 * time.Minute
	maxBashTimeout        = 10 * time.Minute
	defaultMonitorTimeout = 5 * time.Minute
	maxMonitorTimeout     = time.Hour
)

const pingQuestion = "Reply with exactly: OK"

// warmer keeps a session's prompt cache from expiring while a tool call runs.
// A request renews the cache, so while the main thread waits on a tool and no
// request is due, it asks Claude Code a side question, which reads the same
// cached prefix. ploopy never talks to the provider: the question goes down
// the session's own stdin. A nil warmer is warming switched off: every method
// does nothing.
type warmer struct {
	send func([]byte) error

	// What the session's environment sets; they replace the tools' defaults.
	bashDefault, bashMax time.Duration

	mu    sync.Mutex
	clock time.Time // when the main thread last sent a request
	// calls are the main-thread tool calls still without a result. The time is
	// when a call can last until; zero when that is unknown.
	calls    map[string]time.Time
	pings    int // since the last real request
	sent     int
	tokens   harness.Tokens
	context  int // what the last main-thread assistant line reported
	awaiting string
	askedAt  time.Time
	seq      int
	stopped  string

	wake     chan struct{}
	quit     chan struct{}
	finished chan struct{}
}

func newWarmer(send func([]byte) error) *warmer {
	return &warmer{
		send:        send,
		bashDefault: envDuration("BASH_DEFAULT_TIMEOUT_MS", defaultBashTimeout),
		bashMax:     envDuration("BASH_MAX_TIMEOUT_MS", maxBashTimeout),
		calls:       map[string]time.Time{},
		wake:        make(chan struct{}, 1),
		quit:        make(chan struct{}),
		finished:    make(chan struct{}),
	}
}

func envDuration(name string, fallback time.Duration) time.Duration {
	ms, err := strconv.Atoi(os.Getenv(name))
	if err != nil || ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

func (w *warmer) notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// requested notes a request the main thread sent: the prompt, or the one that
// follows a tool result.
func (w *warmer) requested(now time.Time) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.clock = now
	w.pings = 0
	w.mu.Unlock()
	w.notify()
}

// call notes a main-thread tool call that has begun.
func (w *warmer) call(id, tool string, input json.RawMessage, seen time.Time) {
	if w == nil {
		return
	}
	if id == "" {
		return
	}
	w.mu.Lock()
	w.calls[id] = w.deadline(tool, input, seen)
	w.mu.Unlock()
	w.notify()
}

// results notes a main-thread user line that carried tool results. Claude Code
// sends its next request right after it.
func (w *warmer) results(ids []string, now time.Time) {
	if w == nil {
		return
	}
	w.mu.Lock()
	for _, id := range ids {
		delete(w.calls, id)
	}
	w.clock = now
	w.pings = 0
	w.mu.Unlock()
	w.notify()
}

// usage notes how large the context was on the main thread's last request.
func (w *warmer) usage(u usage) {
	if w == nil {
		return
	}
	size := u.Input + u.CacheRead + u.CacheCreation
	if size == 0 {
		return
	}
	w.mu.Lock()
	w.context = size
	w.mu.Unlock()
}

// deadline is when a call can last until, or the zero time. Claude Code never
// blocks a foreground command or a monitor longer than its limit.
func (w *warmer) deadline(tool string, input json.RawMessage, seen time.Time) time.Time {
	switch tool {
	case "Bash":
		var in struct {
			Timeout    float64 `json:"timeout"`
			Background bool    `json:"run_in_background"`
		}
		if json.Unmarshal(input, &in) != nil || in.Background {
			return time.Time{}
		}
		limit := w.bashDefault
		if in.Timeout > 0 {
			limit = time.Duration(in.Timeout * float64(time.Millisecond))
		}
		return seen.Add(min(limit, w.bashMax))
	case "Monitor":
		var in struct {
			Timeout float64 `json:"timeout_ms"`
		}
		if json.Unmarshal(input, &in) != nil {
			return time.Time{}
		}
		limit := defaultMonitorTimeout
		if in.Timeout > 0 {
			limit = time.Duration(in.Timeout * float64(time.Millisecond))
		}
		return seen.Add(min(limit, maxMonitorTimeout))
	}
	return time.Time{}
}

// latest is the turn's deadline: the latest of its calls', unknown when any
// call's is.
func (w *warmer) latest() (time.Time, bool) {
	var latest time.Time
	for _, at := range w.calls {
		if at.IsZero() {
			return time.Time{}, false
		}
		if at.After(latest) {
			latest = at
		}
	}
	return latest, true
}

// answered reads the reply to a ping.
func (w *warmer) answered(id, problem string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	if id == w.awaiting {
		w.awaiting = ""
		if problem != "" {
			w.stop(problem)
		}
	}
	w.mu.Unlock()
	w.notify()
}

// stop ends warming for the rest of the session; the first reason stands.
func (w *warmer) stop(reason string) {
	if w.stopped == "" {
		w.stopped = reason
	}
}

// run decides when to ping, until the session ends or warming stops.
func (w *warmer) run() {
	defer close(w.finished)
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		wait, ok := w.step(time.Now())
		if !ok {
			return
		}
		timer.Reset(wait)
		select {
		case <-w.quit:
			return
		case <-w.wake:
		case <-timer.C:
		}
	}
}

// step sends a ping if one is due. It returns how long to sleep before looking
// again, and false once warming is over.
func (w *warmer) step(now time.Time) (time.Duration, bool) {
	const idle = time.Hour // a change of state wakes it earlier

	w.mu.Lock()
	if w.stopped != "" {
		w.mu.Unlock()
		return 0, false
	}
	if w.awaiting != "" {
		if left := w.askedAt.Add(answerWait).Sub(now); left > 0 {
			w.mu.Unlock()
			return left, true
		}
		w.stop(fmt.Sprintf("a ping got no answer within %s", answerWait))
		w.mu.Unlock()
		return 0, false
	}
	if len(w.calls) == 0 || w.pings >= pingCap {
		w.mu.Unlock()
		return idle, true
	}
	if due := w.clock.Add(pingAfter); now.Before(due) {
		w.mu.Unlock()
		return due.Sub(now), true
	}
	if at, known := w.latest(); known && !at.After(w.clock.Add(deadlineMargin)) {
		w.mu.Unlock()
		return idle, true
	}

	w.seq++
	id := fmt.Sprintf("ploopy-warm-%d", w.seq)
	line, err := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": id,
		"request":    map[string]any{"subtype": "side_question", "question": pingQuestion},
	})
	if err != nil {
		w.stop("a ping did not encode: " + err.Error())
		w.mu.Unlock()
		return 0, false
	}
	// Booked before the write: the answer can be read before send returns.
	pings, sent, tokens := w.pings, w.sent, w.tokens
	w.awaiting, w.askedAt, w.clock = id, now, now
	w.pings++
	w.sent++
	w.tokens = w.tokens.Add(harness.Tokens{CacheRead: w.context})
	w.mu.Unlock()

	// A write that fails means the session is over, and says nothing about the
	// cache.
	if err := w.send(append(line, '\n')); err != nil {
		w.mu.Lock()
		w.awaiting, w.pings, w.sent, w.tokens = "", pings, sent, tokens
		w.mu.Unlock()
		return 0, false
	}
	return 0, true
}

// finish stops the goroutine and returns why warming stopped, if it did.
func (w *warmer) finish() string {
	if w == nil {
		return ""
	}
	close(w.quit)
	<-w.finished
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopped
}

// totals is how many pings were sent and what they are estimated to have read.
func (w *warmer) totals() (int, harness.Tokens) {
	if w == nil {
		return 0, harness.Tokens{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sent, w.tokens
}
