# Lending: a bedrock agent, lent subscriptions, and a board to steer them

Status: authority for `LENDING_EXECUTION.md`, written 2026-10-17 against `6676ee0`.

ploopy becomes the manager of what the author already pays for. Every unit
gets done by the *bedrock* agent, which always works (such as opencode with
DeepSeek). The *subscriptions* (Claude, Codex) are an upgrade: whichever of
them the author has lent, and that has room, takes the next session. When none
has room, the bedrock takes it at once. A subscription nobody uses before its
week resets is money left lying around, and the order of the subscriptions
follows from that.

## 1. Decisions settled with the author (2026-10-17)

- **D1** `--allow-dirty` goes. A run always hands uncommitted changes in its
  worktree to the session, and the prompt says whose they are. Every run has a
  worktree of its own branch, so those changes belong to the branch.
- **D2** Moving a branch out of the main checkout carries its uncommitted
  changes into the new worktree. It no longer refuses.
- **D3** Two kinds of account. The *bedrock* is the run's own agent: it always
  works, and nothing ever waits for a subscription. The *subscriptions* are the
  metered harnesses (`harness.Metered`: Claude, Codex). They take sessions only
  while lent and with room.
- **D4** Lending is per machine, not per run. Several runs may go on, but
  only on this one machine. A lease says on what terms the author lends one
  subscription: until a time, nightly in a span, or until reclaimed. It lives
  in one file that every run reads, and the author changes it with
  `ploopy lend` / `ploopy reclaim`, from the board, or with `ploopy adjust`.
- **D5** Fresh-window counts (`--night claude=2`) go. A lease's end decides
  which fresh windows fit, and the weekly reserve decides how much of the week
  goes.
- **D6** The reserve is set per subscription (20% by default) and shrinks as
  the weekly reset nears (§6), so a week's leftovers are used before they
  reset. A run can be told to spend everything instead (`spend all`).
- **D7** ploopy picks the order among lent subscriptions, not the author: first
  a window already open, then whichever has the most of its week to lose (§7).
  The bedrock is always last. The author considered a fixed Claude → Codex order
  and chose this instead.
- **D8** A plan or a run chooses which lent subscriptions it may use: all of
  them, any one of them, or none (bedrock only). This is set with
  `subscriptions:` in front matter, `--subscriptions`, or a key on the board
  (§9). Units that name their own `Agent:` run as named whatever the set.
- **D9** The secondary stays, as the bedrock's own fallback when the bedrock
  hits a usage limit (such as another opencode model when DeepSeek is
  limited).
- **D10** One keyboard board replaces the settings form, the "like last time"
  question and the "While you are away" page. `ploopy adjust` shows the same
  board. It needs bubbletea as a direct dependency, which `AGENTS.md` must then
  list.
- **D11** The top-level "What now?" menu gets one-key shortcuts.
- **D12** The repository gets a sandbox for trying all of this without real
  sessions or real plans: fake `claude`, `codex` and `opencode` binaries that
  speak each CLI's format and report usage windows from an editable scenario,
  and a toy repository with isolated state directories (§12).
- **D13** `ploopy review` uses the same board. The collecting sessions get the
  run's columns, and the triage becomes one more column on the board instead of
  a page of its own (§13).
- **D14** A review can go straight on to its fix plan. When its triage has
  written one, the same invocation runs it with the review's settings, and
  pushes and takes its end action only after that (§13).

## 2. Vocabulary

- **bedrock**: the run's own agent: `loop.Options.Harness/Model/Effort`, and
  `runSettings.Harness/Model/Effort` in `cmd/ploopy/settings.go`. The README and
  every message call it the bedrock. Code identifiers may keep `primary`.
- **subscription**: a harness implementing `harness.Metered`. A subscription
  harness can still be a run's bedrock (Claude for a whole plan, by day). It is
  then that run's bedrock and never also lent to it, as `resolveSettings`
  enforces today for night agents.
- **lease**: one subscription lent on some terms (§4). A harness without a lease
  is *mine*: no run touches it.
- **room**: whether a lent subscription may take the next session (§5).
- **keep**: the share of a subscription's week left to the author (§6).

## 3. Uncommitted changes at the start of a run

