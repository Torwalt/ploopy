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

    ploopy                            ask what to run, and run it
    ploopy status [PLAN]              every plan, or one plan's units, and what they took
    ploopy lint [PLAN]                check plans parse the way the loop needs
    ploopy show PLAN 1.2 [--prompt]   a unit's work order, or the whole prompt
    ploopy mark PLAN 1.2 skipped      record a status by hand
    ploopy replay PLAN 1.2            what that unit's session did

With nothing passed, ploopy asks for the plan, where it runs, the harness, the
model, the effort, what a unit due in peak hours does and what to do when the
run ends. Every answer is also a flag, for a run nobody is watching:
`ploopy run --help`. Arguments after `--` go to the harness.

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
Monday to Friday. A run on a harness with peak hours asks once, before it
starts, what a unit due to start in them does: wait for off-peak, or run
anyway. `--peak wait` or `--peak run` answers for a run nobody is watching,
which otherwise runs anyway. Nothing is asked once the run has started, and a
unit already running is never interrupted.

A run can end by suspending or powering the machine off, after a countdown
that `c` or Ctrl-C cancels. Keys typed into the terminal during the run are
discarded, and nothing else cancels it. A run cancelled with Ctrl-C, or whose
terminal closes, takes no end action. For as long as it runs, idle suspend is
inhibited, so a lid or an idle timer cannot end the night early.

## What a run took

When a run ends, before any end action, ploopy prints the plan unit by unit
and stage by stage: wall clock, time in `verify` and `test`, attempts, tokens
read and written, and cost. A unit's numbers cover every session it took,
failed attempts included, and are recorded in `<PLAN>.state.json`.
`ploopy status PLAN` prints the same table at any time.

Every session, check and wait is also appended to `.ploopy/<plan>/stats.jsonl`,
one JSON object a line, for whatever else you want to ask of it.

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
