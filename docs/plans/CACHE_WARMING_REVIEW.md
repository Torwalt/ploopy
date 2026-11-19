---
reviews: docs/plans/CACHE_WARMING.md
test: no
review-base: 1be6f592df48213f88fc069f36c1e50ea3d05aed
review-head: 66035915e305d41cbc913a8565344070c13eaded
---

# Review of CACHE_WARMING

Status: written by `ploopy review` on 2026-11-19 against
`66035915e305d41cbc913a8565344070c13eaded`. It reviews 6 landed units of
`docs/plans/CACHE_WARMING.md`.

Only required fixes go into `docs/plans/CACHE_WARMING_FIXES.md`: evidenced
defects, regressions, violated repository rules or unmet plan requirements.
Useful optional improvements go into one follow-up issue for this review pass,
using the session's persistence instructions. Unsupported concerns are dropped.
Zero fixes and zero optional findings is a successful review. Explain required
design choices and their alternatives under *For the author*.

One frontier session reviews the complete change `git diff
1be6f592df48213f88fc069f36c1e50ea3d05aed..66035915e305d41cbc913a8565344070c13eaded`
against the plan at the recorded tip, including fixes already applied. It checks
the units and their integration itself, without collecting sessions, and writes
the fix plan only when necessary.

## Stage 1 — Review

### 1.1 Review the branch against the plan

Review: direct
Agent: claude
Model: opus
Effort: high

**Goal:** evidence checked, duplicates merged, required fixes separated from
optional improvements and unsupported concerns dropped; a fix plan only for
required fixes, with any necessary design decisions resolved; optional findings
persisted together in one issue.

**Done when:** the review summary is committed under *For the author*, the fix
plan when necessary lints clean, and any optional findings are committed in one
follow-up document linked from the summary. No source or tests changed.


## For the author

Reviewed 2026-11-19: the complete change
`1be6f592df48213f88fc069f36c1e50ea3d05aed..66035915e305d41cbc913a8565344070c13eaded`
against `docs/plans/CACHE_WARMING.md`. That covers all six units (1.1, 2.1,
2.2, 2.3, 3.1, 3.2) and how they fit together. Plan and state bookkeeping were
left out.

**Outcome: no required fixes, so there is no fix plan.** Two optional findings
are in
[`CACHE_WARMING_FOLLOWUPS_66035915e305.md`](CACHE_WARMING_FOLLOWUPS_66035915e305.md).

What was checked against the plan:

- **1.1.** `Start` passes `--input-format stream-json` and gives the child an
  `os.Pipe` read end, closing the parent's copy. It writes the prompt line in
  the measured format. The write end is closed exactly once, under the write
  lock, in three places: on `result`, on `Kill`, and in `read`'s defer when the
  output ends without a result. `dev/fakeagent` reads one line under
  stream-json input. Tests exist for the three Wait fakes.
- **2.1.** The warmer matches the contract:
  - The clock is set by the prompt, by main-thread tool results and by pings.
  - Subagent lines are excluded through `parent_tool_use_id`.
  - Deadlines: foreground Bash uses `timeout` or its default, capped at the
    maximum, both taken from `BASH_*_TIMEOUT_MS` when set. Monitor is capped at
    an hour. Any other tool, background Bash included, has no known deadline.
  - Rules 1–5 are in `step`, and requests are numbered `ploopy-warm-<n>`.
  - Warming stops, with a drift note through `s.drift`, on an error answer, a
    synthetic answer or the 60 s answer wait.
  - A ping's estimate is a cache read of the last main-thread context.
  - `Outcome.Pings` and `Outcome.PingTokens` are zero without pings.
  - The constants are package variables, and the tests shrink them with their
    ratios kept.
- **2.2.** `cache` accepts only `warm` and `1h`, and only under
  `[agents.claude]`. The load errors name `agents.<name>.cache`.
  `allHarnesses` carries the value to every Claude session: runs, reviews,
  rescue and landing repair, and after `adjust` too. `1h` sets the hour and
  builds no warmer.
- **2.3.** Pings are booked in the stats log and the history, both omitted when
  zero. The report prints them on their own line, marked as estimated. Tokens,
  the cache share and the unit's state entry stay as reported.
- **3.1.** The background-command rule is in every skill whose session runs
  commands.
- **3.2.** The README and the ploopy skill describe warming, the ping, the
  opt-out and the estimates. Nothing in either still says the prompt goes on
  stdin as plain text.

Verification: `go test -race -count=3` passed for `internal/harness/claude`,
`dev/fakeagent`, `internal/config` and `internal/stats`, which settles the
timing tests' stability. `go vet ./internal/harness/...` and `gofmt -l` were
clean.

Optional findings, all in the follow-up document:

1. A tool result in a turn with several calls moves the clock before any
   request has gone out. A real session log shows the second `tool_use` of one
   message arriving after the first call's result. This follows the contract as
   written, so changing it is your design call, after a measurement.
2. The `ploopy history` import does not copy `Pings` and `PingTokens` from the
   stats log. Live runs book them correctly.

Dropped concerns:

- *A later call's deadline is measured from when it was seen, not from when it
  started.* This is the contract's explicit rule. It is noted under finding 1
  rather than raised as a finding of its own.
- *Landing repair sessions do not book pings.* `internal/land` never wrote
  stats or history for its sessions (it keeps only `CostUSD`). That predates
  this pass, and unit 2.3 named only `accountSession`.
- *Pings that got an error, a synthetic answer or no answer are still booked as
  estimated reads.* The plan's estimation rule makes no exception for them, and
  warming stops after the first such ping, so there is at most one per session.
- *A blocked stdin write could hang `finish` or `Kill`.* `Kill` kills the
  process before it closes stdin. When the output ends, the child has gone, so
  a write fails with EPIPE. The concern is speculative.
- *`dev/fakeagent` does not answer side questions.* It is a dev-only tool, and
  its scenarios never wait four minutes.
- *An `Agent` call is pinged while its subagent runs.* The contract says so:
  any other tool has no known deadline.

No design decisions were needed, since there are no required fixes.
