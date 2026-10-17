---
context: README.md
---

# Lending: execution

Status: active task document, written 2026-10-17 against `6676ee0`.

The authority is `docs/plans/LENDING.md`, and every session reads it. Units cite
its sections as §N and its decisions as Dn. When a unit and the authority
disagree, the authority wins. Say so in the handover.

This repository has no `.ploopy.toml`, so the loop runs no checks of its own.
Before committing, run the verification block of `AGENTS.md`:
`go build -o /dev/null ./...`, `go vet ./... && gofmt -l .` (which must print
nothing), and `go test ./...`.

Every unit that changes what the author sees, such as flags, commands, messages
or screens, updates the README section that describes it in the same commit. The
loop's tests in `internal/loop` are the specification of the judge. Change one
only where the authority changes the contract (D1, D5), and say which in the
commit body.

## Stage 1 — Uncommitted changes go to the session

### 1.1 Hand uncommitted changes to the session instead of refusing

**Goal:** A run of an implementing plan never refuses a dirty worktree. Each
session that starts on uncommitted changes is told what they are and whose they
are, as §3 specifies. A red preflight on inherited changes goes to the first
session instead of stopping the run. `--allow-dirty` becomes a hidden,
deprecated no-op, and `Options.AllowDirty` is deleted.

**Start from:**
- `Loop.checkTree` and `Loop.preflight` in `internal/loop/loop.go`;
- the attempt loop that calls `l.prompt` (search for `prompt, err := l.prompt`);
- `renderPrompt` and `promptArgs` in `internal/loop/prompt.go`, and the template
  `internal/loop/prompt.md`;
- the `allow-dirty` flag in `cmd/ploopy/run.go` (hide and deprecate it as
  `worktree` is);
- the existing test near `a rerun after a stop needed --allow-dirty` in
  `internal/loop/loop_test.go`, which must keep passing.

Review plans keep refusing a dirty tree at the start. Their refusal no longer
mentions `--allow-dirty`.

**Done when:**
- `rg -n 'AllowDirty|allow-dirty' internal` is empty, and
  `rg -n 'allow-dirty' cmd` finds only the deprecated flag registration;
