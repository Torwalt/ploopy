# ploopy

Runs a plan overnight, one unit per fresh Claude Code, opencode or Codex session. The
agent commits; ploopy judges from git whether the unit landed, retries or
stops, and records progress in `<PLAN>.state.json` beside the plan.

## Flow

1. Write `docs/plans/<NAME>.md` with the `plan-writing` skill.
2. `ploopy` → run a plan → like last time. It runs in a worktree of the plan's
   branch; keep working on master.
3. `ploopy adjust` from any terminal changes what the run does next.
4. In the morning, still on master: `ploopy review <NAME>`, read what it puts
   to you, run the fix plan it wrote.
5. `ploopy land <branch>` closes all three plans, rebases the branch onto master,
   runs the checks, and fast-forwards master. Master is yours to push.

## Trying it out

A sandbox runs ploopy without real sessions or real plans. Either command opens
a shell in a toy repository with fake `claude`, `codex` and `opencode` on
`PATH`, and its own state directories:

```sh
go run ./dev/sandbox
nix run .#sandbox
```

It prints the directory it made. Edit `scenario.toml` there to change how long a
session takes, how much of a window it uses, its cost or its outcome (`done`,
`blocked`, `nothing`, `red`, `dirty`, `limit` or `logged-out`).
`repo/docs/plans/TOY.md` is the plan, with eight tiny units. Then run `ploopy`,
or `ploopy review TOY`.

## Worktrees

Every run, and every review, goes to a worktree of the plan's branch at
`<repo>.worktrees/<branch>`, whichever checkout ploopy is started from:

- a worktree that holds the branch is used as it is;
- a branch checked out nowhere is checked out there;
- a branch the main checkout has moves there, and the main checkout to the
  default branch. Uncommitted changes besides the plan and the author's own
  paths move with the branch, into the worktree the run sees;
- a plan on the default branch gets a branch named after it, `PLAYTEST_FIXES`
  → `playtest-fixes`, at the commit it is on. A later run finds that branch
  again;
- a plan not yet committed is committed on the branch the run uses, and taken
  out of the default branch's checkout;
- what a removed worktree left behind at that path is cleared, and anything
  else there is moved aside.

`close`, `mark`, `report --commit` and `show` check a branch out the same way
when no checkout holds it.

A run never refuses uncommitted changes. Each session that starts on them is
told which paths are dirty and whether an earlier attempt or the worktree
before this run left them, and a red check on inherited changes goes to the
first session instead of stopping the run. A review still refuses them, because
its sessions change nothing.

## Land

`ploopy land [BRANCH]` refuses a branch that still has open units. Otherwise:

- every plan on the branch is closed, each in a commit carrying its report;
- the branch is rebased onto the default branch and `verify` and `test` run on
  the result, each once more before it counts as failed;
- a conflict, or a check that fails on the rebased branch, goes to a session
  with the `land-repair` skill. During a conflict the session resolves and
  stages, and ploopy continues the rebase; after a failing check it adds
  commits. git and the checks judge it, up to `--attempts` sessions per repair.
  Who repairs is asked on a page of its own, like the triage, and remembered;
- the default branch is fast-forwarded where it is checked out. If it moved
  during the checks, the branch is rebased again;
- a branch that was pushed before is pushed again with a lease, ploopy's
  worktree for it is removed and the branch deleted.

A failed repair leaves the default branch as it was: after a conflict the
rebase is aborted, after a failing check the branch stays rebased so the
failure can be looked at. Logs go to `.git/ploopy/land/<branch>/`.
`--no-agent` stops at the first conflict or failure instead.

## Review

`ploopy review <NAME>` writes `<NAME>_REVIEW.md` beside the plan, commits it and
runs it like any plan:

- one session per landed unit, with the run's agent, reads that unit's work
  order, handover and commits and writes findings with the `plan-review` skill.
  It changes nothing: a review session that commits stops the run;
- one more session looks only at the seams between the units;
- one triage session weighs every finding with the `review-triage` skill. Who
  runs it is asked on a page of its own — agent, model, effort and a spending
  cap, Opus at first and then like last time — and the `--triage-*` flags
  answer it. Until the triage has run, `ploopy review` on the same plan offers
  to change it. The triage writes `<NAME>_FIXES.md`, an ordinary plan for every
  finding that holds. It makes a recommended choice for each design finding so
  the plan can run immediately, and records each recommendation and its
  alternatives under *For the author* at the end of the review. The author can
  revise those choices before running the fix plan. The triage may commit those
  two files and nothing else.

A repository adds its own review checklist at `.agents/review-checklist.md`, or
names one with `review_checklist` in `.ploopy.toml`; collecting sessions go
through it item by item.

## Start

