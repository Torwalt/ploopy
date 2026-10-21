package loop

import (
	"fmt"
	"time"

	"github.com/Torwalt/ploopy/internal/harness"
)

type routeKey struct{ harness, model string }

func (l *Loop) routed(agent Agent) Agent {
	key := routeKey{agent.Harness.Name(), agent.Model}
	if until := l.routeLimits[key]; until.After(l.opts.Now()) {
		if model := harness.FallbackModel(agent.Harness, agent.Model); model != "" {
			agent.Model = model
		}
	}
	return agent
}

// toFallback changes the route before considering another harness or waiting.
func (l *Loop) toFallback(agent Agent, limit *harness.RateLimit) bool {
	model := harness.FallbackModel(agent.Harness, agent.Model)
	if model == "" || model == agent.Model {
		return false
	}
	until := limit.ResetAt
	if !until.After(l.opts.Now()) {
		until = l.opts.Now().Add(l.opts.LimitWait)
	}
	if l.routeLimits == nil {
		l.routeLimits = map[routeKey]time.Time{}
	}
	l.routeLimits[routeKey{agent.Harness.Name(), agent.Model}] = until
	next := agent
	next.Model = model
	message := fmt.Sprintf("%s hit its usage limit; continuing with %s until %s",
		agent.Label(), next.Label(), until.Format(time.RFC3339))
	l.record("switch", message)
	l.report.Say("%s", message)
	return true
}

// routeWake is when the subscription behind an API route can be tried again.
func (l *Loop) routeWake(agent Agent) time.Time {
	var wake time.Time
	for key, until := range l.routeLimits {
		if key.harness == agent.Harness.Name() && until.After(l.opts.Now()) &&
			harness.FallbackModel(agent.Harness, key.model) == agent.Model &&
			(wake.IsZero() || until.Before(wake)) {
			wake = until
		}
	}
	return wake
}
