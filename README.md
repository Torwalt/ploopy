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
  c  close a plan without landing it, or a review or fix plan
  l  land a finished branch: rebase, check, close its plans, fast-forward
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
- `launch = "tmux"` in `.ploopy.toml` (or `--detach`) starts the run in a tmux
  window of the current session, `ploopy-<NAME>`, and returns: the window's
  shell runs it, so it outlives whatever started it, an agent's session
  included. A plan's review and fix plans run in its window. Windows stay until
  you close them; `--here` runs in this terminal instead.

**Automation.** `[automation]` in `.ploopy.toml` sets what a run does on its
own.
- `follow_up` reviews an ordinary plan that completes and runs the fix plan its
  triage writes, in the same invocation; on a review it runs that fix plan. A
  fix plan is never reviewed. `--until` and `--units` end the chain, as does a
  run that failed. `--follow-up` overrides it; `--then fixes` is its deprecated
  alias.
- `rescue` names a stronger agent that steps in where a run would stop on a
  unit: retries spent, sessions in a row that touch nothing, or a block the
  second opinion confirmed. It only diagnoses and answers `RETRY`, `DECIDED` or
  `BLOCKED`; it never edits, commits or lands. At most two rescues run per unit
  per run.
- A `DECIDED` rescue decided for you. Its decisions are on the unit in the state
  file, under *Rescued* in `ploopy report`, and in the run's closing summary;
  review them before landing.

OpenCode sessions use a bundled `ploopy` primary agent, supplied through the
child's `OPENCODE_CONFIG_CONTENT`; no repository or global profile installation
is needed. It disables questions and entering or leaving planning mode. The
work order and skill stay in the session prompt, and OpenCode keeps its normal
model-specific system prompt. Model, effort and credentials are not set by the
profile. Existing inline configuration is preserved except for the reserved
`agent.ploopy` entry; configuration files and the parent's environment are untouched.
Invalid inline JSON is reported before starting the session.

**Lifecycle.** A plan exists while master holds it; its progress lives on its
branch.
- Starting a plan commits it on master as last edited, then runs it on a branch
  named after it (`PLAYTEST_FIXES` → `playtest-fixes`) in
  `<repo>.worktrees/<branch>`; the main checkout must be on master.
- Status shows each plan, with its review and fix plans, from the branch that
  got furthest. A plan gone from master is gone, whatever stale branch or
  worktree still holds it.
- `ploopy land` closes it; `ploopy close` drops it from master unlanded.
- `setup` in `.ploopy.toml` readies a new worktree before its first check, from
  inside it, with `$PLOOPY_MAIN` naming the main checkout to copy ignored files
  from; it may change only ignored files.
- A worktree belongs to one plan and its review and fix plans, and stays until
  `ploopy land` lands its branch.

**Adjust.** `ploopy adjust` opens the board for a run going on in this
repository.
- Changes apply from the next unit; `x` stops after the current one.

**Subscriptions.** The run's own agent is the *bedrock*; Claude and Codex take
sessions only while lent.
- `ploopy lend claude --until 09:00`, `--nightly 21:00-09:00` or `--open`;
  `ploopy reclaim claude|all` takes them back.
- Windows about to reset go first, 20% of each week stays yours (`--keep`), and
  a subscription at its limit hands over without waiting.

**Review.** `ploopy review <NAME>` writes and runs `<NAME>_REVIEW.md`.
The board offers two modes: `collected` (the default) runs a read-only session
per landed unit and one for the seams, then a frontier triage; `direct` runs
one frontier session against the complete branch change and the plan. The
reviewer uses the `triage_*` settings in either mode. Direct review records the
first landed unit's starting revision and the branch tip, including fixes
already applied. `--since` is only available for collected reviews. A written
review keeps its mode and recorded revision when resumed.

Both modes write `<NAME>_FIXES.md` only for evidenced defects, regressions,
violated repository rules or unmet plan requirements. Optional improvements
are collected together in **one issue per review pass**, for later interactive
organization or disposal. Unsupported concerns are dropped. Zero fixes and
zero optional findings is a successful result. With follow-up on, any fix plan
runs immediately using the implementation agent, in the same worktree.

- Add a checklist at `.agents/review-checklist.md`.
- Select the mode on the board, set `[review] mode`, or use `--review-mode direct`
  on `review` or a `run --follow-up`.
- Without a persistence hook, optional findings are committed beside the plan
  as `<NAME>_FOLLOWUPS_<reviewed-head>.md`. Closing the plan leaves them intact.
- To use another Markdown issue system, configure `followups_instructions` and
  `followups_paths` together under `[review]`. The first names a Markdown file
  inlined into the reviewer prompt; the second lists allowed repository paths
  or globs (`*` does not cross directories). The reviewer may change one
  follow-up Markdown document and must link it from *For the author*. Ploopy
  does not execute a hook command or depend on the issue system.

