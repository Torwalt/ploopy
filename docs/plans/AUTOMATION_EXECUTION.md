---
test: yes
---

# Automation: execution

Status: active task document, written 2026-10-23 against `fea1f08`. The
authority is `AUTOMATION.md` beside this file; units cite its sections as §N.

Every unit keeps behaviour unchanged where its new setting is unset. The tests
in `internal/loop` are the specification of the judge: add tests, and change an
existing one only where §4 renames `then` or §7 changes what a run that stops on
a unit leaves in the state file.

## Stage 1 — Every run setting in `.ploopy.toml`

### 1.1 `[run]` and `[review]` tables

**Goal:** A repository can set every run and triage setting in `.ploopy.toml`,
per §3: the `[run]` and `[review]` tables, parsed and validated, and read with
the precedence flag > front matter > file > last run > default. A value from
the file prefills the board without locking it, and the board says "from
.ploopy.toml". `unattended` counts a harness and an effort from the file as
given. The old top-level `push` still works.

**Start from:** `config.Config` and `Load` in `internal/config/config.go`;
`givenSettings`, `fill`, `unattended` and the `given` sources in
`cmd/ploopy/settings.go`; `chooseTriage` in `cmd/ploopy/triage.go`; the board
in `cmd/ploopy/board.go`, where it shows a locked field's source; `env.cfg`.

**Non-goals:** `[automation]`, follow-up and rescue (later units). No new
settings: only mirrors of what flags already set.

**Done when:** `internal/config` tests cover parsing and every validation error
of the new keys. `cmd/ploopy` tests show that a file value beats the last run,
that a flag beats a file value, and that `ploopy run --plan X` with no terminal
starts from `[run] agent`/`effort` alone. `[run] push` beats a top-level `push`.
The README's settings section documents both tables.

## Stage 2 — Follow-up

### 2.1 `then` becomes the follow-up setting

**Goal:** The follow-up setting replaces `then` everywhere, per §4. It is
`runSettings.FollowUp`, `chosenRun.followUp`, `control.Settings.FollowUp`,
`--follow-up` on `run`, `review` and `adjust`, `[automation] follow_up`, and
`followUp` in `last-run.json`. `--then` stays as a deprecated alias. The board
shows the field under an Automation heading. In this unit, follow-up on an
ordinary plan has no effect yet. On a review it runs the fix plan, as `--then
fixes` does today.

**Start from:** `rg -n '\bthen\b|Then\b|thenValue|validThen' cmd/ploopy
internal/control`. Also `chainedFixPlan` in `cmd/ploopy/run.go`, the `then`
field of the board in `cmd/ploopy/board.go`, and the flags of `launch.go` and
`adjust.go`.

**Non-goals:** Reviewing an ordinary plan (2.2). Do not touch the rescue.

**Done when:** `rg -n 'thenValue|validThen' cmd internal` comes back empty.
The tests in `cmd/ploopy/review_chain_test.go` pass with `--follow-up`, and one
of them uses `--then fixes`. `ploopy adjust --follow-up=false` stops a chain
before its fix plan, and a test shows it. `control.Settings.Describe` says
"then run the fix plan" on a review.

### 2.2 A finished plan goes on to its review and fix plan

**Goal:** With follow-up on, a plan that completes goes on, in the same
process, to its review and then the fix plan, per §5. A fix plan never chains
into a review. The review is written through the same code as `ploopy review`,
with the triage from `--triage-*`, then `[review]`, then the last triage, then
the default. `Describe` and the run's messages say what comes next.

**Start from:** `runPlan`, `chainedFixPlan` and `runFixPlan` in
`cmd/ploopy/run.go`; `reviewPlan`, `writeReview` and `reviewTriage` in
`cmd/ploopy/review.go`; `addTriageFlags`; `review.Family`, `review.Path` and
`review.FixesPath` in `internal/review/review.go`. The existing chain tests in
`cmd/ploopy/review_chain_test.go` show how a chain is driven with the fake
harness.

**Non-goals:** No new process or tmux window (D2). No change to the review's
own board.

**Done when:** Tests in `cmd/ploopy` show these four cases. A plan with
follow-up on runs plan → review → fix plan, then pushes and takes its end
action once, after the fix plan. With follow-up off, the plan stops after
itself. A plan stopped early (`--until`, a block or `adjust --stop`) does not
go on to its review. A fix plan that completes with follow-up on does not start
a review. The `skills/ploopy` skill describes the follow-up.

## Stage 3 — Rescue

### 3.1 The rescue session in the loop

Context: internal/loop/review.go internal/loop/triage_prompt.md

**Goal:** With `loop.Options.Rescue` set, a unit that reaches one of the three
triggers of §6 is handed to one rescue session first. The session gets the
prompt of §7 and the new `unit-rescue` skill. The loop holds it to read-only,
reads its marker and report, and acts on `RETRY`, `DECIDED` or `BLOCKED` per
§7, within `Options.Rescues` (default 2). `harness.ParseMarker` learns `RETRY`
and `DECIDED`, which mean nothing outside a rescue. The session's records in
`stats.jsonl` and `history.jsonl` carry `role: "rescue"`, and estimates leave
them out. With `Options.Rescue` nil, nothing changes.

