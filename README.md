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
    ploopy status                     every plan and where it stands
    ploopy lint [PLAN]                check plans parse the way the loop needs
    ploopy show PLAN 1.2 [--prompt]   a unit's work order, or the whole prompt
    ploopy mark PLAN 1.2 skipped      record a status by hand
    ploopy replay PLAN 1.2            what that unit's session did

With nothing passed, ploopy asks for the plan, the harness, the model, the
effort and what to do when the run ends. Every answer is also a flag, for a run
nobody is watching: `ploopy run --help`. Arguments after `--` go to the harness.

## Harnesses

| | effort | peak hours |
|---|---|---|
| `claude` | medium, high, xhigh | none |
| `opencode` | high, max | DeepSeek's |

Claude Code is driven through `stream-json`, so the outcome, cost and session
identity come from a structured result. ploopy assigns each session's id, so
`ploopy replay PLAN 1.2` can hand back a `claude --resume` for the session that
did the work.

opencode reports in prose, so it is the degraded path: the outcome is read from
the last lines of its output.

## Overnight

DeepSeek bills double on weekday mornings — 01:00–04:00 and 06:00–10:00 UTC,
Monday to Friday. Starting a run inside one of those windows asks whether to
wait for off-peak; a unit already running is never interrupted.

A run can end by suspending or powering the machine off, after a countdown any
keypress cancels. For as long as it runs, idle suspend is inhibited, so a lid
or an idle timer cannot end the night early.

## Per-repository settings

Optional `.ploopy.toml` at the repository root:

    plans = "docs/plans"
    context = ["AGENTS.md"]
    verify = "make lint"
    test = "make test"
    author_paths = ["NOTES.md"]
    skill = ".agents/skills/plan-unit/SKILL.md"
    state_commit = "plans: record %s progress"

`verify` runs once before the first unit and after every unit; without it,
nothing is checked. `author_paths` are yours: the loop never counts them as a
session's mess, so you can keep working while it runs. Without `skill`, a
repository's `.agents/skills/plan-unit/SKILL.md` is used if it exists, else the
one the binary ships with.

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
A usage limit is waited out rather than retried. `BLOCKED` stops the run.