For Benchpress, copy [these instructions](docs/review-followups-benchpress.md)
into `.agents/review-followups.md` and configure:

```toml
[review]
mode = "direct"                         # or "collected"
followups_instructions = ".agents/review-followups.md"
followups_paths = ["docs/workbench/*.md"]
```

Review session history and stats include `review_mode`, `review_base` and
`review_head` alongside time and cost, so comparisons can identify the reviewed
snapshot. Costs remain subject to what each harness reports.

**Land.** `ploopy land [BRANCH]` rebases the branch onto the default branch,
runs the checks, closes the branch's plans and fast-forwards.
- A conflict or red check goes to a `land-repair` session; `--no-agent` stops
  instead.
- The worktree and branch are removed; master is yours to push.

**Skills.** `ploopy skills` writes the skills ploopy ships into
`.agents/skills` (linked into `.claude/skills` when there is a `.claude`):
`ploopy`, `plan-writing`, `plan-unit`, `plan-review`, `review-triage`, `review-direct`,
`unit-rescue` and `land-repair`.
- Rerunning it brings them up to date with the binary; runs always use the
  binary's copy.
- The repository's own rules go in `ploopy-local`, a section per skill; runs
  add each section after its skill.

**History.** Every session is logged to `.git/ploopy/history.jsonl`, shared by
all worktrees.
- `ploopy history` shows what a unit took each agent and where the usage
  windows stand; the board's footer forecasts the run from it.

**Agents.** Claude Code, opencode and Codex run through their installed CLI and
login; pick one in the Bedrock column or with `agent:`, `model:`, `effort:` in
front matter.
- The picker offers Sonnet and Opus, Sol, and DeepSeek V4.1 Flash, all at high
  effort. `.ploopy.toml` can replace those lists; explicit plan and flag
  settings still accept other supported choices.
- Flash starts on OpenCode Go. When Go reports a usage limit, a fresh session
  continues the unit through the DeepSeek API without spending an attempt.
  Partial work stays in place, and each session records the route it used.
  Go is tried again after the reported reset, or after 30 minutes when the
  CLI gives no reset. This also applies to review and landing repair sessions.
- Go counts peak hours double like the API, so both follow the run's peak
  setting.
  Claude and Codex keep their subscription reserves and have no API fallback
  by default. Authentication and other errors do not trigger a paid fallback.
- Units are judged from git, so an adapter whose output format drifted costs no
  unit; it is reported in red at the end of the run.

For example, these are the default choices, written explicitly in `.ploopy.toml`:

```toml
[agents.claude]
models = ["sonnet", "opus"]
efforts = ["high"]

[agents.codex]
models = ["gpt-6.1-sol"]
efforts = ["high"]

[agents.opencode]
models = ["opencode-go/deepseek-v4.1-flash", "deepseek/deepseek-flash"]
efforts = ["high"]

[agents.opencode.fallbacks]
"opencode-go/deepseek-v4.1-flash" = "deepseek/deepseek-flash"
```

The first model is the default. Add model IDs from the installed harness to
enable more choices; `opencode models` lists its provider/model IDs. Providers
and credentials stay in the harness. An explicit `fallbacks` table replaces
the default routes; `fallbacks = {}` under `[agents.opencode]` disables the
automatic API fallback. Routes have one fallback, with no chains or cycles.
Existing leases keep the model and effort they were lent with.

## Settings

`.ploopy.toml` at the repository root configures a repository once. Every key
is optional and unknown keys are refused. A value from the file prefills the
board and can still be changed there; only flags and a plan's front matter lock
a field. A flag wins over the plan, the file, the last run and the default, in
that order; the board says where a prefilled value came from.

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
mode = "collected"                      # collected | direct
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

`[run] agent` and `effort` are enough for an unattended run:
`ploopy run --plan X --detach` starts from them. `[run] push` is the documented
push key; the old top-level `push` still works, and `[run] push` wins when both
are given. `[review]` chooses the review mode, frontier agent, model, effort and spending
cap, plus optional follow-up persistence instructions and paths.
`[automation]` is the follow-up and the rescue agent, model and effort; setting
`rescue = "none"` turns the rescue off. When `rescue` is absent, a run uses the
last run's rescue, and then none.

## Try it

```sh
go run ./dev/sandbox      # or: nix run .#sandbox
```

A toy repository with fake `claude`, `codex` and `opencode`; edit its
`scenario.toml` to change how sessions end.

## Develop

`nix develop` (direnv loads it) has go, the three harnesses and `ploopy` built
from this checkout on every call.