- new loop tests drive a real repository through the fake harness and show:
  - a run started on uncommitted changes the loop did not leave runs its first
    unit, and that session's prompt contains `## Uncommitted changes in the
    tree`, the changed path and the "before this run started" origin sentence;
  - a retry after an attempt that left changes gets the "earlier attempt" origin
    sentence;
  - with inherited changes and a verify command that fails, the run is not
    stopped by the preflight, and the first prompt carries the verify tail;
  - a clean tree's prompt has no such section;
  - a review plan started on a dirty tree still fails the run;
- the README no longer mentions `--allow-dirty`.

### 1.2 Carry uncommitted changes into the worktree on hand-off

**Goal:** When a run moves a branch out of the main checkout into a worktree,
the branch's uncommitted changes move with it, by the stash procedure of §3. It
no longer refuses.

**Start from:**
- `env.handOff` in `cmd/ploopy/worktree.go`, which refuses today when
  `DirtLines` is not empty;
- `Repo.HandOff`, `Repo.Dirt` and `Change` in `internal/repo/repo.go`. The
  stash and pop belong in `internal/repo`, as a method or an extension of
  `HandOff`, so `cmd` makes no git calls of its own;
- `cmd/ploopy/worktree_test.go` for how hand-off is tested today.

Author paths (`AuthorPaths`) are not part of `Dirt` and must stay in the main
checkout. A pending plan is committed before the stash, as today, by
`commitPending`.

**Done when:**
- a test hands off a branch whose main checkout has a modified tracked file, a
  new untracked file and an author-path file. Afterwards the worktree has the
  first two uncommitted, the main checkout is on the default branch with only the
  author-path file changed, and `git stash list` is empty in both;
- a test where `worktree add` fails (such as an occupied target path) leaves the
  main checkout on the branch with its changes back in place, and the stash
  empty;
- the README's Worktrees bullet about uncommitted changes describes the new
  behaviour.

## Stage 2 — Leases

### 2.1 The lease file

**Goal:** A new package `internal/lease` reads and writes the machine's lease
file exactly as §4 specifies: path, format, kinds, one lease per harness in name
order, expired until-leases read as absent, atomic replacement. Nothing uses it
yet.

**Start from:**
- §4 of the authority;
- `control.Dir` and `control.writeJSON` in `internal/control/control.go`, for
  the path style and the atomic write. Copy the write rather than exporting it:
  the packages stay independent;
- `AGENTS.md`'s *Package boundaries*. Add `internal/lease` there: "the
  machine's lease file: which subscriptions the author lends, on what terms.
  Must not import `loop` or `harness`."

Suggested API: `Path() string`, `Load() (File, error)`, `Save(File) error`,
`(File) Set(Lease) File`, `(File) Reclaim(harness string) File`,
`(File) Active(now time.Time) []Lease`. Tests set `XDG_STATE_HOME` to a temp dir.

**Explicit non-goals:** no commands, no wiring into runs, no span parsing (the
span is a string here).

**Done when:**
- `go test ./internal/lease` covers:
  - a missing file;
  - a round trip of all three kinds;
  - replacing and reclaiming;
  - name order;
  - an expired until-lease left out of `Active` but still in the file;
  - an invalid file's error naming the path;
  - the fallback to `$HOME/.local/state` when `XDG_STATE_HOME` is unset;
- `rg -n '"github.com/Torwalt/ploopy/internal/(loop|harness)"' internal/lease`
  is empty.

### 2.2 The loop runs on leases

**Goal:** `internal/loop` replaces `Night`/`NightAgent` with `Lease`/`Lending`
and decides room by §5 and who runs a session by §8. For now the keep is the
lease's own (`SpendAll` gives 0), and leases go in their given order: the next
unit adds the shrinking keep and the order. `cmd/ploopy` keeps compiling and
behaving as before, through a bridge that turns today's away span and night
agents into nightly leases.

**Start from:**
- `internal/loop/night.go`: `Night`, `NightAgent`, `Night.Room`, `nightPick`,
  `nightWake`, `readMeters`, `meter.enter`;
- `Loop.pick`, `Options.Night`, `Steer.Night` and the `info.Away` assignment
  (search `night.Away.Span(info.Started)`) in `internal/loop/loop.go`;
- the bridge sites in `cmd/ploopy`:
  - `toLoopNight` in `night.go`;
  - `steering.Steer` in `steer.go`;
  - `resolveSettings` in `settings.go`;
  - the `Night:` field of `loop.Options` in `run.go`.

  The bridge maps `away` plus each night agent with `Windows >= 0` to
  `Lease{Kind: nightly, Span: away, Keep: reserve}` and ignores the window
  count;
- `internal/loop/night_test.go`.

**Concrete work:**
- the types and the span table of §5, `Lending.Room` with the five rules in
  order, and `lendPick`/`lendWake` (renamed from `nightPick`/`nightWake`);
- `meter.enter` keyed per §5;
- `Away` in stats and history set when the session ran on a lease (§8).

**Tests:**
- rewrite `night_test.go` around leases. D5 changes the contract: the
  fresh-window-count tests (`TestOnlyTheApprovedFreshWindowsAreOpened`,
  `TestMovingTheSpanDoesNotOpenTheApprovedWindowsAgain`) are replaced, not
  kept;
- keep the behaviour every other test there pins, and add:
  - an open lease that opens consecutive fresh windows;
  - an until-lease refusing a fresh window that would run past it;
  - an until-lease that uses up a window the author opened which resets before
    the lease ends, and refuses one that resets after;
  - a passed until-lease that lends nothing;
  - with every lease out of room, the bedrock runs the unit with no wait.

**Done when:** `rg -n '\bNight\b|NightAgent' internal/loop` is empty, the tests
above pass, and `go test ./...` is green.

### 2.3 The shrinking reserve and the order among subscriptions

**Goal:** `EffectiveKeep` (§6) decides rule 3 of `Lending.Room`, and `lendPick`
orders the leases with room by §7.

**Start from:**
- `Lending.Room` and `lendPick` in `internal/loop/night.go`. Rename the file to
  `lending.go` if you like, and its test file with it;
- §6 and §7, including their worked examples.

**Done when:**
- table tests reproduce every row of §6's table and the 100%-keep case;
- a test reproduces §7's worked example (codex before claude), and one where
  claude has an active window and goes first despite lower urgency;
- an unknown weekly budget sorts last among fresh candidates;
- a loop test with two open leases shows the units going to the one §7 puts
  first, and then to the other once the first's window is spent.

### 2.4 Runs read the machine's leases; `lend` and `reclaim`

**Goal:** Lending comes from the lease file (§4), not from run settings. The
away span, night agents and reserve leave the settings, the flags, the control
registry and the settings pages (§11). `ploopy lend` and `ploopy reclaim` exist
as §4 specifies.

**Start from:**
- `cmd/ploopy/night.go`, `settings.go`, `steer.go`, `adjust.go`, `run.go` and
  `main.go`, where commands are registered;
- `control.Settings` and `control.NightAgent` in `internal/control/control.go`;
- §4, §8 and §11.

**Concrete work:**
- `steering.Steer()` builds `Lending` from `lease.Load` and `Active(now)` each
  time, as §9's bullet says, leaving out the bedrock's harness. A run without
  steering builds it once at start. A broken lease file is said once in the feed
  and lends nothing;
- remove `runSettings.Away/Night/Reserve`, `given.away/night/reserve`, the away
  field of `askSettings`, `askNight` and everything only it used, and
  `control.Settings.Away/Night/Reserve` and `control.NightAgent`;
- the away/night entries of `adjustInteractively` go;
- `--away`, `--night` and `--weekly-reserve` on `run` and `adjust` stay
  registered, hidden and deprecated, with no effect;
- until the board lands, the settings page prints the active leases above its
  questions, one line each (§6's keep notation), followed by "change them with
  `ploopy lend`";
- `ploopy lend` (list, or set with `--until`/`--nightly`/`--open`, `--keep`,
  `--model`, `--effort`) and `ploopy reclaim <harness>|all`, both from any
  directory, like `adjust`;
- the chooser in `main.go`'s `chooseCommand` gets an entry for `lend`;
- the README's "While you are away" section becomes "Subscriptions" (§11).

**Tests:**
- in `cmd/ploopy`, with `XDG_STATE_HOME` and `XDG_RUNTIME_DIR` set to temp
  dirs:
  - lend then list;
  - `--until 09:00` resolves to the next 09:00;
  - a non-metered harness is refused;
  - a bad nightly span is refused;
  - `reclaim all`;
  - `steering.Steer()` reflects a lease written after the run started and drops
    the bedrock's harness;
- `cmd/ploopy/night_test.go`'s tests of removed functions go with them.

**Done when:** `rg -n 'askNight|parseNight|awayPresets|NightAgent|Reserve \*int' cmd internal`
is empty, the tests pass, and `ploopy lend --help` and `ploopy reclaim --help`
describe §4.

### 2.5 A plan or a run refuses the subscriptions; spend all

**Goal:** §9: the `subscriptions` front-matter key, `--no-subscriptions` and
`--spend-all` on `run`, `Subscriptions`/`SpendAll` in `control.Settings` with
`adjust` flags, and the effect on `Steer`. The settings page offers both as
fields until the board replaces it.

**Start from:**
- `plan.Settings` and the `test:` lint in `internal/plan/plan.go`;
- `givenSettings`, `askSettings`, `describe` and `runSettings` in
  `cmd/ploopy/settings.go`;
- `control.Settings.Over/Describe`, `changeFromFlags` in `adjust.go`, and
  `steering.Steer`.

A plan's `subscriptions: no` counts as given, as front-matter agents do today.
It is not asked, and the run says "the plan runs on the bedrock only" at start.

**Done when:**
- a plan test lints `subscriptions: maybe` as an error;
- a cmd test shows a plan with `subscriptions: no` and an open lease getting a
  nil `Lending`;
- `--spend-all` sets `SpendAll`;
- `control` tests cover `Over` and `Describe` for both new fields;
- the README documents the key and both flags.

## Stage 3 — The board

### 3.1 The board component

**Goal:** `ui.Board` in `internal/ui`, a bubbletea program with the model, keys
and layout of §10, independent of any ploopy command. bubbletea becomes a direct
dependency, and `AGENTS.md`'s dependency line names it.

**Start from:**
- `internal/ui/form.go` (`ui.Field`, `FormNote`) for the style of API and the
  theme;
- `huh.ThemeBase16` for colours;
- the bubbletea version already indirect in `go.mod`. Use it, do not upgrade.

**Explicit non-goals:** no command uses the board yet.

**Done when:**
- unit tests drive the model through `Update` with key messages, without a
  terminal, and cover:
  - column focus by number and by Tab;
  - a letter cycling forward, and Shift+letter back;
  - a repeated letter acting only in the focused column;
  - arrows;
  - a locked field unchanged, with its reason in the view;
  - `+`/`-` and `[`/`]` on a time field;
  - the footer re-computed after a change;
  - Enter returning the values;
  - Esc returning `huh.ErrUserAborted`;
- a view test at width 120 shows the columns side by side, and at width 60
  stacked;
- `go.mod` lists bubbletea without `// indirect`.

