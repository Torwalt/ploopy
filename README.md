# ploopy

Runs a task document one unit per fresh coding-agent session. The agent
implements and commits; ploopy decides from the repository whether the unit
landed, retries or stops, and records progress in `<PLAN>.state.json` beside
the plan.

A higher-intelligence agent writes the plan with the `plan-writing` skill. A
cheaper one executes it, one unit at a time, with no memory of the last.

## Install

    nix run github:Torwalt/ploopy

Or as a flake input:

```nix
inputs.ploopy.url = "github:Torwalt/ploopy";
# then: inputs.ploopy.packages.${system}.ploopy
```

`nix develop` gives a shell with the Go toolchain and both harnesses.

## Use

    ploopy                            ask what to do, then ask for the rest
    ploopy run                        run a plan's open units
    ploopy status [PLAN]              every plan, or one plan's units, and what they took
    ploopy report [PLAN]              what a plan took, slowest units and commands, cost
    ploopy close [PLAN]               delete a finished plan and its state, report in the commit
    ploopy show [PLAN] [1.2]          a unit's work order, or with --prompt the whole prompt
    ploopy replay [PLAN] [1.2]        what that unit's session did
    ploopy mark [PLAN] [1.2] [STATUS] record a status by hand
    ploopy lint [PLAN]                check plans parse the way the loop needs
    ploopy adjust                     change what a running run does next

`ploopy` alone asks what to do, a run going on first, so nothing needs
remembering. Every command then asks for what it was not given: the plan, from
every branch and worktree, then the unit, then the status. Every answer is
also an argument or a flag, for a run nobody is watching.

`ploopy run` asks for the plan and where it runs, then puts the rest on one
page: the harness, model and effort, the agent that takes over on a usage
limit, what a unit due in peak hours does, whether to push, and what to do when
the run ends. The page starts from the last run in this repository, and
starting exactly like it is one keypress. What a flag or the plan's front
matter already says is not asked. `ploopy run --help` lists the flags.
Arguments after `--` go to the harness.

## Where a plan's progress lives

A plan runs on its own branch, often in a worktree, so the checkout you stand
in may know nothing of it. `ploopy status` and the plan picker look at every
checkout of the repository, at every local branch not yet merged into the
default one, and at the runs going on, and show each plan as the copy with the
newest progress has it, with where that is. Picking a plan that lives on
another branch runs it there: in its worktree, or in a new one beside this
checkout, which stays where it is. A branch merged into the default one is done
with and left out, and so is a branch that forked before a plan was deleted, so
a closed plan does not come back. A plan made again after its deletion is a new
one.

## Harnesses

| | effort | peak hours |
|---|---|---|
| `claude` | medium, high, xhigh | none |
| `opencode` | high, max | DeepSeek's |

Claude Code is driven through `stream-json`, so the outcome, cost and session
identity come from a structured result. ploopy assigns each session's id, so
`ploopy replay PLAN 1.2` can hand back a `claude --resume` for the session that
did the work. Its cost is the API-price equivalent, not what a subscription
charges.

opencode is driven through `--format json`, so tool calls, token use, cost
and session identity come from its events. Its outcome is still read from the
final text, and a usage limit from its wording.

## Pushing

`--push`, or `push = true` in `.ploopy.toml`, pushes the branch once the run
ends, before any end action, whatever the run's result. A branch without an
upstream of its own name goes to origin with `--set-upstream`. ploopy never
forces, never pushes the default branch, and never waits for a prompt: a push
that fails or takes over two minutes is reported and the run carries on to its
end action. Sessions still may not push.

## A secondary agent

`--secondary opencode` (with `--secondary-model` and `--secondary-effort`)
names an agent that takes over when the primary hits a usage limit, instead of
the run waiting the limit out. A session cut off mid-unit hands its commits on,
and the secondary is told to continue from them. The run goes back to the
primary at the first unit after the limit resets. When both are limited, the
run waits for whichever comes back first.

## Worktrees

On a branch other than the default, ploopy asks whether to run here or in a
worktree (`--worktree`). In a worktree, this checkout moves to the default
branch — origin's HEAD, else `master`, else `main` — and the branch is checked
out in `../<repo>.worktrees/<branch>`, so you can keep working here while the
run goes on there. The worktree gets only what is committed, so a dirty tree
or an uncommitted plan is refused before anything moves.

The worktree stays when the run ends: its `.ploopy/` holds the logs `replay`
reads. Resume a stopped run by running `ploopy` inside it; once the branch is
done with, `git worktree remove` it and switch back.

## Overnight

