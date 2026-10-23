# Automation: follow-up, rescue, and every run setting in `.ploopy.toml`

Status: authority for `AUTOMATION_EXECUTION.md`, written 2026-10-23 against `fea1f08`.

A plan should run overnight and heal itself. Today a unit that fails its
retries, or blocks twice, stops the run, and the author spends the morning
asking an agent why. Most of those stops are not real blockers: a provider
returned an empty completion, a request stalled until the wall clock killed a
session whose work was already committed, or the block was a decision the
agent could have made. And once a plan finishes, its review and fix plan wait
for the author to start them.

This pass adds an *Automation* part to a run, with two settings:

- **follow-up**: once a plan completes, review it and run the fix plan the
  review's triage writes, in the same invocation.
- **rescue**: where the run would stop on a unit, a stronger agent diagnoses
  why and decides whether to retry, to make the decision the unit was blocked
  on, or to stop for the author.

Every run setting, these included, gets a mirror in `.ploopy.toml`, so a
repository is configured once and then runs unattended.

## 1. Decisions settled with the author (2026-10-23)

- **D1** The follow-up is one setting, on or off. On an ordinary plan it means
  review, then the fix plan. On a review it means the fix plan, as
  `--then fixes` does today. It replaces `then` (§4).
- **D2** The follow-up chains inside the same `ploopy` process, the way
  `runPlan` in `cmd/ploopy/run.go` already chains a review into its fix plan.
  It does not start a child process or a new tmux window. Push, worktree
  handling and the end action happen once, after the last plan of the chain.
- **D3** A fix plan is never reviewed automatically. One round per run.
- **D4** The rescue is diagnose-only. It reads and decides. It never edits,
  commits or lands anything. git still judges every unit.
- **D5** The rescue runs at the three places where a run stops on a unit
  today, including a block the second opinion confirmed (§6).
- **D6** A rescue answers `RETRY`, `DECIDED` or `BLOCKED` (§7). `DECIDED`
  lets it make the decision that the block was waiting on, standing in for the
  author.
- **D7** Every rescue is recorded where the author reads in the morning: the
  unit's entry in the state file, `ploopy report`, and the summary at the end of
  the run (§9). The author reviews what a `DECIDED` rescue chose and may
  revisit it.
- **D8** Every setting a run is started with has a `.ploopy.toml` key (§3).
- **D9** Names: the settings section is "Automation", and the agent is the
  "rescue".

## 2. Vocabulary

- **chain**: the plans one invocation runs in order: plan, review, fix plan.
  `runPlan`'s loop over `runOnce`.
- **rescue**: one diagnosis session for one unit, on the rescue agent.
- **trigger**: the reason a rescue was started (§6).
- **stop point**: a place in `runUnit` (`internal/loop/loop.go`) that today
  returns an error or settles a block, ending the run.

## 3. Settings in `.ploopy.toml`

`config.Config` (`internal/config/config.go`) gains three tables. Every key is
optional, and unknown keys are still an error.

```toml
[run]
agent = "opencode"                       # --agent
model = "opencode-go/deepseek-v4.1-flash"
effort = "high"
secondary = "claude"                     # or "none"
secondary_model = "sonnet"
secondary_effort = "high"
peak = "wait"                            # wait | run
subscriptions = "all"                    # all | none | names
spend_all = false
push = true
finish = "none"                          # none | suspend | poweroff

[review]
triage_agent = "claude"
triage_model = "opus"
triage_effort = "high"
triage_budget = 0.0

[automation]
follow_up = true
rescue = "claude"                        # or "none"
rescue_model = "opus"
rescue_effort = "high"
```

- **Precedence.** A flag wins. Then the plan's front matter, for the keys it
  can carry today. Then `.ploopy.toml`. Then the last run (`last-run.json`, or
  `last-triage` for the triage). Then the default. A value from the file
  prefills the board and can be changed there. It is not locked: the board
  locks only what flags and front matter gave. The board shows where a value
  came from, as it already does for flags and the plan, as "from
  .ploopy.toml".
- **Unattended.** In `unattended` (`cmd/ploopy/settings.go`), a harness and an
  effort from the file count as given. A repository with `[run] agent` and
  `effort` can be run with `ploopy run --plan X --detach` and nothing else.
- **Validation** happens where the flag is validated today, and with the same
  messages, naming the key instead of the flag: an unknown harness, an effort
  that harness does not offer, a bad `peak`/`finish`, a subscription set
  `subscriptionSet` rejects.
- **The old top-level `push`** stays readable. `[run] push` wins when both are
  given. The README documents only `[run] push`.
- Values are read in `cmd/ploopy` and passed on. Nothing under `internal/loop`
  reads `.ploopy.toml`.

## 4. The follow-up setting

- **Where it lives.** `runSettings.FollowUp bool` and `chosenRun.followUp`,
  replacing `Then`/`then`. The flag is `--follow-up` (a bool, on `run`,
  `review` and `adjust`). `--then` stays as a deprecated alias: `fixes` means
  on, `stop` means off. The key is `[automation] follow_up`. The default is
  off.