`checkTree` in `internal/loop/loop.go` refuses today unless the loop itself
left the tree dirty (`progress.LeftDirty` for that unit) or `AllowDirty` is set.
From now on:

- An implementing plan never refuses. When `repo.DirtLines` is not empty before
  a session, its prompt gets a section after *Your previous attempt at this
  unit*:

  ```
  ## Uncommitted changes in the tree

  <origin sentence>

  <the DirtLines, one per line, in a fenced block>

  They are part of this branch's work. Finish them as part of this unit, or
  discard what does not belong to it. Leave the tree clean when you finish.
  ```

  The origin sentence is *"An earlier attempt at this unit left these
  uncommitted."* when `progress.forUnit(unit) && progress.LeftDirty`. Otherwise
  it is *"These were in the worktree before this run started. Nobody has
  committed them."*. With a clean tree the section is left out entirely; the
  prompt template has a placeholder that renders empty.
- With uncommitted changes the loop did not leave, a red preflight does not
  stop the run. The run says that verify is red on the inherited changes, and
  the failing tail of the verify log goes into the first session's prompt, in
  the same section under *Verify is red with these changes:*. The judge after
  the session is unchanged: a red verify, or a dirty tree after the session,
  fails the attempt as today.
- A review plan (any unit whose `Role()` is not `plan.Implement`) still refuses
  a dirty tree at the start. Its sessions must not change anything. The refusal
  no longer names `--allow-dirty`.