```
$ ploopy
┃ What now?
┃ > run a plan
┃   review a plan's work: findings, a triage, a fix plan
┃   status of every plan
┃   report on a plan: time, checks, cost
┃   close a finished plan: delete it and its state, report in the commit
┃   show a unit's work order
┃   replay a unit's session
┃   mark a unit by hand
┃   lint the plans

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

## Subscriptions

The run's own agent is the *bedrock*: it always works. Claude and Codex are
*subscriptions*: they take a session when you lend them, and nothing ever
waits for one. Lending is per machine, not per run, and every run reads the
same lease file.

`ploopy lend` lists what is lent. `ploopy lend claude --until 09:00`,
`ploopy lend codex --open`, or `ploopy lend claude --nightly 21:00-09:00` lends
a subscription; `--keep` says how much of its week stays yours, `--model` and
`--effort` how it runs. `ploopy reclaim claude` takes one back and
`ploopy reclaim all` takes them all. They work from any directory, like
`ploopy adjust`.

- an `until` lease lasts to the next such clock time, or to an RFC 3339 instant
  in the file;
- a `nightly` lease is lent every day inside its span, such as 21:00-09:00;
- an `open` lease lasts until reclaimed.

Each lease keeps a share of its subscription's week for you, 20% unless
`--keep` says otherwise. The reserve shrinks as the weekly reset nears, so a
week's leftovers are used before they reset, and `--keep 0` spends the lot.
The settings page shows the active leases above its questions.

Inside its lease, each session goes to the subscription with room that ploopy
puts first, else to the bedrock. A window already open goes before a fresh
one, and the one that resets first leads; among fresh ones, the subscription
with the most of its week to lose goes first:

- a window that resets before you reclaim the subscription is used up for
  free;
- a fresh window opens only when it would end before the lease does;
- a subscription at its usage limit hands over without waiting;
- one that cannot run, logged out or not installed, hands over too and sits
  out half an hour;
- none goes past the share of the week you keep.

A lease only lets a subscription take sessions; the bedrock takes everything
else. Claude reports its windows only from inside a session, so before the
first one ploopy goes by the last it saw; Codex's are read live through
`codex app-server`, without a model call.

## History

Every session goes to `.git/ploopy/history.jsonl`, shared by all worktrees and
kept when one is removed. `ploopy history` says what a landed unit took each
agent here, every attempt included, and where the windows stand; the settings
page shows the same above its questions. An estimate needs three landed units
of one agent; units run on a lent subscription are preferred once there are
three, because your own use moves the windows during the day. `--import` adds the
sessions the checkouts' stats logs still hold, Claude's windows read back out of
its transcripts.

## When an adapter falls short

A harness that changes its output format must not cost a unit, and must not go
unnoticed. Each adapter knows what a finished session reports — Claude a result
event, its cost, tokens and usage windows; Codex a thread and token usage;
opencode its steps — and counts structured lines that no longer decode. Plain
output beside the stream, such as a warning, does not count. Units are judged
from git as always; what fell short is said once when it first happens, marked
in the stats log and the report, and repeated in red at the end of the run:

```
[ploopy] harness adapter degraded
  Units were judged from git as always; these sessions' cost, tokens or usage windows are incomplete.
  claude sonnet high: 2 of 2 session(s)
    no result event: cost, tokens and turns are unknown
    1 line did not decode: result: json: cannot unmarshal string into Go struct field streamLine.num_turns of type int
    logs: .ploopy/night/logs/1.2-0.log, .ploopy/night/logs/1.3-0.log
```

## Codex

Choose `codex` on the Agent page for runs, reviews, triage or usage-limit
fallback. It uses the installed Codex CLI and its existing login, config,
MCP servers and skills. `nix develop` includes the CLI; authenticate with
`codex login` before running unattended.

For a review entirely with Codex, choose it for both the run and the triage.
The same choices can be passed as flags:

```sh
ploopy review PASS --agent codex --effort high --triage-agent codex
ploopy run --plan PASS --agent codex --effort high
```

Plans may set `agent: codex`, `model: gpt-6.1-sol` and `effort: high` in
front matter, or `Agent: codex`, `Model:` and `Effort:` on individual units.
The model picker offers GPT-6.1 Sol, Astra, Luna, GPT-6 Sol and GPT-5.6 Sol;
`--model` accepts other model names. Efforts are `low`, `medium`, `high`,
`xhigh` and `max`. Availability follows your Codex account.

The adapter runs `codex exec --json`, sends the work order on stdin, and
records text, tool activity, token usage and the actual thread ID. Before and
after each session it reads the account's usage windows from `codex
app-server`. `ploopy
replay` shows `codex resume <thread-id>` to reopen it. It recognizes usage
limits for the normal wait/fallback policy. Codex's stream reports no dollar
cost or spending-cap control: costs stay unknown and a nonzero `--budget`,
`--triage-budget` or unit `Budget:` is refused before the session starts.

Like the other unattended adapters, Codex runs with approval and sandbox
bypasses so it can build, test and commit without a terminal. It enables
hooks, installs ploopy's Bash git-command guard, and bypasses hook trust for
that invocation; this also runs enabled user/project hooks. Native web search
is disabled. Extra CLI arguments after `--` can override Codex settings.
See the official [non-interactive interface](https://learn.chatgpt.com/docs/non-interactive-mode)
and [hook contract](https://learn.chatgpt.com/docs/hooks).

To write a plan interactively in Codex, copy `skills/plan-writing/` from this
repository into your project's `.agents/skills/plan-writing/` (or your
user-level `~/.agents/skills/plan-writing/`), then ask Codex to use
`$plan-writing`. Planning remains an interactive harness session; ploopy
executes the resulting committed plan.
