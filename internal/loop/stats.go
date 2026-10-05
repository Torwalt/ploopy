package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
	"github.com/Torwalt/ploopy/internal/plan"
)

// Stats is what a run took, for the summary at its end.
type Stats struct {
	Started  time.Time
	Ended    time.Time
	Sessions int
	Session  time.Duration // agents at work
	Checks   time.Duration // setup, preflight, verify and test
	Waited   time.Duration // peak hours, usage limits and backoff
	CostUSD  float64
	Tokens   harness.Tokens
}

// record is one line of `stats.jsonl`: a session, a check, a wait or a whole
// run. The file is local and only ever appended to.
type record struct {
	Kind    string          `json:"kind"`
	Run     string          `json:"run"`
	Unit    string          `json:"unit,omitempty"`
	Stem    string          `json:"stem,omitempty"`
	Agent   string          `json:"agent,omitempty"`
	Started time.Time       `json:"started"`
	Seconds float64         `json:"seconds"`
	Verify  float64         `json:"verify_s,omitempty"`
	Test    float64         `json:"test_s,omitempty"`
	Tokens  *harness.Tokens `json:"tokens,omitempty"`
	CostUSD float64         `json:"cost_usd,omitempty"`
	Turns   int             `json:"turns,omitempty"`
	Tools   int             `json:"tools,omitempty"`
	Verdict string          `json:"verdict,omitempty"`
	Reason  string          `json:"reason,omitempty"`
}

// sessionInfo is what the loop itself saw of a session.
type sessionInfo struct {
	Started time.Time
	Took    time.Duration
	Tools   int
}

// spent is what a unit has taken so far, across its sessions and the runs
// that tried it. It lives in the progress file until the unit settles.
type spent struct {
	Elapsed time.Duration  `json:"elapsed,omitempty"`
	Session time.Duration  `json:"session,omitempty"`
	Verify  time.Duration  `json:"verify,omitempty"`
	Test    time.Duration  `json:"test,omitempty"`
	CostUSD float64        `json:"cost_usd,omitempty"`
	Tokens  harness.Tokens `json:"tokens"`
}

func (l *Loop) log(r record) {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(l.dir, "stats.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	r.Run = l.runID
	line, err := json.Marshal(r)
	if err != nil {
		return
	}
	_, _ = file.Write(append(line, '\n'))
}

// account books a session against its unit, the run and the stats log. The
// unit's clock runs from when it started, waits for a usage limit included.
func (l *Loop) account(u plan.Unit, stem string, agent Agent, info sessionInfo, outcome harness.Outcome, verdict, reason string) {
	now := l.opts.Now()
	verify, test := l.checked["verify"], l.checked["test"]
	l.checked = map[string]time.Duration{}

	s := &l.progress.Spent
	s.Elapsed += now.Sub(l.mark)
	l.mark = now
	s.Session += info.Took
	s.Verify += verify
	s.Test += test
	s.CostUSD += outcome.CostUSD
	s.Tokens = s.Tokens.Add(outcome.Tokens)
	_ = l.progress.save()

	l.stats.Sessions++
	l.stats.Session += info.Took
	l.stats.CostUSD += outcome.CostUSD
	l.stats.Tokens = l.stats.Tokens.Add(outcome.Tokens)

	r := record{
		Kind: "session", Unit: u.ID, Stem: stem, Agent: agent.Label(),
		Started: info.Started, Seconds: info.Took.Seconds(),
		Verify: verify.Seconds(), Test: test.Seconds(),
		CostUSD: outcome.CostUSD, Turns: outcome.Turns, Tools: info.Tools,
		Verdict: verdict, Reason: firstLine(reason),
	}
	if outcome.Tokens != (harness.Tokens{}) {
		tokens := outcome.Tokens
		r.Tokens = &tokens
	}
	l.log(r)
}

// pause waits, and books the wait against the run. A change the author makes
// meanwhile ends it early, and woken says so: the caller looks again.
func (l *Loop) pause(ctx context.Context, why string, d time.Duration) (woken bool, err error) {
	started := l.opts.Now()
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var woke atomic.Bool
	if l.opts.Steering != nil {
		changed := l.opts.Steering.Changed()
		go func() {
			select {
			case <-changed:
				woke.Store(true)
				cancel()
			case <-waitCtx.Done():
			}
		}()
	}

	err = l.opts.Sleep(waitCtx, d)
	took := l.opts.Now().Sub(started)
	l.stats.Waited += took
	l.log(record{Kind: "wait", Unit: l.unit, Started: started, Seconds: took.Seconds(), Reason: why})
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if woke.Load() {
		return true, nil
	}
	return false, err
}

// timedCheck runs a repository command and books the time it took.
func (l *Loop) timedCheck(ctx context.Context, name, command, logPath string) error {
	started := l.opts.Now()
	err := l.runCheck(ctx, command, logPath)
	took := l.opts.Now().Sub(started)
	l.stats.Checks += took
	if l.checked == nil {
		l.checked = map[string]time.Duration{}
	}
	l.checked[name] += took
	verdict := "passed"
	if err != nil {
		verdict = "failed"
	}
	l.log(record{Kind: "check", Unit: l.unit, Started: started, Seconds: took.Seconds(), Reason: name, Verdict: verdict})
	return err
}

// ended closes the run's books.
func (l *Loop) ended(status string) Stats {
	l.stats.Ended = l.opts.Now()
	r := record{
		Kind: "run", Started: l.stats.Started, Seconds: l.stats.Ended.Sub(l.stats.Started).Seconds(),
		CostUSD: l.stats.CostUSD, Verdict: status,
	}
	if l.stats.Tokens != (harness.Tokens{}) {
		tokens := l.stats.Tokens
		r.Tokens = &tokens
	}
	l.log(r)
	return l.stats
}

func firstLine(text string) string {
	for i, r := range text {
		if r == '\n' {
			return text[:i]
		}
	}
	return text
}

func seconds(d time.Duration) int { return int(d.Round(time.Second).Seconds()) }