### 3.2 Run settings on the board

**Goal:** `chooseSettings` puts everything on one board, per §10's columns for a
run, with the forecast footer. This replaces:
- the "like last time / change the settings" pick;
- `askSettings`;
- the lease lines printed above the page;
- `printEstimates` above the page.

On start the leases changed on the board are written to the lease file, and the
run settings saved as today.

**Start from:**
- `chooseSettings`, `askSettings`, `fill` and `given` in `cmd/ploopy/settings.go`;
- `windowsNow`, `loadHistory` and `printEstimates` in `cmd/ploopy/night.go`;
- `history.For`, `Estimate.UnitsPerWindow` and `WeeklyPerWindow`;
- `loop.EffectiveKeep` and the §7 order (export what the forecast needs from
  `internal/loop`, without duplicating the arithmetic);
- §10, especially *Footer forecast*.

**Concrete work:**
- the forecast is a pure function in `cmd/ploopy` (or `internal/history` if it
  needs nothing from `loop`), tested on its own;
- given settings show locked with their source;
- a non-interactive run (`unattended`) is unchanged.

**Done when:**
- `rg -n 'askSettings|"change the settings"' cmd` is empty;
- forecast tests cover:
  - an open lease limited by the week;
  - an until-lease limited by fresh windows;
  - a subscription without an estimate shown as `?`;
  - the bedrock taking the rest with its cost;
