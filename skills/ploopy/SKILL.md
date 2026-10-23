---
name: ploopy
description: Work with ploopy in this repository — where plans live and how they move from written to landed, reading progress, starting a run detached from the agent session, the worktrees ploopy makes and how they are set up, repository settings, automation with follow-up and rescue, reviewing, closing and landing. Use whenever the author mentions ploopy, a plan's progress, starting, stopping or landing a plan, or a worktree for a plan.
---

# Working with ploopy

ploopy runs a plan one unit per fresh agent session and judges from git whether
each unit landed. It is a supervisor: it owns the plan's lifecycle, its branch,
its worktree and its progress. Ask it rather than reconstructing any of that
from git by hand.

## A plan's lifecycle

1. **Written.** A plan is a Markdown file in the plans directory (`docs/plans/`
   unless `.ploopy.toml` says otherwise), written with the `plan-writing`
   skill and committed on the default branch in the main checkout.
2. **Started.** `ploopy run` commits the plan there in its latest form if it is
   not yet, makes a branch named after it (`PLAYTEST_FIXES` →
   `playtest-fixes`) and a worktree for that branch at
   `<repo>.worktrees/<branch>`, then runs the units there. The main checkout
   must be on the default branch and stays the author's.
3. **In progress.** Progress is `<PLAN>.state.json` beside the plan, on the
   plan's branch. A review (`<PLAN>_REVIEW.md`) and the fix plan its triage
   writes (`<PLAN>_FIXES.md`) live on the same branch and belong to the plan.
4. **Landed.** `ploopy land <branch>` rebases the branch onto the default
   branch, runs the checks, closes the plan and its reviews and fix plans with
   their reports as commit bodies, fast-forwards the default branch, and removes
   the worktree and the branch. Pushing the default branch is the author's.

The plan on the default branch is the plan's identity: a plan exists while the
default branch holds it. The copy on its branch is the one that runs, and may
differ. Once a plan is gone from the default branch, any branch, backup or
worktree that still holds a copy is ignored.

## Reading where things stand

- `ploopy status` lists every open plan with the progress of the branch that got
  furthest, and where that is (`· pass in ../repo.worktrees/pass`). `ploopy
  status <plan>` lists its units.
- `ploopy show <plan> <unit>` prints a unit's work order as a session sees it.
- `ploopy report <plan>` and `ploopy history` say what a plan or a unit took.
- `ploopy replay <plan> <unit>` says what a unit's session did.
- `ploopy lint` checks that plans parse the way the loop needs.

Never write a `.state.json` by hand. `ploopy mark <plan> <unit>
landed|skipped|pending` records a unit's status when the author asks for it.

## Starting a run

A run lasts hours and must outlive the agent session that starts it. Never run
`ploopy run` through your own shell tool, in the foreground or the background,
except with `--detach` or with `launch = "tmux"` in `.ploopy.toml`: then ploopy
starts the run in a tmux window of the current session, `ploopy-<PLAN>`, and
returns at once. The tmux server owns the run, so it survives your session.
Without tmux, ask the author to start it.

```sh
ploopy run --detach --plan <plan> --agent <agent> <flags>
```

Nobody answers a question in a detached window, so pass every choice as a flag
or set it in `.ploopy.toml`: `--plan` is required, and `--agent` unless
`[run]` names an agent and an effort. Add whatever the author asked for
(`--model`, `--effort`, `--units`, `--until`, `--finish`, `--push`, …; `ploopy
run --help` lists them). If the author did not say which agent and the
repository does not set one, ask before starting. Run `ploopy lint` and
`ploopy status` first. Start it from the main checkout:
ploopy finds or makes the worktree itself. `ploopy review <plan> --detach`
starts a review the same way. A plan, its reviews and its fix plans share one
window; a window busy with something else is refused.

Confirm it started with `ploopy status`. Do not stay to watch the run; its
progress lives in `<PLAN>.state.json` and in `ploopy status`. Never close a
run's window: the author closes them.

`ploopy adjust` changes a run going on, and `ploopy adjust --stop` stops it
after the current unit. Never kill a run's process or window.

## Models and subscriptions

