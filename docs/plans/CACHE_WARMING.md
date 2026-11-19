---
test: yes
---

# Keeping Claude's prompt cache warm through long checks

Status: active task document, written 2026-11-19 against `c0ad9e0`.

A Claude Code session on a subscription caches its prompt for an hour. The
hour costs twice the input price to write instead of 1.25×. A five-minute
cache is cheaper, but it expires while a long check runs, and the whole context
is then written again. This pass gives ploopy's Claude sessions the five-minute
cache and keeps it warm: while a tool call outlasts the cache, ploopy asks the
running session a trivial side question. Claude Code answers it from the same
cached prefix, and that read renews the cache.

## Decisions settled with the author (2026-11-19)

- **D1** Warm is the default for every session started through the Claude
  adapter (units, reviews, triage, rescue, landing repair): five-minute cache
  and pings. `[agents.claude] cache = "1h"` opts a repository out: an hour's
  cache and no pings. `cache` takes only `"warm"` and `"1h"`, and only under
  `[agents.claude]`.
- **D2** ploopy never talks to the provider. The ping is a `side_question`
  control request (what `/btw` sends) written to the session's stdin. Claude
  Code makes the request with its own login.
- **D3** When to ping follows from what the running tool can still take. It is
  not a fixed window. The rule is in *The warming contract* below.
- **D4** The harness reports neither a ping's tokens nor its cost. ploopy counts
  its pings and estimates their tokens, and books them apart from the session's
  reported usage. The report must not credit the cache with savings it did not
  make.
- **D5** All of it lives in `internal/harness/claude`, plus the setting's wiring.
  The loop never learns that warming exists.
- **D6** One unit, independent of the cache, adds a rule for every session: a
  session never finishes while a background command it needs is still running.

## What was measured (Claude Code 2.1.287, 2026-11-19)

These are facts a unit may rely on. A unit that finds one no longer true reports
the drift instead of working around it.

- `claude -p --input-format stream-json --output-format stream-json --verbose`
  reads its prompt as a line on stdin and keeps reading. It ends only once stdin
  is closed after a `result`.
- The prompt line is
  `{"type":"user","message":{"role":"user","content":"<prompt>"}}`.
- A side question sent while a foreground Bash call runs is answered within
  about a second. The call keeps running, and nothing enters the main
  conversation. The request is
  `{"type":"control_request","request_id":"<id>","request":{"subtype":"side_question","question":"Reply with exactly: OK"}}`.
- The side question's answer arrives as
  `{"type":"control_response","response":{"subtype":"success","request_id":"<id>","response":{"response":"OK","synthetic":false}}}`.
  A `system` line with subtype `control_request_progress` may come first.
  `"synthetic":true` means no answer was produced.
- With `CLAUDE_CODE_PROMPT_CACHE_TTL=5m`, a seven-minute foreground command
  without a ping cost a rewrite of the conversation's own context (9020 tokens
  written). With one ping at four minutes, the next request read 22031 and
  wrote 788.
- The ping itself read the full context (21879) and wrote 152. Its usage
  appears in no stream-json line, neither the `result` nor the
  `control_response`.
- `CLAUDE_CODE_PROMPT_CACHE_TTL` (`5m` or `1h`) takes precedence over the
  `promptCacheTtl` setting.
- A foreground Bash call never blocks longer than 10 minutes, whatever
  `timeout` the model asks for. At its timeout the command is moved to the
  background and the model gets the turn back.
  - The default timeout is 2 minutes.
  - `BASH_DEFAULT_TIMEOUT_MS` and `BASH_MAX_TIMEOUT_MS` change both limits.
- In `-p`, a background task is killed when the model ends its turn.
- In hero-rts, 400 ploopy sessions over 30 days had 302 pauses of more than
  four minutes between two main-thread requests.
  - All of them were foreground Bash calls.
  - None was longer than 10 minutes; the median was 8.5.
  - 89 ran into the 10-minute limit.

## The warming contract

The *clock* is when the main thread last sent a request, because a request
renews the cache. It is set when:

- the prompt is written;
- a main-thread `user` line carrying a `tool_result` is read, since Claude Code
  sends its next request right after;
- a ping is sent.

`assistant` lines do not set it: they arrive after their request went out.
Lines whose `parent_tool_use_id` is non-null belong to a subagent. They neither
set the clock nor count as main-thread calls. `tool_progress` lines carry their
own tool's id in that field and are not read for this.

The session is *waiting* while a main-thread tool call has no result yet.
Claude Code sends nothing until every result of a turn is in, so a waiting
session has no request in flight.

A waiting turn has a *deadline*, the latest deadline of its pending calls:

- **Foreground `Bash`** (`run_in_background` not true): seen +
  min(`timeout`, max). `timeout` defaults to 120 000 ms and max to 600 000 ms.
  `BASH_DEFAULT_TIMEOUT_MS` and `BASH_MAX_TIMEOUT_MS` in the session's
  environment replace those defaults when set.
- **`Monitor`**: seen + min(`timeout_ms`, 3 600 000 ms), with `timeout_ms`
  defaulting to 300 000.
- **Any other tool**, background Bash included: no deadline is known.

Ping when all hold:

1. warming is on (D1) and was not stopped for this session;
2. the session is waiting;
3. clock + 4 min has passed;
4. the deadline is unknown, or later than clock + 4 min 30 s;
5. fewer than 12 pings were sent since the last real request (the clock set by
   a prompt or a `tool_result`).

The cap in rule 5 is the break-even point. A ping costs about 0.1× the context
and a rewrite 1.25×, so after 12 pings writing anew would have been as cheap.
For a known deadline the rules give at most two pings, at 4 and 8 minutes.

Warming stops for the rest of the session, with a drift note naming why, when a
ping:

- gets an error `control_response`,
- gets a `synthetic` answer, or
- gets no `control_response` within 60 s.

A ping's estimated tokens are a cache read of the context the last main-thread
`assistant` line reported: its `input_tokens` + `cache_read_input_tokens` +
`cache_creation_input_tokens`. Its other counts are left at zero.

The constants are package-level values in the adapter so tests can shrink them:

| name | value |
|---|---|
| ping after | 4 min |
| deadline margin | 4 min 30 s |
| ping cap | 12 |
| answer wait | 60 s |
| cache TTL when warm | `5m` |

## Traps particular to this pass

- **`exec` and stdin.** If `cmd.Stdin` is not an `*os.File`, `exec.Cmd.Wait`
  waits for the goroutine that copies it. A writer left open then hangs
  `Proc.Wait` forever.
  - Give the child an `os.Pipe` read end, and close the parent's copy of it
    after start, so a write to a dead child fails instead of blocking.
  - Close the write end exactly once, under the same lock as every write: on
    `result`, on `Kill`, and when the output ends without a `result`.
- **The fake harnesses.** `dev/fakeagent` reads stdin to EOF for `claude`, which
  deadlocks against a stdin that stays open until `result`. The adapter tests'
  `fakeClaude` script never reads stdin. A warming test needs a script that
  reads its stdin a line at a time and answers `control_request`s.
- **Time in tests.** Shrink the constants to tens of milliseconds and keep the
  ratios between them, so a slow machine does not flip an outcome.

## Stage 1 — The prompt as a stream

### 1.1 Claude sessions take their prompt as a stream-json message

**Goal:** The Claude adapter starts `claude` with `--input-format stream-json`.
It writes the prompt as the first stdin line and keeps stdin open until the
session's `result`, so later units can write control requests to a running
session. Nothing else about a session changes.

**Start from:**
- `Start`, `session.read`, `session.result` and `session.Kill` in
  `internal/harness/claude/claude.go`;
- `harness.StartProc` in `internal/harness/proc.go`;
- `fakeClaude` and `TestTheSessionIsStartedTheWayAnUnattendedRunNeeds` in
  `internal/harness/claude/claude_test.go`;
- `readPrompt` and its use for `claude` in `dev/fakeagent/main.go`, with
  `dev/fakeagent/main_test.go`.

Read *Traps particular to this pass* first.

**Done when:**
- The argument test expects `--input-format stream-json`.
- A test reads the fake's first stdin line and finds the prompt as the user
  message in the measured format.
- Wait returns, without hanging, for three fakes:
  - one that prints a `result` and then waits for stdin to close;
  - one that exits without a `result`;
  - one killed mid-session.
- `dev/fakeagent` as `claude` reads only the first line under
  `--input-format stream-json`, and its tests pass.
- `go test ./...` passes.

## Stage 2 — Warming

### 2.1 Ping a waiting Claude session before its cache expires

Test: yes

#### Goal

The Claude adapter runs every session with `CLAUDE_CODE_PROMPT_CACHE_TTL=5m`
and sends pings by *The warming contract*. It reports how many it sent and
their estimated tokens on the outcome.

#### Concrete implementation work

- Start the process through `harness.StartProcWithEnv` with the TTL variable.
- Keep the clock, the pending main-thread calls with their deadlines, the ping
  count since the last real request, and the last main-thread context size.
  Update them in `session.read`.
  - Lines today decode through `streamLine` and `assistantMessage`. The
    `parent_tool_use_id` and the `assistant` message's `usage` must be read.
  - `control_response` lines are dropped today by the type switch in `read` and
    must be read.
- A timer goroutine decides when to ping and writes the request through the
  stdin writer from 1.1. Requests are numbered `ploopy-warm-<n>`. It stops when
  the session ends.