DeepSeek bills double on weekday mornings — 01:00–04:00 and 06:00–10:00 UTC,
Monday to Friday. Before a run starts, ploopy asks what a unit due to start
in them does: wait for off-peak, or run anyway. `--peak wait` or `--peak run`
answers for a run nobody is watching, which otherwise runs anyway. Nothing is
asked once the run has started, `ploopy adjust --peak` changes it, and a unit
already running is never interrupted.

A run can end by suspending or powering the machine off, after a countdown
that `c` or Ctrl-C cancels. Keys typed into the terminal during the run are
discarded, and nothing else cancels it. A run cancelled with Ctrl-C, or whose
terminal closes, takes no end action. For as long as it runs, idle suspend is
inhibited, so a lid or an idle timer cannot end the night early.

## Changing a running run

`ploopy adjust`, from any terminal, changes what a run that is already going
does next: its end action, whether it pushes, whether it stops once the current
unit is done, the agent that takes over on a usage limit, and what a unit due
in peak hours does. With nothing passed it asks; every answer is also a flag
(`ploopy adjust --finish poweroff`, `--stop`, `--push`, `--secondary claude`).
`ploopy` alone, while a run goes on, offers it first.

A run announces itself under `$XDG_RUNTIME_DIR/ploopy/` and looks for changes
every two seconds; it says in its feed when it sees one. A stop or a new
secondary ends a usage-limit wait at once, and running through peak hours ends
a wait for off-peak. Nothing here reaches the state file or git.

## What a plan took

`ploopy report PLAN` is the plan's account: units landed and blocked, runs and
wall clock, time in sessions, checks and waits, agents and their cost, tokens,
each stage, the slowest units, whether `verify` and `test` got slower as the
plan went on, the slowest commands the sessions ran, and what went wrong.
DeepSeek's cost is what was billed; Claude's is marked `≈`, the API-price
equivalent.

The run that lands a plan's last unit prints the report and stamps it into that
unit's state commit, so it outlives the plan, its state file and the worktree:
`git log --grep '^ploopy report: '` finds every one. `ploopy report PLAN
--commit` stamps one by hand, into an empty commit.

`ploopy close PLAN` ends a plan: it deletes the plan and its state file in one
commit, on the branch and in the checkout that hold them, with the report as
the commit's body. Nothing else that is staged goes into that commit, so the
rest of the clean-up stays yours. A plan with units still open is closed only
when you confirm, or with `--force`. The subject is `close_commit` in
`.ploopy.toml`, `plans: close %s` by default.

A unit's own numbers, every attempt included, are in `<PLAN>.state.json`;
`ploopy status PLAN` shows them unit by unit. Every session, check, wait and
tool call is appended to `.ploopy/<plan>/stats.jsonl`, one JSON object a line.
That log stays with its checkout; without it the report tells what the state
file holds.

## Per-repository settings

Optional `.ploopy.toml` at the repository root:

    plans = "docs/plans"
    context = ["AGENTS.md"]
    verify = "make lint"
    setup = "pnpm install --frozen-lockfile"
    test = "make test"
    author_paths = ["NOTES.md"]
    skill = ".agents/skills/plan-unit/SKILL.md"
    state_commit = "plans: record %s progress"
    close_commit = "plans: close %s"
    push = true

`verify` runs once before the first unit and after every unit; without it,
nothing is checked. `setup` readies a fresh worktree before that first check,
and may change only ignored files. `author_paths` are yours: the loop never
counts them as a session's mess, so you can keep working while it runs.
Without `skill`, a repository's `.agents/skills/plan-unit/SKILL.md` is used if
it exists, else the one the binary ships with.

Session logs, prompts and handovers go to `.ploopy/` in the repository. It
ignores itself, so nothing there can reach a commit.

## How it decides

The agent is never believed. A unit landed when, and only when:

1. it did not report `BLOCKED`, and did not run past its wall clock;
2. the working tree is clean, discounting your own paths;
3. it ended with `DONE` or `NOTHING-TO-DO`;
4. `DONE` is backed by at least one commit since the unit started;
5. `verify` passes, and `test` too when the unit asks for it.

Anything else is a failed attempt, retried with the reason in the next prompt.
A usage limit is waited out, or handed to the secondary agent, rather than
retried.

`BLOCKED` is the one claim the repository cannot answer, so it is not taken on
trust. The unit goes to a fresh session that is told the claim and asked to
establish it; a block two sessions agree on stops the run and is recorded with
both reasons. `--block-retries 0` believes the first. A unit left blocked is
open again, and the run that picks it up tells its session what blocked it.