- `setup` must still leave the tree clean: `Loop.setup` is unchanged.
- `--allow-dirty` stays registered, hidden and deprecated ("uncommitted changes
  always go to the session"), like `--worktree`, and has no effect.
  `Options.AllowDirty` is deleted.

Carrying changes on hand-off (D2): `env.handOff` in `cmd/ploopy/worktree.go`
refuses today when `DirtLines` of the main checkout is not empty. Instead:

1. Stash exactly the dirty paths, untracked ones included. That is every
   `Change.Path`, plus `Change.From` for a rename, as `repo.Dirt` returns them.
   Author paths are not in `Dirt`, so they stay in the main checkout.
2. Do `repo.HandOff` as today.
3. `git stash pop` inside the new worktree. The stash was made at the branch's
   HEAD, and the worktree checks out that same commit, so it applies cleanly.
4. If step 2 or 3 fails, pop the stash back in the main checkout (after
   switching it back to the branch, which `HandOff` already does on failure)
   and return the error. As in `HandOff`, these steps run under
   `context.WithoutCancel`.

The message printed afterwards says how many changes moved with the branch.

## 4. The lease file

Path: `$XDG_STATE_HOME/ploopy/leases.json`, else
`$HOME/.local/state/ploopy/leases.json`. It is a state directory, not the
runtime one, so a reboot keeps "until I reclaim". The directory is created
`0700`, and the file is replaced whole through a temp file and a rename, like
`control.writeJSON`.

```json
{
  "leases": [
    {"harness": "codex",  "model": "gpt-6.1-sol", "effort": "high",
     "kind": "open", "keep": 0},
    {"harness": "claude", "model": "sonnet", "effort": "high",
     "kind": "until", "until": "2026-10-18T09:00:00+02:00", "keep": 20}
  ]
}
```

- At most one lease per harness, kept in harness-name order. Writing a lease for
  a harness replaces its lease; reclaiming removes it.
- `kind` is one of:
  - `until`: lent from now until `until`, an RFC 3339 instant;
  - `nightly`: lent every day inside `span`, such as `"21:00-09:00"`, in the
    format `loop.ParseAway` reads;
  - `open`: lent until reclaimed.
- `keep` is a whole percent, 0–100.
- `model` and `effort` are what the subscription runs as when lent.
- A lease of kind `until` whose instant has passed is read as absent. Reading
  never rewrites the file.
- A missing file means nothing is lent. An unreadable or invalid file is an
  error naming the path. A run that hits it mid-run says so once and treats
  nothing as lent.

The package is `internal/lease`, with its own small types (harness, model,
effort as strings; span as a string). It imports neither `loop` nor `harness`.
`cmd/ploopy` validates a span with `loop.ParseAway` before writing one.

Commands (in `cmd/ploopy`, beside `adjust`):

```
ploopy lend                              # lists the leases, one per line
ploopy lend codex --open [--keep 0]
ploopy lend claude --until 09:00 [--keep 20] [--model sonnet] [--effort high]
ploopy lend claude --nightly 21:00-09:00
ploopy reclaim codex | ploopy reclaim all
```

`--until HH:MM` means the next such clock time from now. `--keep` defaults to
the harness's current lease's keep, else 20. Model and effort default to the
current lease's, else the harness's first model and `preferredEffort`. A
harness that is not `harness.Metered` is refused: "X has no subscription to
lend". Like `adjust`, `lend` and `reclaim` work from any directory.

## 5. Room

`internal/loop/night.go` holds today's `Away`, `Night`, `NightAgent` and
`Night.Room`. They become `Lease` and `Lending`:

```go
type LeaseKind string // "until", "nightly", "open"

type Lease struct {
	Agent
	Kind  LeaseKind
	Until time.Time // kind until
	Span  Away      // kind nightly
	Keep  float64   // share of the week kept for the author, 0..1, before it shrinks
}

type Lending struct {
	Leases   []Lease // in lease-file order
	SpendAll bool    // the run keeps no reserve
}
```

`Away`, `ParseAway` and `Away.Span` stay as they are. A lease's span at an
instant:

| kind | inside | end |
|---|---|---|
| until | now < Until | Until |
| nightly | `Span.Span(now)` inside | that span's end |
| open | always | none (zero time) |

`Lending.Room(lease, b *harness.Budget, ownWindow bool, limited, now)` decides,
in this order, and the first rule that applies wins. `why` reads as today's
messages do.

1. `limited` after now → no ("at its usage limit until …").
2. Not inside the lease → no ("lent from …" for nightly, "not lent" for a
   passed until).
3. `keep := EffectiveKeep(lease, b, now, SpendAll)` (§6). When `keep >= 1` →
   no ("its whole week stays yours"). When `b` is known and
   `b.Weekly.UsedAt(now) >= 1 - keep` → no ("has spent N% of its week; M% stays
   yours").
4. The five-hour window is active (`b.Session.Active(now)`):
   - `Used >= 1` → no ("window is spent until …");
   - the lease has an end, the window resets after it, and `ownWindow` is false
     → no. That window is the author's, and it runs past the time they take the
     subscription back;
   - otherwise → yes, not fresh.
5. No active window: when the lease has an end and `now + length` is after it →
   no ("a fresh window would run past …"). Otherwise → yes, fresh.

`ownWindow` is today's `meter.opened[b.Session.ResetAt]`: a window this run
opened is its own to use up wherever its reset falls. The meter bookkeeping
(`meter`, `enter`, `saw`, `windows`, `opening`, `metered`, `benched`) stays.
`meter.enter` keys on the lease span's start for nightly leases. For until and
open leases it keys on the zero time, so a run opens nothing twice. The
fresh-window count it guarded is gone (D5). The `opened` map stays, because
`ownWindow` needs it.

Several runs read the same leases. Each keeps its own meters. The windows a
harness reports belong to the account, so every run sees the same `Used`. A
window opened by another run counts as not its own, so near a lease's end only
the run that opened that window keeps using it. That is accepted.

## 6. The shrinking reserve

```
EffectiveKeep(lease, b, now, spendAll):
    if spendAll:                           return 0
    if b == nil or b.Weekly.ResetAt.IsZero() or !b.Weekly.Active(now):
                                           return lease.Keep
    left := b.Weekly.ResetAt - now         // time until the week resets
    return lease.Keep × clamp(left / 7d, 0, 1)
```

`Keep` of 1 (100%) never shrinks: `EffectiveKeep` returns 1 whenever
`lease.Keep >= 1`, before the arithmetic.

Worked examples, keep 20%:

| weekly used | resets in | effective keep | limit (1 − keep) | room by the week? |
|---|---|---|---|---|
| 51% | 120h | 20 × 120/168 = 14.3% | 85.7% | yes |
| 85% | 120h | 14.3% | 85.7% | yes |
| 86% | 120h | 14.3% | 85.7% | no |
| 93% | 36h | 20 × 36/168 = 4.3% | 95.7% | yes |
| 93% | 36h, spend all | 0% | 100% | yes |
| 99% | 6h | 0.7% | 99.3% | yes |
| 40% | unknown reset | 20% | 80% | yes |

The board and `ploopy lend` show keep as `20% → 4%` when it has shrunk by at
least one point, and as `20%` otherwise.

## 7. Which lent subscription goes first

Each session, `nightPick` (renamed `lendPick`) evaluates every lease with
`Room`. Among those with room:

1. Those whose window is already active (not fresh) come before those that
   would open a fresh one. Among them, the one whose window resets first goes
   first: it expires first.
2. Among those needing a fresh window, the one with the most of its week to
   lose goes first:
   `urgency = (1 - EffectiveKeep - b.Weekly.UsedAt(now)) / hours until b.Weekly.ResetAt`.
   An unknown weekly window (nil budget, zero reset) has urgency 0.
3. Ties keep lease-file order.

Worked example, 23:00, both lent open, keep 20 for claude and 0 for codex, no
active windows:

- claude: weekly 40% used, resets in 96h → keep 20 × 96/168 = 11.4% → room
  48.6% → urgency 0.51%/h;
- codex: weekly 10% used, resets in 24h → keep 0 → room 90% → urgency 3.75%/h.

Codex goes first. When its session opens a window, codex stays first by rule 1
until that window is spent, and claude goes next.

## 8. Who runs a session

`Loop.pick` keeps its order, with the night step replaced:

1. the unit's own agent (`l.own`), never handed over;
2. unless the run refused the subscriptions (§9): the first lent subscription
   with room, in §7 order;
3. the secondary, while the bedrock is limited;
4. the bedrock.

Nothing waits for a subscription. `nightWake` (renamed `lendWake`) only
shortens a wait the bedrock is already in (peak hours, a usage limit). It does
that when a lease begins, a subscription's limit lifts, or a spent window
resets, as today. A subscription that hits its usage limit or cannot run hands
over without waiting, as today. `sayPick` and the `night` record kind stay, and
their texts say "lent" where they say "away".

`history.Record.Away` and `loop` stats' `Away` keep their JSON names. They now
mean that the session ran on a lent subscription; today they mean it started
inside the away span. `history.Estimate`'s clean preference is unchanged.

`Options.Night` becomes `Options.Lending *Lending`, and `Steer.Night` becomes
`Steer.Lending`. A nil `Lending` means nothing is lent, or the run refused.

## 9. Which subscriptions a run uses, spending all

A run's *subscription set* says which lent subscriptions it may use:

- `all` (the default): every lent one;
- `none`: the bedrock only;
- harness names, such as `claude` or `claude codex`: only those, if lent.

Order inside the set is always §7's, and the bedrock is last.

- Plan front matter key `subscriptions` takes `all`, `none`, or space-separated
  harness names. It is added to `plan.Settings`. Lint rejects an empty value and
  names that are not harnesses ploopy knows (`plan.Harnesses`). Whether a named
  harness is metered is checked when the run starts.
- `ploopy run --subscriptions all|none|claude,codex` and `--spend-all`.
- `control.Settings` gains `Subscriptions string` (`""` unset, else `all`,
  `none`, or space-separated names) and `SpendAll *bool`. Both are laid over by
  `Over` and rendered by `Describe` ("bedrock only", "claude only", "spending
  all of the week"). `ploopy adjust --subscriptions …` and
  `--spend-all=true|false`.
- `steering.Steer()` builds `Lending` from the lease file each time it is asked
  (the file is small, and a pick happens once per session). It keeps only the
  leases in the run's set, leaves out the bedrock's own harness, and sets
  `SpendAll`. An empty result is nil.
- A run without steering (the registry was unreachable) builds `Lending` once,
  at the start.

## 10. The board

A bubbletea program in `internal/ui` (`ui.Board`), replacing `ui.Form` for run
settings and `adjust`. `ui.Form`/`ui.Pick` stay for everything else.

```
ploopy · PASS_FIXES on pass-fixes · 12 open units
──────────────────────────────────────────────────────────────────────────────
 1 Bedrock             2 claude                3 codex               4 Run
 a agent  opencode     u lent  until 09:00     u lent  until I say   s subs  use lent
 m model  ds-flash     m model sonnet          m model gpt-6.1-sol   w week  keep reserve
 e effort high         e effort high           e effort high         p push  yes
 l limit  wait         k keep  20% → 6%        k keep  0%            f end   nothing
 o peak   wait           5h 49% → 22:10          5h 60% → 20:12
                         week 51% → Tue          week 9% → Thu
──────────────────────────────────────────────────────────────────────────────
 ≈ codex 5 · claude 7 · opencode 0 units · ≈ $0.00
 enter start   1–4 column   letter next   shift+letter back   +/- hour   esc
```

**Model.** `ui.Board` takes a title and columns. A column has a title and
fields, and a field has:

- a key letter;
- a label;
- choices as `[]ui.Choice`, re-read through a func each time, as `ui.Field` does
  with `Binding`;
- a pointer to its value;
- an optional `Locked` reason, such as "from a flag" or "from the plan".

A column may also carry read-only lines (the window status). The board takes a
`Footer func() []string`, called after every change, and returns when the
author starts or cancels. A cancel returns `huh.ErrUserAborted`, so callers keep
their handling.

**Keys.**

- `1`…`9` and Tab/Shift-Tab focus a column. Focus stays until changed, and
  starts on column 1.
- A field's letter cycles that field of the focused column to its next choice,
  and Shift+letter to the previous one. Letters repeat across columns (every
  subscription column has `m`, `e`, `u`, `k`).
- Up/down move a field cursor inside the focused column, and left/right cycle
  the field under the cursor, for those who prefer arrows.
- In a subscription column, `+`/`-` move the lease's end hour by one hour (the
  end for until, the span's end for nightly), and `[`/`]` move a nightly span's
  start hour by one hour.
- Enter starts (in `adjust`: applies). Esc, `q` and ctrl-c cancel.
- A key on a locked field shows its reason in the help line and changes nothing.

**Layout.** Columns sit side by side when the terminal is wide enough for all of
them (a `tea.WindowSizeMsg`). Otherwise they stack vertically, in order. The
focused column's title is highlighted, and so is the field under the cursor.
lipgloss styles only; colours follow `huh.ThemeBase16` so both screens look
alike.

**Columns for a run.**

- Bedrock: `a` agent, `m` model, `e` effort, `l` on a usage limit (the
  secondary), `o` a unit due in peak hours (shown only when some harness has
  peak hours, as today).
- One column per subscription harness other than the bedrock's:
  - `u` lent, cycling mine → until <hour> → nightly <span> → until I reclaim.
    The first until offered is the next 09:00, and the first nightly is
    21:00–09:00, or what the current lease says;
  - `m` model and `e` effort;
  - `k` keep, cycling 0, 10, 20, 30, 40, 50, 100, plus the current value when it
    is not among them;
  - read-only lines: its windows as `windowsNow` reads them.
- Run:
  - `s` subscriptions, cycling the set (§9): all lent → each subscription alone →
    bedrock only. With Claude and Codex that is: all, claude only, codex only,
    bedrock only. A subscription column outside the set is dimmed and titled
    "not this run";
  - `w` week (keep reserves / spend all);
  - `p` push (only when the branch can be pushed, as today);
  - `f` when the run ends.

Changing the bedrock's agent to a subscription harness removes that harness's
column. A given setting (a flag, or front matter) shows locked. On start, the
board writes the leases it changed to the lease file, and the run settings go
to `last-run.json` as today. The board opens prefilled from the last run and
the leases, so starting like last time is one key: Enter.

**Footer forecast.** It is computed after every change, from
`loadHistory`, `windowsNow` and the plan's open units `U`. For each lent
subscription, in §7 order, with `est := history.For(records, label)`:

- not lent, or `!est.Metered()`: it shows `X ?` and takes no units;
- `rest := 1 - b.Session.Used` when its window is active and resets inside the
  lease, else 0;
- `fresh :=` the five-hour windows that fit between the later of now and the
  active window's reset, and the lease's end. For an open lease this is
  unbounded. For nightly it is the current or next span only;
- `room := 1 - EffectiveKeep - weeklyUsed`, and by the week
  `room / est.WeeklyPerWindow()` windows (unbounded when that is 0);
- `units := min(U, round(min(rest + fresh, by the week) × est.UnitsPerWindow())))`,
  then `U -= units`.

The bedrock takes the remaining `U`, and the cost is `U × its est.CostUSD` when
known. Its line renders as above, followed by `· done ≈ HH:MM` when every
estimate has `Minutes`. `cmd/ploopy/night.go`'s `propose` is the arithmetic
being generalised. It goes when the board lands.

**adjust on the board.** The same columns. The bedrock's agent, model and
effort are locked ("a running run keeps its agent"); its `l` and `o` stay live.
The Run column adds `x` stop after the current unit. Enter applies the run
settings through `control.Adjust` and writes the leases, which reach every run
on the machine. `pickRun` still asks which run when several go on.

## 11. What goes away

- `--allow-dirty` (hidden, deprecated, no effect) and `Options.AllowDirty`.
- `--away`, `--night` and `--weekly-reserve` on `run` and `adjust`, hidden and
  deprecated with "lend subscriptions with `ploopy lend`". They have no effect.
- `runSettings.Away/Night/Reserve`. Old `last-run.json` files still load:
  unknown JSON fields are ignored.
- `control.Settings.Away/Night/Reserve`, `control.NightAgent`. The status files
  are ephemeral, so there is no compatibility to keep.
- `awayPresets`, `awayChoices`, `askNight`, `windowChoices`, `nightSuffix`,
  `parseNight`, `toLoopNight`, `propose` in `cmd/ploopy/night.go`, as the units
  that replace them land. `windowsNow`, `loadHistory`, `historyPath` and
  `printEstimates` stay while something uses them.
- The "like last time / change the settings" question and `askSettings`.
- The README's "While you are away" section becomes "Subscriptions", describing
  the bedrock, leases, keep, the order and refusal.

## 12. The sandbox

**Fake agents**: `dev/fakeagent`, one `package main` binary that answers as
whichever of `claude`, `codex` and `opencode` it was invoked as
(`filepath.Base(os.Args[0])`).

- **Formats.** It emits what each adapter parses, as the adapter's own tests
  feed it:
  - Claude: stream-json with assistant text, a `rate_limit_event` whose
    `rate_limit_info` carries `five_hour` and `seven_day`, and a result event
    with cost and turns;
  - Codex: `exec --json` events with a thread id and token usage, plus
    `app-server` answering `initialize` and `account/rateLimits/read` over
    JSON-RPC on stdin/stdout;
  - opencode: its `--format json` steps.

  A usage limit is answered in each harness's limit format, so the adapters'
  limit parsers recognise it. Logged out is a failure to start. The prompt
  arrives the way each adapter delivers it.
- **Scenario.** It is read from `$PLOOPY_SANDBOX/scenario.toml`, one table per
  harness:

  ```toml
  [claude]
  seconds = 6                 # how long a session takes
  session_used = 0.40         # the five-hour window now
  session_resets_in = "2h"
  weekly_used = 0.51
  weekly_resets_in = "96h"
  per_session = 0.15          # share of the five-hour window one session takes
  per_session_weekly = 0.01
  cost = 0.80                 # dollars a session reports
  outcome = "done"            # done | blocked | nothing | red | dirty | limit | logged-out

  [codex]
  seconds = 6
  weekly_used = 0.10
  weekly_resets_in = "24h"    # no session window: the next session opens one
  per_session = 0.20
  per_session_weekly = 0.02
  outcome = "done"

  [opencode]
  seconds = 4
  cost = 0.05
  outcome = "done"
  ```

  Times are relative to when the scenario is read, so leases and nightly spans
  are tried against the real clock: lend Claude nightly over a span that
  includes now.
- **State.** It lives in `$PLOOPY_SANDBOX/state.json`, with absolute reset
  instants and used shares. It is seeded per harness from the scenario on first
  use, and seeded again whenever `scenario.toml` is newer than the state. Each
  session adds its `per_session` shares. A window whose reset has passed
  restarts at 0, with a new reset five hours (seven days) on. A window that
  reaches 1 answers the next session with a usage limit until it resets.
  `app-server` reads the same state without changing it.
- **Roles.** The fake tells them apart by the prompt's opening line:
  - an implementing unit appends `unit <id> by <harness>` to `work.txt`, commits
    `Do unit <id>`, writes the handover to the path the prompt names, and ends
    `DONE`;
  - `red` also creates and commits `BROKEN`, which the toy repository's verify
    rejects;
  - `dirty` leaves `work.txt` changed and uncommitted;
  - `blocked` and `nothing` end with those markers and change nothing;
  - a collecting review session writes `No findings.` to the findings path the
    prompt names, commits nothing, and ends `DONE`;
  - a triage writes a two-unit `<NAME>_FIXES.md` and an `AuthorHeading` section
    at the end of the review plan (the paths come from the prompt and
    `internal/review`), commits both, and ends `DONE`.

**The sandbox**: `dev/sandbox`, `package main`, run as `go run ./dev/sandbox
[DIR]` and as the flake app `nix run .#sandbox`. It:

1. Builds ploopy and the fake into `DIR/bin`, with `claude`, `codex` and
   `opencode` as links to the fake. DIR is a new temp directory when not given.
2. Creates `DIR/repo`: `git init` on `master`, an `AGENTS.md` of two lines, a
   `.ploopy.toml` with `verify = "test ! -e BROKEN"`, and a toy plan
   `docs/plans/TOY.md` of eight one-line units, all committed.
3. Writes the default scenario above to `DIR/scenario.toml`.
4. Execs `$SHELL` in `DIR/repo`, with `PATH` starting with `DIR/bin`,
   `XDG_STATE_HOME` and `XDG_RUNTIME_DIR` inside DIR, and `PLOOPY_SANDBOX=DIR`.
   The author's real leases and runs are never touched.
5. Prints a cheat sheet first: where the scenario is, `ploopy lend …`, `ploopy`,
   `ploopy review TOY`.

`dev/` is outside the flake's `subPackages`, so release builds carry neither
binary.

## 13. Review on the board, then the fix plan

**One board.** `ploopy review` builds one board: the run's columns (§10) for
the collecting sessions, and a Triage column before the Run column:

- `a` agent, `m` model (strongest first, `triageModels`), `e` effort,
  `b` spending cap (`budgetChoices`);
- the Run column gains `n` then: *stop* / *run the fix plan*.

This replaces both prompts of a review today: the triage's own page
(`chooseTriage`'s keep-or-change pick and `askTriage`), and the run settings
asked afterwards. `chooseTriage` still resolves flags, the review's current
triage, the last triage and the default, and the board shows the result. The
board's values then go to the review's run without asking again.

- **A review already written whose triage has not run:** the Triage column is
  prefilled "as this review has it". Changing it commits as `retriage` does.
- **A review whose triage has run:** the column is locked ("the triage has
  run").
- **The triage's agent** is the triage unit's own agent. It runs as named and is
  never handed over, whatever the leases and the subscription set.

**Then the fix plan (D14).** `ploopy review --then stop|fixes` (default
`stop`), and `control.Settings.Then` (`""`, `stop`, `fixes`), so
`ploopy adjust --then fixes` and the adjust board (`n`) change it mid-run.
When the review run ends complete, its fix plan exists, and Then is `fixes`:

1. `reviewOutcome` is printed as today.
2. The fix plan runs in the same worktree, as a run of its own, announced in
   the registry. It uses the review's resolved settings, except those the fix
   plan's front matter gives. No board is shown.
3. Pushing, leaving the worktree and the end action happen once, after the fix
   plan's run. The review's run skips them.

In every other case (the review stopped, failed, or wrote no fix plan) the
review ends as today. A stop signal ends whichever run is going, and the fix
plan does not start after a stopped review.

## 14. Package placement

- `internal/lease` (new): the lease file. It must not import `loop` or
  `harness`. It is added to `AGENTS.md`'s package list.
- `internal/loop`: `Lease`, `Lending`, `Room`, `EffectiveKeep`, the order, and
  the pick.
- `internal/ui`: the board. Nothing else writes to the terminal.
- `cmd/ploopy`: wiring. It turns lease-file entries into `loop.Lease`, the board
  into settings, and holds the `lend` and `reclaim` commands.
- `github.com/charmbracelet/bubbletea` moves from indirect to direct in
  `go.mod`, and `AGENTS.md`'s dependency line names it.
- `dev/fakeagent` and `dev/sandbox` (§12): development tools, not in the
  release build. `AGENTS.md` gains a line about them under *Tests*.