- **Runtime control.** In `control.Settings`, `FollowUp *bool` replaces
  `Then`. `Describe` says "then review and fix" on an ordinary plan and "then
  run the fix plan" on a review. `ploopy adjust --follow-up[=false]` changes it
  during a run. `last-run.json` stores `followUp`. An old `then` field is
  ignored.
- **The board** shows the field on every run, in the Automation part, beside
  the rescue fields (§8). `launch.go` passes `--follow-up` to a detached run
  instead of `--then`.

## 5. The chain

After `runOnce` returns, `runPlan` asks for the next plan of the chain:

1. The last plan is ordinary: not a review (`p.Reviews() == ""`) and not a
   fix plan (`review.Family` and the `_FIXES` naming in `internal/review`
   say which). It completed (`planRun.complete`), was not stopped, and
   follow-up is on. Then it writes the review and runs it. If no unit of the
   plan landed with commits, there is nothing to review: say so and end the
   chain.
2. The last plan is a review that completed, follow-up is on, and the triage
   wrote `<PLAN>_FIXES.md`. Then it runs the fix plan, as `chainedFixPlan`
   does today.
3. Otherwise the chain ends. A fix plan always ends it (D3).

Writing the review in the chain:

- Uses the same code path as `ploopy review` (`writeReview` in
  `cmd/ploopy/review.go`), extracted so that no prompt and no board is
  needed. The review plan is written and committed on the plan's branch, in
  the run's worktree.
- The refusal "is running" (`c.Live`) does not apply: the chain is that
  process. The refusals for a fix plan or review left over from an earlier
  round still apply. They end the chain with the reason. They do not fail the
  run.
- The collecting sessions inherit the run's settings as adjusted
  (`overChosen`), then the review plan's own front matter
  (`applyPlanSettings`), as a fix plan does today.
- The triage comes from `[review]` in `.ploopy.toml`, then the last triage,
  then the default (`defaultTriage`). `ploopy run` accepts the `--triage-*`
  flags that `ploopy review` has, and they win.
- The run says, when it starts and when it moves on, which plan comes next:
  "PLAN complete; reviewing it (triage claude opus high)".

## 6. When a rescue runs

The rescue agent is `loop.Options.Rescue *Agent`. When it is nil, `runUnit`
behaves exactly as at `fea1f08`, and every existing test passes unchanged.

There are three triggers, each at a stop point of `runUnit`:

| Trigger | Today | Name in records |
|---|---|---|
| retries spent | `failedAttempt` returns "unit X failed after N attempt(s)" | `failed` |
| transient limit | "N sessions in a row ended without touching the repository" | `transient` |
| block confirmed | `Blocked` verdict with `blocks >= BlockRetries`, before `settle` | `blocked` |

A rescue never runs on:

- a fatal verdict;
- an exhausted budget;
- usage-limit waits running out;
- a session that could not start;
- a cancelled run;
- an error from git or the filesystem.

These stay stop points.

**Bound.** At most `Options.Rescues` rescues per unit per run, 2 by default.
Once those are spent, the next stop point stops the run as today. The run
counts this, so nothing in `progress.json` changes for it.

**Agent and limits.** The rescue runs on its own agent, regardless of leases,
the secondary or the off-peak setting. If the rescue session hits a usage limit, fails
to start, ends with no marker, or touches the repository (§7), the rescue has
failed. The run stops as it would have without a rescue. The record says why
the rescue failed.

## 7. The rescue session

**Prompt.** The template is `internal/loop/rescue_prompt.md`, embedded like
`triage_prompt.md`, with the skill `skills/unit-rescue/SKILL.md`. That skill
is embedded in `embed.go` and written by `ploopy skills`. A repository may
override it at `.agents/skills/unit-rescue/SKILL.md`, like the other skills.
The prompt gives:

- the unit's work order, as `ploopy show` renders it, and the plan's path;
- the trigger, and every attempt of this unit in this run: the agent, the
  verdict, the failure reason (the `failure` texts the loop built), the exit
  status, whether it timed out, the marker, tokens, the degradation notes, and
  the path of its session log under `.ploopy/<plan>/logs/`;
- for the latest attempt, the last 60 lines of its session log;
- the repository: the unit's base, `git log --oneline base..HEAD`, the dirty
  paths, the handover if there is one, and the paths of the last verify and
  test logs;
- for a block: the reason each blocking session gave, and the handover of the
  second opinion;
- where to write its report, and the markers.

**Read-only.** Before the session, the loop takes the head and the dirt, and
afterwards compares with `touched`. Any change fails the rescue (§6). The
loop never reverts what the rescue changed: the change stays for the author,
and the record names it.

**Report.** The rescue writes `<handovers>/<unit>.rescue-<n>.md`, outside the
index, in these sections:

```
## Diagnosis
<what happened, with the evidence: log lines, commits, check results>

## Decisions
<DECIDED only: each decision, the options, why this one>

## Next attempt
<what the next session must know, addressed to it>

Wait: 30m          # optional, RETRY only; at most 2h
```

**Markers.** The last line of its final message is one of:

- `RETRY <one-line reason>`: the cause lies outside the unit's work order, for
  example a provider failure, a stall, a kill after the work was done, a flaky
  environment or a check that failed for an unrelated reason.
- `DECIDED <one-line summary>`: the unit stopped on a question the author
  would answer, such as a design choice or an ambiguity in the work order, and
  the rescue answered it from the repository's documents and code.
- `BLOCKED <one-line reason>`: only the author can unblock this, for example
  missing credentials, a contradiction in the plan, or a decision with a cost
  the documents do not settle.

`harness.ParseMarker` learns `RETRY` and `DECIDED`. A unit, review or triage
session that ends with either is judged as one with no marker. Only the rescue
gives them meaning.

**What the loop does with the answer.**

- `RETRY`: wait for the `Wait:` duration if one is given, else for
  `Options.Backoff`, then pass the peak gate. Reset the unit's attempt,
  transient and block counters. The next attempt's `failure` text is the
  previous failure text, followed by "A rescue diagnosed this:" and the
  report's *Next attempt* section.
- `DECIDED`: reset the counters, and start the next attempt at once. Its
  `failure` text says that the author's stand-in decided, and gives the
  *Decisions* and *Next attempt* sections. The text says the decisions are
  settled: do not reopen them, and record them in the handover. Every later
  attempt of the unit in this run keeps the decisions (`progress.Failure`
  carries them), so a later retry does not lose them.
- `BLOCKED`: the unit settles as `blocked`, with the rescue's reason, and the
  run stops. This also applies to the `failed` and `transient` triggers,
  which at `fea1f08` leave no state entry. Now the author finds the unit
  blocked in `ploopy status`, with the rescue's reason.
- A failed rescue (§6): the run stops as it would have without a rescue. For
  the `blocked` trigger, the unit settles blocked as today.

## 8. The rescue setting

- `loop.Options.Rescue *Agent` and `Options.Rescues int`.
- `chosenRun.rescue *loop.Agent` and `runSettings.Rescue *control.Agent`, in
  the shape of the secondary. Flags on `run`, `review` and `adjust`:
  `--rescue <harness|none>`, `--rescue-model`, `--rescue-effort`. The
  `.ploopy.toml` keys are under `[automation]` (§3). The default is none.
- `control.Settings.Rescue *Agent` (an empty harness means none). The loop
  reads it through `steer()` when a rescue is due, as it reads the secondary,
  so `ploopy adjust --rescue claude --rescue-model opus` takes effect at the
  next stop point.
- The board's Automation part has the follow-up field and the rescue's agent,
  model and effort. The rescue can be any harness, the run's own included.
- A chained review and fix plan keep the rescue (`overChosen`). The triage and
  the collecting units are rescued like any other unit.
- `land` repair sessions get no rescue in this pass.

## 9. Records

- **State.** `state.Entry` gains `Rescues []Rescue` (omitted when empty).
  `Rescue` has `trigger`, `verdict` (`retry`, `decided`, `blocked` or
  `failed`), `agent` (a label), `reason` (its marker line), `report` (the
  report file's text, cut at 4000 bytes), `date`, and `cost_usd`. Rescues
  collect in `progress` while the unit runs, and go onto the entry when the
  unit settles, landed or blocked. A rescue's cost is part of the unit's
  `cost_usd`. Its time is not part of `session_s`.
- **Stats and history.** The session's record in `stats.jsonl` and in
  `history.jsonl` gets `role: "rescue"`. Unit sessions omit the field.
  Estimates (`internal/history/estimate.go`) leave rescue sessions out.
- **Report.** `ploopy report` gets a section, *Rescued*, after *What went
  wrong*. Per rescue it shows the unit, the trigger, the verdict, the reason,
  and for `decided` the *Decisions* section in full. Since `land` stamps the
  report into the closing commit, this record reaches the default branch.
- **End of the run.** The run's closing summary lists every rescue of the run,
  one line each. Under each `decided` line it shows the decisions. It ends
  with "review these: ploopy report <plan>".
- **Live.** The run says when a rescue starts and what it answered, like a
  second opinion today: `record("rescue", …)` and `report.Say`.

## 10. Package placement

- `internal/config`: the three tables and their validation.
- `cmd/ploopy`: the precedence, the board, the flags, `adjust`, the chain.
- `internal/control`: `FollowUp` and `Rescue` in `Settings`.
- `internal/loop`: the triggers, the rescue session, its prompt and its judge,
  the counters, `progress` rescues.
- `internal/harness`: the two markers.
- `internal/state`, `internal/stats`, `internal/history`: the record fields
  and the report section.
- `internal/ui`: the end-of-run lines.
- `skills/unit-rescue`, `skills/ploopy`: the new skill, and the lifecycle text
  for follow-up and rescue.