- the README's *Start* section shows the board in place of the old pages.

### 3.3 `ploopy adjust` on the board

**Goal:** `adjustInteractively` shows the board with §10's *adjust* columns, and
Enter applies the run settings through `control.Adjust` and writes the leases.
`pickRun` still chooses the run first.

**Start from:**
- `adjustInteractively`, `apply` and `pickRun` in `cmd/ploopy/adjust.go`;
- the board fields that 3.2 built for a run. Share the column builders between
  run and adjust, not copies of them.

**Done when:**
- a test builds the adjust columns for a live run and shows the bedrock's agent,
  model and effort locked and `x` stop present;
- a test applies a board result and reads back the changed `control.Settings`
  and lease file;
- the README's adjust paragraph matches.

### 3.4 One-key shortcuts in the main menu

**Goal:** `ploopy` with no arguments offers its commands with a shortcut letter
each, shown in the label. Pressing the letter picks at once, and arrows plus
Enter still work. Suggested letters: `r` run, `v` review, `s` status, `p`
report, `h` history, `c` close, `l` land, `n` lend, `w` show, `y` replay,
`m` mark, `t` lint, `a` adjust (only when a run goes on).

**Start from:**
- `chooseCommand` in `cmd/ploopy/main.go`;
- `ui.Pick` in `internal/ui/select.go`. Add a `ui.PickKeyed` (or a `Key` on
  `ui.Choice`) built on bubbletea, or on the board's building blocks from 3.1.
  Leave `ui.Pick`'s other callers as they are.

**Done when:**
- a model test shows a letter picking its command, an unknown letter doing
  nothing, and Enter picking the highlighted one;
- the README's *Start* listing shows the letters.

## Notes for the author

- No session sees this section.
- The repository has no `.ploopy.toml`, so the loop judges units only by commits
  and a clean tree, and the sessions run the checks themselves. A `.ploopy.toml`
  with `verify = "go build -o /dev/null ./... && go vet ./... && test -z \"$(gofmt -l .)\""`
  and `test = "go test ./..."` would make the loop check them too. Add it before
  running if you want that.
- Units 2.2 and 2.4 are the largest. If one blocks, split it with new ids; do
  not renumber.