The default picker offers Claude Sonnet and Opus, Codex `gpt-6.1-sol`, and
OpenCode Go `opencode-go/deepseek-v4.1-flash`, all at high effort. Repository
settings can replace a harness's lists with `[agents.claude]`, `[agents.codex]`
or `[agents.opencode]`, each with `models = [...]` and `efforts = [...]`.
Use the installed harness's model IDs; `opencode models` lists its IDs.
Explicit plan and flag settings can still name other supported choices.

DeepSeek Flash uses Go first. A reported usage limit hands the unit's existing
work to a fresh session on `deepseek/deepseek-flash`, without spending an
attempt or changing harness. Each session records its actual route. The next
session tries Go again after the reported reset, or after 30 minutes if the
CLI supplies no reset. Review and landing repair sessions use the same policy.
Go counts peak hours double, so Go and the API both follow the run's peak
setting. Auth and
other failures do not switch to the API.

`[agents.opencode.fallbacks]` maps a primary model ID to its fallback ID. An
explicit table replaces the defaults; `fallbacks = {}` under
`[agents.opencode]` disables automatic API fallback. Fallbacks cannot chain.
Provider configuration and authentication belong to the harness.

Claude and Codex still run only as selected or lent, keep their existing
reserves, and have no API fallback by default. Existing leases retain their
explicit model and effort.

## Automation

`follow_up`, or `--follow-up` on a run, reviews an ordinary plan that completes
and then runs the fix plan its triage writes, in the same invocation; on a
review it runs that fix plan. A fix plan is never reviewed. A run limited by
`--until` or `--units`, or one that failed, does not go on to the next plan of
the chain. `--then fixes` is its deprecated alias.

`rescue`, `--rescue <harness|none>`, with `rescue_model` and `rescue_effort`,
names a stronger agent for the three places a run would otherwise stop on a
unit: its retries spent, sessions in a row that touch nothing, or a block the
second opinion confirmed. The rescue is read-only: it diagnoses, and it never
edits, commits or lands. At most two rescues run per unit per run. It answers:

- `RETRY` when the cause was outside the unit's work order, such as a provider
  failure or a stall. The unit runs again, told what was diagnosed.
- `DECIDED` when the unit stopped on a question the author would answer. The
  rescue decided it, and the next attempt keeps the decisions and records them
  in its handover.
- `BLOCKED` when only the author can unblock it. The unit is recorded blocked
  and the run stops.

A `DECIDED` rescue stood in for the author. Review its decisions before
landing: they are on the unit in `<PLAN>.state.json`, under *Rescued* in
`ploopy report <plan>` with the decisions in full, and in the run's closing
summary. Revisit one if the documents do not support it.

## Repository settings

`.ploopy.toml` at the repository root configures a repository once. Every key
is optional and unknown keys are refused. A flag wins over the plan's front
matter, then the file, then the last run, then the default. A value from the
file prefills the board and can still be changed there; the board says where a
prefilled value came from.

```toml
[run]
agent = "opencode"
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

With `[run] agent` and `effort` set, a detached run needs no flags beyond
`--plan`: `ploopy run --plan <plan> --detach`. `[run] push` is the documented
push key; the old top-level `push` still works, and `[run] push` wins when both
are given.

## Worktrees

ploopy makes the worktree a plan runs in. Do not make one for a plan yourself,
and do not check a plan's branch out anywhere else: git allows a branch in one
checkout only.

A fresh worktree has none of what git does not carry: build outputs, installed
dependencies, local settings, an approved `.envrc`. The `setup` command in
`.ploopy.toml` readies it once, from inside it, before its first check. It sees
the main checkout as `$PLOOPY_MAIN`, to copy from, and may change only ignored
files. When a repository needs more than one line, `setup` runs a script it
keeps. To work in a plan's worktree by hand, go to the path `ploopy status`
names.

## Reviewing, closing, landing

- `ploopy review <plan>` writes and runs the review of a plan's landed units,
  then a triage that writes the fix plan. With follow-up on, that fix plan runs
  in the same invocation.
- `ploopy land [branch]` is how finished work reaches the default branch. It
  refuses while a plan of the branch has open units.
- `ploopy close <plan>` drops a plan from the default branch without landing
  it, leaving its branch alone; on a review or fix plan it closes that one on
  its branch.

Landing and closing change the default branch: do either only when the author
asks.
