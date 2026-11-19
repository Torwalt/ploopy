# Follow-ups from the review of CACHE_WARMING

Reviewed plan: `docs/plans/CACHE_WARMING.md`. Reviewed revision:
`66035915e305d41cbc913a8565344070c13eaded` (change from
`1be6f592df48213f88fc069f36c1e50ea3d05aed`). Written 2026-11-19 by the direct
review `docs/plans/CACHE_WARMING_REVIEW.md`.

Every finding here is optional: the landed work meets the plan without it.

## A tool result in a turn with several calls moves the clock too early

**Evidence.** `session.toolResults` (`internal/harness/claude/claude.go`) calls
`warmer.results` for every main-thread `user` line carrying a `tool_result`,
and `warmer.results` (`internal/harness/claude/warm.go`) sets the clock and
resets the ping count without condition. In a real session log of this plan,
`.ploopy/cache_warming/logs/2.3-0.log` lines 32–35, one assistant message
(`msg_011ChCTFDsgZsBZKYBpdzKsA`) issued two `Bash` calls. Its second
`tool_use` line came only after the first call's `tool_result`. So when the
first result is read, Claude Code has sent no request, and the adapter cannot
tell yet whether more calls of that message follow.

**Effect.** Take a turn whose first call runs for more than a minute and whose
next call is long. The clock moves to the first result, but the cache was last
renewed when the turn's request went out. The first ping then comes four minutes
after the first result, which is past the five-minute expiry, so the context is
written again. Rule 5's count is reset early in the same way. The deadline of a
later call is also measured from when its `tool_use` was seen, not from when it
started.

**Proposed improvement.** Move the clock only when a request has demonstrably
gone out. For example, record each call's assistant `message.id`, and set the
clock on the first main-thread assistant line of a new message id instead of on
a result line. That is late by the request's time to first token, a few seconds
against the one-minute margin. Alternatively, keep the clock on results but
undo the move when a further `tool_use` of the same message id arrives. Either
way, measure first how Claude Code 2.1.287 emits a turn with two long
foreground `Bash` calls.

**Why optional.** The implementation follows *The warming contract* as written:
"a main-thread `user` line carrying a `tool_result` is read" sets the clock.
Changing that rule is a change of the author's contract and needs a measurement.
The cost is at most one missed renewal per affected turn, and correctness is
untouched.

## `ploopy history` import drops a session's pings

**Evidence.** `importHistory` (`cmd/ploopy/history.go`, the `history.Record`
literal near line 119) copies `Tokens`, `CostUSD`, `Budget` and `Degraded` from
each stats-log session record, but not the new `Pings` and `PingTokens`.

**Effect.** A session that reaches the history only through the import has no
ping figures there, even though its stats log holds them.

**Proposed improvement.** Add `Pings: rec.Pings, PingTokens: rec.PingTokens` to
that literal, and extend the import test with a pinged record.

**Why optional.** Live runs write pings to both the stats log and the history
through `accountSession`, which is what unit 2.3 required. The import is a
recovery path for sessions the history missed, and nothing reads ping figures
from the history yet: `ploopy report` reads the stats log, and
`history.Estimates` reads only reported tokens.