**Start from:** `runUnit`, `failedAttempt`, `blockedAttempt`, `touched` and
`session` in `internal/loop/loop.go`; `judgeCollect` in
`internal/loop/review.go` for a read-only judge; `account` in
`internal/loop/stats.go`; `markerRe` and `ParseMarker` in
`internal/harness/harness.go`; `embed.go` and `cmd/ploopy/skills.go` for how a
skill ships and is overridden (`config.RepoTriageSkill`); `history.Record` and
`internal/history/estimate.go`.

**Non-goals:** The `Rescues` field in the state file, the report section and
the end-of-run lines (3.2). Flags, the board, `.ploopy.toml` and `adjust`
(3.3): set `Options.Rescue` directly in tests.

**Done when:** Fake-harness tests in `internal/loop` cover these cases:

- `RETRY` after spent retries lands the unit on the next attempt, and that
  attempt's prompt holds the *Next attempt* text;
- `DECIDED` after a confirmed block lands the unit, and the prompts after it
  hold the decisions;
- `BLOCKED` stops the run, and the unit is settled blocked with the rescue's
  reason;
- the transient trigger;
- the bound of 2;
- a rescue that commits, or leaves the tree dirty, fails the rescue and stops
  the run;
- a rescue with no marker stops the run;
- `Wait:` is honoured, and capped at 2h;
- with no rescue, the run behaves as before.

A unit session that ends with `RETRY` is judged as having no marker.
`skills/unit-rescue/SKILL.md` says how to tell a provider failure from a real
blocker. It names the signals of §7 and the provider failures listed in §1
(empty completion, a stall, a kill after the commit).

### 3.2 Rescues on record

**Goal:** Every rescue is recorded per §9. It is on the unit's state entry as
`rescues`, with its cost counted in the unit's `cost_usd`. It is in
`ploopy report` under *Rescued*, with a `decided` rescue's decisions in full.
It is in the run's closing summary, one line per rescue, with the decisions
under `decided` lines and a pointer to `ploopy report`.

**Start from:** `state.Entry` in `internal/state/state.go` and its frozen
format (fields are additive and omitted when empty); `entry` and `settle` in
`internal/loop/loop.go`; `progress` in `internal/loop/progress.go`; `trouble`
in `internal/stats/report.go`; the run's closing summary in
`internal/ui/summary.go`.

**Non-goals:** Settings and flags (3.3).

**Done when:** A state-file round-trip test with `rescues` exists, and an old
state file loads unchanged. A loop test shows a `decided` rescue on the landed
entry, with its report text. A report test shows the *Rescued* section. A
summary test shows the closing lines.

### 3.3 Choosing the rescue

**Goal:** The author chooses the rescue agent per §8. On the board it is in the
Automation part, beside follow-up. As flags it is `--rescue`,
`--rescue-model` and `--rescue-effort` on `run`, `review` and `adjust`. In the
file it is `[automation] rescue`, `rescue_model` and `rescue_effort`. It is
also remembered in `last-run.json`, and passed on to a detached run. A run in
progress takes a change through `ploopy adjust`, which the loop reads at the
next stop point. A chained review and fix plan keep it.

**Start from:** How the secondary agent goes through each of these:
`--secondary` in `cmd/ploopy/run.go` and `adjust.go`, `runSettings.Secondary`,
`control.Settings.Secondary`, `steer()` in `internal/loop/loop.go`,
`overChosen`, and the `limit` field in `cmd/ploopy/board.go`.

**Non-goals:** Rescue for `land` repair sessions.

**Done when:** Tests show that a rescue from the file, from a flag or from the
last run reaches `loop.Options.Rescue`. `adjust --rescue none` turns it off for
the rest of a running run. A detached launch passes the rescue on.
`Describe` names the rescue.

## Stage 4 — Documents

### 4.1 Skills and README

Test: no

**Goal:** The shipped documents describe the pass as it now stands.
`skills/ploopy/SKILL.md` covers the follow-up, the rescue, what a `DECIDED`
rescue means for the author, and where its record is. It also covers the
`.ploopy.toml` tables, and that a detached run needs no flags beyond `--plan`
once `[run]` names an agent and an effort. The README's settings and
run-lifecycle sections match. `AGENTS.md`'s package boundaries mention the
rescue under `internal/loop`, and the new skill under `skills/`.

**Start from:** `skills/ploopy/SKILL.md`, `README.md`, `AGENTS.md`, and
`AUTOMATION.md` for what was decided.

**Done when:** `rg -n -- '--then' README.md skills` names it only as a
deprecated alias. The skill and README give a `[run]`/`[automation]` example.