- Add two fields to `harness.Outcome`, both zero when no ping was sent:
  - `Pings int`;
  - `PingTokens harness.Tokens`, the summed estimates.
- A stop of warming goes through `s.drift`, so it appears in
  `Outcome.Degraded`.

#### Explicit non-goals

- No setting yet; that is 2.2.
- Pings are not booked in stats or history; that is 2.3.
- No UI event, and no change outside `internal/harness`.

#### Tests

Use a fake `claude` that reads its stdin line by line.
- It prints a foreground Bash `tool_use` with a `timeout` long enough to need a
  ping, answers the ping, then prints the `tool_result` and the `result`. The
  outcome has one ping and its estimate.
- The same fake with a short default-timeout call sends no ping.
- A tool with no known deadline is pinged up to the cap and no further.
- An error answer and a missing answer each stop warming and leave a drift note.
- A subagent's `tool_result` (non-null `parent_tool_use_id`) does not reset the
  clock.
- The child's environment has `CLAUDE_CODE_PROMPT_CACHE_TTL=5m`.

#### Completion criteria

The tests above exist and `go test ./...` passes.

### 2.2 `[agents.claude] cache` opts a repository out of warming

**Goal:** `.ploopy.toml` accepts `cache` under `[agents.claude]` with `"warm"`
(the default) or `"1h"`. `"1h"` runs Claude sessions with
`CLAUDE_CODE_PROMPT_CACHE_TTL=1h` and sends no pings. Any other value, or
`cache` under another agent, is a load error naming the key. The documentation
of agent settings describes the key.

**Start from:**
- `Agent` and `Load` in `internal/config/config.go`, with its tests;
- `allHarnesses` in `cmd/ploopy/main.go`, where agent settings reach the
  harnesses;
- the `Harness` struct and `Start` in `internal/harness/claude/claude.go`;
- the `[agents.claude]` paragraphs in `README.md` and `skills/ploopy/SKILL.md`.

**Done when:**
- The config tests cover the two values, a bad value and `cache` under
  `[agents.codex]`.
- An adapter test shows that `"1h"` sets the hour and sends no ping where
  `"warm"` would.
- `rg -n 'cache' README.md skills/ploopy/SKILL.md` shows the key documented.
- `go test ./...` passes.

### 2.3 Book the pings in stats, history and the report

**Goal:** A session's pings and their estimated tokens reach the stats log and
the history. The plan report shows them apart from the session's reported
usage, marked as estimated. The cache share the report already prints stays
computed from reported usage only.

**Start from:**
- `accountSession` in `internal/loop/stats.go`;
- `stats.Record` in `internal/stats/stats.go`;
- `history.Record` in `internal/history`;
- the token lines of the report in `internal/stats/report.go`, near the
  `"%d%% from cache"` line.

New fields are additive and omitted when zero, like the others there.

**Done when:**
- A loop or stats test with a fake outcome carrying pings finds them in the
  record and in the report text.
- A record without pings serializes exactly as before.
- `go test ./...` passes.

## Stage 3 — Background work, and closing the pass

### 3.1 A session never finishes while its background command runs

**Goal:** Every session that runs commands is told: a command it started in the
background stops when its session ends. It must not finish while one whose
result it needs is still running. It waits for it, in steps if need be, and
reads the result before it commits or reports.

The wording is harness-neutral, since ploopy ends every session's process group
whatever the harness. Per `AGENTS.md`, a session's contract lives in the skill
that describes it.

**Start from:**
- the *Tests* section of `skills/plan-unit/SKILL.md`;
- `skills/land-repair/SKILL.md`.

Read every other skill under `skills/` a session receives (`unit-rescue`,
`review-direct`, `plan-review`, `review-triage`). Add the rule where that
session runs commands.

**Done when:**
- `rg -n -i 'background' skills/plan-unit/SKILL.md skills/land-repair/SKILL.md`
  shows the rule.
- Any embedded copy or test that pins those skills' text still passes:
  `go test ./...`.

### 3.2 Describe warming where it stays true

**Goal:** `README.md` and `skills/ploopy/SKILL.md` describe, briefly:
- that Claude sessions keep a five-minute cache warm by default;
- what a ping is and when one is sent;
- that `cache = "1h"` opts out;
- that the report's ping figures are estimates.

Nothing in either still describes Claude's prompt going on stdin as plain text.

**Start from:**
- the agent and report sections of `README.md`;
- the `[agents.claude]` paragraph of `skills/ploopy/SKILL.md`;
- this plan's preamble, which holds the facts.

**Done when:** `rg -n -i 'warm' README.md skills/ploopy/SKILL.md` finds the
description, and `go build -o /dev/null ./...` passes. The plan and its
`.state.json` stay.
