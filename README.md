# ploopy

Runs a plan one unit per fresh Claude Code, opencode or Codex session. The
agent commits; ploopy judges from git whether the unit landed, retries or
stops, and records progress in `<PLAN>.state.json` beside the plan.

## Start

```
$ ploopy
What now?
> r  run a plan
  v  review a plan's work: findings, a triage, a fix plan
  s  status of every plan
  p  report on a plan: time, checks, cost
  h  history: what a unit took each agent here, and the usage windows
  n  lend a subscription to every run, or list what is lent
  c  close a finished plan: delete it and its state, report in the commit
  l  land a finished branch: close its plans, rebase, check, fast-forward
  w  show a unit's work order
  y  replay a unit's session
  m  mark a unit by hand
  t  lint the plans

ploopy · PASS_FIXES on pass-fixes · 12 open units
──────────────────────────────────────────────────────────────────────────────
 1 Bedrock             2 claude                3 codex               4 Run
 a agent  opencode     u lent  until 09:00     u lent  until I reclaim  s subs  all lent
 m model  ds-flash     m model sonnet          m model gpt-6.1-sol      w week  keep reserves
 e effort high         e effort high           e effort high            p push  yes
 l limit  wait         k keep  20% → 6%        k keep  0%               f end   nothing
 o peak   wait           5h 51% → 22:10          5h 60% → 20:12
                          week 51% → Tue          week 9% → Thu
──────────────────────────────────────────────────────────────────────────────
 ≈ claude 7 · codex 5 · opencode 0 units · ≈ $0.00 · done ≈ 04:12
 enter start   1–4 column   letter next   shift+letter back   +/- hour   esc
```

A command's letter picks it; while a run is going on in this repository, `a`
changes it. The board opens prefilled from the last run; a setting given as a
flag or in the plan's front matter shows locked. Every answer is also a flag:
`ploopy run --help`.

## Features

**Run.** Write `docs/plans/<NAME>.md` with the `plan-writing` skill, then run it.
- Each unit gets a fresh session; commits since its base, a clean tree and the
  `verify`/`test` checks from `.ploopy.toml` decide whether it landed.
- A stopped run records the session it cut off and resumes there next time.

**Worktrees.** A plan runs in `<repo>.worktrees/<branch>`, its branch named
after it (`PLAYTEST_FIXES` → `playtest-fixes`), so master stays yours.
- A worktree belongs to one plan and its review and fix plans; other plans the
  branch carries have no say over it.
- It is removed once that family is complete or closed.

**Adjust.** `ploopy adjust` opens the board for a run going on in this
repository.
- Changes apply from the next unit; `x` stops after the current one.

**Subscriptions.** The run's own agent is the *bedrock*; Claude and Codex take
sessions only while lent.
- `ploopy lend claude --until 09:00`, `--nightly 21:00-09:00` or `--open`;
  `ploopy reclaim claude|all` takes them back.
- Windows about to reset go first, 20% of each week stays yours (`--keep`), and
  a subscription at its limit hands over without waiting.

**Review.** `ploopy review <NAME>` writes and runs `<NAME>_REVIEW.md`: one
read-only session per landed unit and one for the seams, then a triage that
writes `<NAME>_FIXES.md`.
- `--then fixes`, or `n` on the board, runs the fix plan in the same worktree
  right after.
- A repository adds its own checklist at `.agents/review-checklist.md`.

**Land.** `ploopy land [BRANCH]` closes the branch's plans, rebases it onto the
default branch, runs the checks and fast-forwards.
- A conflict or red check goes to a `land-repair` session; `--no-agent` stops
  instead.
- The worktree and branch are removed; master is yours to push.

**History.** Every session is logged to `.git/ploopy/history.jsonl`, shared by
all worktrees.
- `ploopy history` shows what a unit took each agent and where the usage
  windows stand; the board's footer forecasts the run from it.

**Agents.** Claude Code, opencode and Codex run through their installed CLI and
login; pick one in the Bedrock column or with `agent:`, `model:`, `effort:` in
front matter.
- Units are judged from git, so an adapter whose output format drifted costs no
  unit; it is reported in red at the end of the run.

## Try it

```sh
go run ./dev/sandbox      # or: nix run .#sandbox
```

A toy repository with fake `claude`, `codex` and `opencode`; edit its
`scenario.toml` to change how sessions end.

## Develop

`nix develop` (direnv loads it) has go, the three harnesses and `ploopy` built
from this checkout on every call.
