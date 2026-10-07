# ploopy

Runs a plan overnight, one unit per fresh Claude Code or opencode session. The
agent commits; ploopy judges from git whether the unit landed, retries or
stops, and records progress in `<PLAN>.state.json` beside the plan.

## Flow

1. Write `docs/plans/<NAME>.md` with the `plan-writing` skill and commit it on a branch.
2. `ploopy` → run a plan → in a worktree → like last time. Keep working on master.
3. `ploopy adjust` from any terminal changes what the run does next.
4. In the morning: `git switch <branch>`, review, `ploopy close <NAME>`.

## Start

```
$ ploopy
┃ What now?
┃ > run a plan
┃   status of every plan
┃   report on a plan: time, checks, cost
┃   close a finished plan: delete it and its state, report in the commit
┃   show a unit's work order
┃   replay a unit's session
┃   mark a unit by hand
┃   lint the plans

┃ Where?
┃   here, on engine-pass
┃ > in a worktree for engine-pass; this checkout moves to master

┃ How should it run?
┃ > like last time: opencode deepseek/deepseek-flash high · no end action · push at the end · wait
┃   for off-peak
┃   change the settings

How should it run?
┃ Agent
┃ ← opencode →
  Model
  deepseek/deepseek-flash
  Effort
  high
  On a usage limit
  wait it out
  A unit due in opencode's peak hours (Mon–Fri 03:00–06:00, 08:00–12:00)
  wait for off-peak
  Push the branch when the run ends
  yes
  When the run ends
  nothing — leave the machine as it is
```

Every answer is also a flag: `ploopy run --help`.

