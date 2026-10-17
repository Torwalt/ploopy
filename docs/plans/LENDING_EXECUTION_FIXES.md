---
test: yes
context: docs/plans/LENDING.md README.md
---

# Fixes from the review of LENDING_EXECUTION

Status: active task document, written 2026-10-17 against `69fafcc` by the triage
of `docs/plans/LENDING_EXECUTION_REVIEW.md`.

Each unit fixes a defect a review found and the triage confirmed. Any design
decisions below are the triage's recommendations. This plan implements them as
written unless the author revises the plan before running it.

`docs/plans/LENDING.md` is the authority for the work being fixed; units cite
its sections as §N and its decisions as Dn. `.ploopy.toml` runs the
verification block of `AGENTS.md` after every unit. A unit that changes what the
author sees updates the README section describing it in the same commit. The
loop's tests in `internal/loop` are the specification of the judge: add to
them, and change an existing one only where a unit below says so.

## Recommended design decisions

### F-D1 — A failed pop in the new worktree undoes the hand-off

**Choice:** when `git stash pop` fails inside the worktree a hand-off just
made, `env.handOff` removes that worktree (discarding whatever the failed pop
half-applied there), switches the main checkout back to the branch, pops the
stash there, and returns the original error. The changes end where they were
before the run: on the branch, in the main checkout. The default branch's
checkout never receives them.

**Why:** §3 step 4 already says a failed step pops the stash back in the main
checkout "after switching it back to the branch", but it was written for a
failed `HandOff`, which switches back by itself. After a successful `HandOff`
the branch is checked out in the new worktree, so the main checkout cannot
switch back until that worktree is gone. Undoing the hand-off is the only
recovery that keeps §3's rule; the worktree holds nothing but the half-applied
pop, because it was created a moment earlier at the branch's HEAD.

### F-D2 — The board never changes a value the author gave

**Choice:** the settings board's `normalize` and `normalizeTriage` correct only
values that were not given by a flag or the plan's front matter. Changing the
agent resets the model and effort to the new harness's defaults only when they
were not given; a given model or effort keeps its value and its lock. An
invalid combination of given values is refused on Enter by the same check that
refuses it when the flags are given together (`resolveSettings` for a run, the
triage's own resolution for the Triage column). `normalize` runs once before
the board is first rendered, as it did before the review board landed, so values
that came from the last run are corrected before the author sees them.

**Why:** the README's *Start* says a given setting "is locked and says where it
came from", and §10 says a key on a locked field changes nothing; silently
replacing the value behind a lock contradicts both. Refusing an invalid
combination on Enter is what the flags already do without a board.

Every unit:

- first shows the defect with a test that fails, where a test can show it, then
  fixes it and keeps the test;
- makes the smallest change that fixes the defect, in the code the unit names;
- implements cited recommended decisions as written and never makes another
  design decision itself; when the fix turns out to need one the plan does not
  make, it finishes BLOCKED and names the decision.

## Stage 1 — Fixes

### 1.1 Take the fake agent's handover path from the session block

**Goal:** `dev/fakeagent` writes the handover to the path the prompt's
`## This session` block names, even when the skill text before it or the
previous handover after it also contains `Write the handover to `.

**Evidence:** `dev/fakeagent/act.go:35` — `quotedAfter` takes the *last*
`Write the handover to ` anywhere in the prompt. `internal/loop/prompt.md`
puts the session's own path in the `## This session` block, then inserts the
previous unit's handover under `## Handover from the previous session`. A
previous handover quoting the phrase (as the real 1.2 handover does) makes the
fake write a file named after work-order text into the repository and leave
the tree dirty; with `strings.Index`, the `plan-unit` skill's own mention
before the block made it write a file named `DONE`.
`TestImplementingAndCollectingRoles` (`dev/fakeagent/main_test.go`) builds a
prompt with neither, so it passes with any lookup.

**Start from:** `quotedAfter` and `implement` in `dev/fakeagent/act.go`;
`TestImplementingAndCollectingRoles` in `dev/fakeagent/main_test.go`;
`internal/loop/prompt.md`.

**Done when:** a test builds an implementing prompt with the phrase both before
the `## This session` block and inside a `## Handover from the previous session`
section after it, runs the fake in a temp repository, and asserts the handover
exists at the session block's path and `git status --porcelain` is empty; it
fails before the change and passes after; `go test ./dev/fakeagent` is green.

### 1.2 Report the weekly window when it is the one the fake has spent

**Goal:** when a scenario's weekly window has run out, the fake's usage-limit
answer leads each adapter to wait until the weekly reset, not the five-hour one
or the next occurrence of a clock time.

**Evidence:**
- `dev/fakeagent/speak.go:62-85` (`claudeInfo`) — with `[claude]`
  `session_used = 0.40`, `session_resets_in = "2h"`, `weekly_used = 1.0`,
  `weekly_resets_in = "96h"`, `spent` returns the weekly reset, but the
  rejected event's `resetsAt`, `rate_limit_info.resetsAt` and `rateLimitType`
  come from `h.Session.ResetAt` and a hardcoded `five_hour`. The Claude
  adapter takes the reset from `rate_limit_info.resetsAt`, so the outcome's
  `RateLimit.ResetAt` is about two hours away while the week resets in 96h. It
  should say `seven_day` and the weekly reset when the weekly window is the
  spent one.
- `dev/fakeagent/speak.go:184-191` (`speakCodexLimit`) — the message says only
  `It resets at 12:42pm (UTC).`; `harness.RateLimitFromText` reads the next
  such clock time, so a 96h weekly reset becomes about 24h. When the reset is
  not within the next 24 hours, the message should omit the `resets at` clause
  so the Codex adapter's `resetOf(outcome.Budget)` fallback
  (`internal/harness/codex/codex.go:258-260`) uses the weekly reset.

§12 *State*: a window that reaches 1 answers with a usage limit "until it
resets".

**Start from:** `claudeInfo`, `speakClaudeLimit` and `speakCodexLimit` in
`dev/fakeagent/speak.go`; `spent` in `dev/fakeagent/scenario.go`;
`TestEachAdapterRecognisesTheSameLimit` in `dev/fakeagent/main_test.go`.

**Done when:** a test per adapter (Claude and Codex) with a spent weekly window
and an unspent five-hour window asserts the outcome's `RateLimit.ResetAt` is
within a few minutes of the scenario's weekly reset; both fail before the change
and pass after; `go test ./dev/fakeagent` is green.

### 1.3 Test every outcome of the fake and the refresh of its windows

**Goal:** the fake's behaviours that §12 specifies and no test pins get tests:
the `red`, `dirty`, `blocked`, `nothing` and `logged-out` outcomes, a window
whose reset has passed, and reseeding when the scenario is newer than the
state.

**Evidence:** `dev/fakeagent/main.go:90`, `dev/fakeagent/act.go:79-90`,
`dev/fakeagent/scenario.go:132-153` — the tests exercise only `done` and
`limit` and seed the state once. Making `act` run for `blocked`, dropping the
`BROKEN` commit for `red`, leaving `Used` unchanged in `Window.refresh`, or
breaking `openState`'s modification-time comparison leaves every test green.

**Start from:** `dev/fakeagent/main_test.go` (`TestImplementingAndCollectingRoles`,
`TestEachSessionAddsItsShare`); `dev/fakeagent/main.go`, `act.go`,
`scenario.go`; §12 *State* and *Roles*.

**Done when:** `go test ./dev/fakeagent` is green with tests that assert:
`red` commits `BROKEN`; `dirty` leaves `work.txt` modified and uncommitted;
`blocked` and `nothing` end with their markers and leave `HEAD` and the tree
unchanged; `logged-out` exits nonzero and changes nothing; a state whose
five-hour reset is in the past reports `Used` 0 and a reset about five hours on
(seven days for the weekly window); editing `scenario.toml` after a session
reseeds the state. Each test fails when the code path it names is removed.

### 1.4 Resolve the sandbox app's directory against the caller's

**Goal:** `nix run .#sandbox -- ./my-sandbox` prepares `./my-sandbox` in the
directory the author ran it from, as `go run ./dev/sandbox ./my-sandbox` does.

**Evidence:** `flake.nix:61-64` — the wrapper runs `cd ${self}` (the flake
source in the read-only Nix store) before `exec go run ./dev/sandbox "$@"`, so
the sandbox's `filepath.Abs` resolves a relative DIR inside the store and
`os.MkdirAll` fails.

**Start from:** the `sandbox` app in `flake.nix`.

**Done when:** the wrapper turns a relative DIR argument into an absolute path
against the caller's working directory before changing directory (no argument
still makes a temp directory); `nix flake check` passes; running
`nix run .#sandbox -- ./x` from a scratch directory prepares `./x` there, and the
commit body says so.

### 1.5 Keep the red preflight's tail until a session starts

**Goal:** the first session that actually starts on inherited uncommitted
changes gets the `Verify is red with these changes:` block, even when the first
agent picked could not start.

**Evidence:** `internal/loop/loop.go:292-293` — `uncommitted` clears
`l.preflightRed` whenever a prompt is rendered. When a lent agent fails to
start (`internal/loop/loop.go:860-866`), the loop benches it and `continue`s;
the next prompt, the first one a session receives, has no tail. §3: "the
failing tail of the verify log goes into the first session's prompt".

**Start from:** `uncommitted` and the session start in `Loop.run` in
`internal/loop/loop.go`; `TestAnInheritedRedPreflightGoesToTheFirstSession` and
`TestANightAgentThatCannotRunHandsOver` in `internal/loop`.

**Done when:** a new loop test combines an inherited red preflight with a lent
agent whose step fails to start, and asserts the bedrock's first prompt
contains `Verify is red with these changes:`; it fails before the change
(clearing `preflightRed` only after a session has started) and passes after;
`go test ./internal/loop` is green.

### 1.6 Pin the origin sentence of a unit resumed after a stop

**Goal:** a run that resumes a unit an earlier run stopped mid-session, leaving
it dirty, tells the session "An earlier attempt at this unit left these
uncommitted."

**Evidence:** `internal/loop/progress.go:67-75` keeps `LeftDirty` across
`progress.reset` for the same unit, which feeds the origin choice at
`internal/loop/loop.go:298`. Reverting that hunk leaves `go test
./internal/loop` green: `TestAStopMidSessionHandsItsWorkToTheNextRun` checks
only the failure text.

**Start from:** `TestAStopMidSessionHandsItsWorkToTheNextRun` in
`internal/loop/loop_test.go`.

**Done when:** that test also asserts the resumed prompt (`f.h.Prompts[1]`)
contains `An earlier attempt at this unit left these uncommitted.`; the
assertion fails with the pre-change `reset` and passes as the code is; `go test
./internal/loop` is green.

### 1.7 Carry staged deletions and renames on hand-off without a stray stash

**Goal:** a hand-off from a main checkout with a staged deletion or a staged
rename carries them into the new worktree, and leaves `git stash list` as it
was, on success and on failure.

**Evidence:** `internal/repo/repo.go:148-153`, `cmd/ploopy/worktree.go:123-134`
— for `D  gone.txt`, or `R  old.txt -> new.txt`, `handOff` passes the deleted
path to `git stash push --include-untracked -- <paths>`, which exits 1 with
`pathspec ... did not match any files` yet still records a stash entry. The
hand-off then refuses, which D2 forbids, and leaves a stash behind.

**Start from:** `StashPaths` in `internal/repo/repo.go`; `handOff` in
`cmd/ploopy/worktree.go`; `TestOtherChangesOnTheBranchMoveWithIt` in
`cmd/ploopy/worktree_test.go`.

**Done when:** a hand-off test with a staged deletion and one with a staged
rename assert the worktree holds them, the main checkout is clean on the
default branch apart from the author paths, and `git stash list` is empty; both
fail before the change and pass after; `go test ./cmd/ploopy ./internal/repo`
is green.

### 1.8 Undo the hand-off when the pop in the new worktree fails (F-D1)

**Goal:** implement F-D1: when `git stash pop` fails in the worktree a
hand-off just made, the worktree is removed, the main checkout is switched back
to the branch, and the stash is popped there; the error is returned.

**Evidence:** `cmd/ploopy/worktree.go:143-149` — after `Repo.HandOff`
succeeds, the main checkout is on the default branch; the fallback pops the
stash into it, so the branch's uncommitted changes land on the default
branch's working tree. Reproduced with a read-only worktree: ` M README.md`
and `?? half.go` ended up on `master`.

**Start from:** `handOff` in `cmd/ploopy/worktree.go`; `RemoveWorktree` and
`Switch` in `internal/repo`; `TestAFailedHandOffPutsTheChangesBack` in
`cmd/ploopy/worktree_test.go`. The recovery runs under
`context.WithoutCancel`, like the rest of the hand-off.

**Done when:** a test makes the pop in the new worktree fail (through a seam
for the pop if the filesystem cannot be made to refuse it portably) and asserts
the worktree is gone, the main checkout is on the branch with the changes, and
the default branch's checkout has none; it fails before the change and passes
after; `go test ./cmd/ploopy` is green.

### 1.9 The lease package writes only what it reads

**Goal:** `lease.Save` refuses a `File` that `lease.Load` would refuse, `Load`
refuses a document that is not a JSON object, and tests pin name order on disk
and on reading, every validation branch, and `"leases": []` for an empty file.

**Evidence:**
- `internal/lease/lease.go:84` — `Save` validates nothing, so
  `File{Leases: [{a, open}, {a, open}]}` is written and the next `Load` fails
  with `more than one lease for a`. §4: at most one lease per harness; an
  invalid file is an error.
- `internal/lease/lease.go:59-62` — a file containing `null` unmarshals into
  the zero `File`, so a corrupt file reads as "nothing lent". §4: only a
  missing file means that.
- `internal/lease/lease_test.go` — `TestLeasesAreKeptInNameOrder` feeds data
  through `Set`, which already sorts, so deleting the sort in `Save` or `Load`
  stays green; no test reaches the empty-harness, unknown-kind, keep-range or
  duplicate checks, or `Save(File{})`'s output.

**Start from:** `Load`, `Save` and `Set` in `internal/lease/lease.go`;
`internal/lease/lease_test.go`.

**Done when:** the checks `Load` makes live in one function that `Save` calls
too; `go test ./internal/lease` is green with tests that: save an unsorted
`File` and read the raw file's order; load hand-written unsorted JSON and check
the order; table-test each invalid lease through both `Load` (error names the
path) and `Save` (error, file unchanged); load `null` and get an error naming
the path; and check `Save(File{})` writes `"leases": []`.

### 1.10 Pin what lending records and when it wakes a waiting bedrock

**Goal:** tests pin `Away` meaning "ran on a lent subscription", `meter.saw`'s
handling of a fresh window reported near an until lease's end, and each reason
`lendWake` shortens a bedrock's wait; the two comments that still describe the
old meaning of `Away` say the new one.

**Evidence:**
- `internal/loop/loop.go:1034` sets `Away: lent`, but
  `TestALentSubscriptionAtItsLimitHandsOverWithoutWaiting` (inside the
  fixture's nightly span, both units on the bedrock) never checks the records;
  a span-based `Away` passes. `internal/loop/stats.go:22` and
  `internal/history/history.go:35` still say "started/ran while the author was
  away". §8.
- `internal/loop/night.go` `meter.saw` — restoring the pre-lending early return
  for until/open leases leaves every loop test green; a session that can run
  only because the reported window is the run's own (`ownWindow`) is untested.
- `internal/loop/night.go` `lendWake` — stubbing it to return the zero time
  leaves `go test ./...` green. §8: it shortens a wait when a lease begins, a
  subscription's limit lifts, or a spent window resets, and never for a unit's
  own agent.

**Start from:** `internal/loop/night_test.go`; `lendWake`, `meter.saw` and
`leaseSpan` in `internal/loop/night.go`.

**Done when:** `go test ./internal/loop` is green with: the history records of
the bedrock sessions in `TestALentSubscriptionAtItsLimitHandsOverWithoutWaiting`
asserted `Away == false`; a test where an until lease's fresh window is
reported with a reset a few minutes off the booked one and a later session in
the lease uses it; tests where a bedrock waiting out a limit or peak hours is
woken at a nightly lease's start, at a lent subscription's limit lift, and at a
spent window's reset; and the two comments updated. Each new test fails with
the mutation named above.

### 1.11 Pin §7's order and the shrinking reserve's edge cases

**Goal:** tests pin the parts of §6 and §7 that no test reaches.

**Evidence:** `internal/loop/night.go:131-145` (`EffectiveKeep`),
`:317-328` (`lendOrder`), `:350-355` (`lendUrgency`). Each of these leaves `go
test ./internal/loop` green: comparing two active windows' resets with
`.After`; breaking ties by harness name; returning 0 for a nil budget; dropping
the upper clamp of `left/7d`; ignoring `spendAll` in `lendUrgency`; treating
only a zero weekly reset as unknown.

**Start from:** `internal/loop/night_test.go`; §6 and §7.

**Done when:** `go test ./internal/loop` is green with tests that: two active
windows put the earlier reset first; two fresh leases of equal urgency keep
`Lending.Leases` order; `EffectiveKeep` with a nil budget is `lease.Keep` and
`Room` allows the lease; a weekly reset 200h away keeps `lease.Keep`; under
`SpendAll` claude (keep 0.2, weekly 50%, 24h) goes before codex (keep 0,
weekly 51%, 24h), and without it codex goes first; a weekly reset in the past
gives urgency 0. Each fails with its mutation above.

### 1.12 `lend` defaults from the active lease and confirms the shrunk keep

**Goal:** `ploopy lend` takes keep, model and effort defaults only from a lease
that is still active, and the line it prints after writing shows §6's shrunk
keep as the listing does.

**Evidence:**
- `cmd/ploopy/lend.go:101-106` — the defaults come from `file.Leases`, so an
  expired until lease with keep 0 and model `haiku` makes `ploopy lend claude
  --open` write `haiku`, keep 0. §4: an until lease whose instant has passed is
  read as absent.
- `cmd/ploopy/lend.go:150` — the confirmation passes a nil budget to
  `leaseLine`, so it prints `keep 20%` while `ploopy lend` right after prints
  `keep 20% → 1%`. §6: `ploopy lend` shows the shrunk keep.

**Start from:** the `lend` command in `cmd/ploopy/lend.go` (move `now` before
the defaults and use `file.Active(now)`; pass `history.Latest(localHistory(...),
h.Name())` as `listLeases` does); `cmd/ploopy/lend_test.go`.

**Done when:** a test seeds an expired until lease (keep 0, a non-default
model), runs `lend claude --open`, and asserts keep 20 and the harness's first
model; a test with history saying the week is nearly spent asserts the
confirmation shows the arrow; both fail before the change and pass after; `go
test ./cmd/ploopy` is green.

### 1.13 `reclaim` refuses a name that is not a subscription

**Goal:** `ploopy reclaim claud` is refused with `claud has no subscription to
lend`, as `lend` refuses it, instead of printing `reclaimed claud`; reclaiming
one harness removes only its lease.

**Evidence:** `cmd/ploopy/lend.go:171-185` — `reclaim` never checks the name
and reports success; only `reclaim all` is tested, so reclaiming the wrong
harness stays green.

**Start from:** `reclaim` and `newReclaim` in `cmd/ploopy/lend.go`; the refusal
in `lend`; `TestReclaimAll` in `cmd/ploopy/lend_test.go`.

**Done when:** tests show `reclaim claude` with claude and codex lent leaves
only codex, and `reclaim claud` fails and leaves the file unchanged; the second
fails before the change; `go test ./cmd/ploopy` is green.

### 1.14 `ploopy lend` refuses a lease file with a malformed nightly span

**Goal:** listing the leases reports a nightly lease whose span `loop.ParseAway`
cannot read as an error naming `lease.Path()`, as runs and the board already
treat it.

**Evidence:** `cmd/ploopy/lend.go:188-202` (`listLeases`) — with
`{"kind":"nightly","span":"09:00",...}` it prints `claude sonnet high · nightly
09:00 · keep 20%` and exits 0, while a run reading the same file says the lease
file cannot be read (`cmd/ploopy/lend.go:303-307`). §4: an invalid file is an
error naming the path. `internal/lease` keeps the span opaque by design, so the
check belongs in `cmd/ploopy`.

**Start from:** `listLeases` and the span check on the run path in
`cmd/ploopy/lend.go`.

**Done when:** a test writes that lease file, runs `ploopy lend`, and asserts an
error naming the path; it fails before the change; `go test ./cmd/ploopy` is
green.

### 1.15 Test `lend`'s write paths, flags and the steering loader

**Goal:** tests pin what `ploopy lend` writes for `--until` and `--nightly`, the
lease-kind mapping into `loop`, the `--keep`/`--model`/`--effort` overrides and
the current lease's defaults, and the loader's broken-file and expired-lease
handling.

**Evidence:** `cmd/ploopy/lend.go:101-146` and `:246-307`. Each of these leaves
`go test ./cmd/ploopy` green: writing `until` or `nightly` as `open`; mapping
`lease.Nightly` to `loop.LeaseOpen`; dropping `keep = f.keep`, `model =
f.model`, `effort = f.effort` or the copy of the current lease's values;
deleting `l.say(err)` or its `said` guard; replacing `lendingAt`'s
`file.Active(now)` with `file.Leases`. The only `--until` test pins `nextClock`.

**Start from:** `cmd/ploopy/lend_test.go`; `lend`, `load`, `say`, `lendingAt`
and `lendingFromLeases` in `cmd/ploopy/lend.go`; §4.

**Done when:** `go test ./cmd/ploopy` is green with tests that: run `lend claude
--until 09:00` and `--nightly 21:00-09:00` and load the stored kind and
instant or span; map each of the three kinds through `lendingFromLeases`; lend
over an active lease with no flags (its keep, model and effort kept) and with
all three flags (overridden); feed the steering loader an invalid file and
assert `Lending == nil` with exactly one report over two `Steer()` calls; and
feed it an expired until lease and assert it is absent. Each fails with its
mutation above.

### 1.16 `run --subscriptions=` means all, as on `adjust`

**Goal:** an explicit empty `--subscriptions` on `run` overrides the plan's and
the last run's set with `all`, as `ploopy adjust --subscriptions=` does.

**Evidence:** `cmd/ploopy/settings.go:150` tests `f.subscriptions != ""`, so
`--subscriptions=` is treated as absent and `subscriptions: none` from the plan
wins; `cmd/ploopy/adjust.go:189` tests `flags.Changed("subscriptions")` and
`subscriptionSet("")` returns `all`.

**Start from:** `givenSettings` in `cmd/ploopy/settings.go`;
`cmd/ploopy/subscriptions_test.go`.

**Done when:** a test with a plan saying `subscriptions: none` and
`--subscriptions=` asserts the set is `all` and given from a flag; it fails
before the change; `go test ./cmd/ploopy` is green.

### 1.17 Test the subscription settings on `adjust`, at the start, and carried over

**Goal:** tests pin `adjust --subscriptions` and `--spend-all`, the start
announcements of a narrowed set and of spend-all, and the last run's carry-over
of both.

**Evidence:** removing the two blocks in `changeFromFlags`
(`cmd/ploopy/adjust.go:189-199`), the two `report.Say` blocks
(`cmd/ploopy/run.go:296-305`), or the two new branches of `fill`
(`cmd/ploopy/settings.go:254-265`) leaves `go test ./...` green.

**Start from:** `cmd/ploopy/subscriptions_test.go`; the code above; §9.

**Done when:** `go test ./cmd/ploopy` is green with tests that: parse
`--subscriptions codex`, `--spend-all=true` and `--spend-all=false` through
`changeFromFlags` and check `change.Subscriptions` and `change.SpendAll`;
capture the reporter of a run whose plan says `subscriptions: codex`, one given
`--subscriptions codex`, and one given `--spend-all`, and check each start
message; `fill` a run with no plan key and no flag from a last run that set
both, and check they carry over. Each fails with its removal above.

### 1.18 The board highlights focus over dim and locked, and says what Enter does

**Goal:** the focused column's title and the field under the cursor are
highlighted even when the column is dimmed or the field locked, and the board's
help line names what Enter does: `start` on a run, `apply` on `ploopy adjust`.

**Evidence:**
- `internal/ui/board.go:477-497` — the title switch tests `column.Dim` before
  `focused`, and the field switch tests `field.Locked` and `column.Dim` before
  the cursor, so on the adjust board (cursor on the locked `a agent`) the
  cursor is invisible from the first frame. §10 *Layout*: "The focused column's
  title is highlighted, and so is the field under the cursor."
- `internal/ui/board.go:419` — the help line always says `enter start`; on the
  adjust board Enter applies. §10: "Enter starts (in `adjust`: applies)".

**Start from:** `columnLines` and `View` in `internal/ui/board.go`;
`newAdjustBoard` in `cmd/ploopy/board.go`; `internal/ui/board_test.go`.

**Done when:** view tests show the focused dim column's title and the cursor's
locked field rendered with the focused styles (combined with dim or locked if
wanted), and an adjust board's view containing `enter apply` and not `enter
start`; they fail before the change; `go test ./internal/ui ./cmd/ploopy` is
green.

### 1.19 Test the board's hour keys away from the lease field

**Goal:** tests pin that `+`/`-` move the lease's end hour wherever the cursor
is in a subscription column, and that `[`/`]` leave a single-clock until term
unchanged.

**Evidence:** `internal/ui/board.go:262-277` — deleting the fallback loop in
`timeField` (271-275) leaves `go test ./...` green; `:321-326` — replacing the
two-clock guard in `adjustClock` with `if start { at = 0 }` leaves it green too.
§10 *Keys*.

**Start from:** `internal/ui/board_test.go`; `timeField` and `adjustClock`.

**Done when:** `go test ./internal/ui` is green with a test that moves the
cursor to `k keep`, presses `+`, and sees the until hour move, and one that
presses `[` and `]` on `until 09:00` and sees it unchanged; each fails with its
mutation above.

### 1.20 The board keeps given values and normalizes before its first frame (F-D2)

**Goal:** implement F-D2 in `cmd/ploopy/board.go`.

**Evidence:**
- `cmd/ploopy/board.go:211-214` (`normalize`) — with `ploopy run --model
  sonnet` (or `--effort medium`) and an editable agent, changing the agent
  replaces the locked model and effort with the new harness's defaults; Enter
  starts with values the author did not pass. Lines 215-217 also replace an
  invalid given effort instead of letting `resolveSettings` refuse it.
- `cmd/ploopy/board.go:236-239` (`normalizeTriage`) — `ploopy review PASS
  --triage-model sonnet`, then cycling the Triage column's agent, replaces the
  flagged model and effort while the field still says "from a flag".
- `cmd/ploopy/board.go:103`, `:120` — `newSettingsBoard` and
  `newAdjustBoard` no longer call `normalize` before the first render, so a
  secondary from the last run that names the bedrock's harness, or an effort
  the bedrock does not offer, is shown and started with.

**Start from:** `normalize`, `normalizeTriage`, `newSettingsBoard`,
`newAdjustBoard`, `newReviewBoard` and `rebuild` in `cmd/ploopy/board.go`; the
`given` type in `cmd/ploopy/settings.go` and `triageDraft.given`;
`resolveSettings`; `cmd/ploopy/board_test.go`.

**Done when:** `go test ./cmd/ploopy` is green with tests that: change the
agent on a board with a given model, and with a given effort, and see both
values and locks kept; do the same for the Triage column with
`--triage-model`; build a run board whose last run's secondary names the
bedrock's harness and see it cleared before any key; and press Enter with a
given effort the chosen agent does not offer and get `resolveSettings`'
refusal. Each fails before the change.

### 1.21 Offer `n then` on the adjust board only for a review run

**Goal:** the adjust board's Run column has `n then` only when the live run's
plan is a review; on an ordinary run it is left out, because nothing chains
from an ordinary plan.

**Evidence:** `cmd/ploopy/board.go:359` adds `n` when `sb.triage != nil ||
sb.adjust`; on an ordinary run, choosing "run the fix plan" sends
`control.Settings.Then` and prints `then run the fix plan`, but
`chainedFixPlan` (`cmd/ploopy/run.go:339`) requires `p.Reviews() != ""`, so
nothing happens. §13 puts `n` on the review board and the adjust board of a
review. `TestTheAdjustBoardLocksTheBedrockAndOffersStop`
(`cmd/ploopy/board_test.go:133-143`) asserts the field on an ordinary run and
changes with this unit.

**Start from:** the Run column in `cmd/ploopy/board.go`; `liveRun` and the
adjust tests in `cmd/ploopy/board_test.go`.

**Done when:** the adjust test on the ordinary `PASS` plan asserts no `n`
field, and a new adjust test on a live review plan asserts it; `go test
./cmd/ploopy` is green.

### 1.22 Test what the boards' Enter carries into a run

**Goal:** tests pin the path from a board's Enter to what runs: edited leases
written, the adjust board's values read and applied, the bedrock prefilled
from the live run, and `n` reaching `Then`.

**Evidence:**
- `cmd/ploopy/settings.go:96`, `cmd/ploopy/board.go:246-273`, `:320-378` —
  deleting the `board.saveLeases(time.Now())` call or making `loopLease` save
  the old term leaves `go test ./...` green. §10: on start the board writes the
  leases it changed.
- `cmd/ploopy/adjust.go:208-214`, `cmd/ploopy/board.go:409-415` — removing
  `sb.read()` from `settingsBoard.run`, or the `applyAdjust` call from
  `adjustInteractively`, leaves it green: the adjust test calls `read` and
  `applyAdjust` itself.
- `cmd/ploopy/board.go:131-139` — `TestTheAdjustBoardLocksTheBedrockAndOffersStop`
  never asserts the bedrock's harness, model and effort are the live run's.
- `cmd/ploopy/board.go:459`, `:478` — no test shows `n` on a board reaching
  `chosen.then` or the adjusted `Then`.

**Start from:** `cmd/ploopy/board_test.go` and `review_board_test.go`;
`settingsBoard.run`, `read`, `finish`, `saveLeases`, `applyAdjust` and
`adjustChange`; `chooseSettings` in `cmd/ploopy/settings.go`;
`adjustInteractively` in `cmd/ploopy/adjust.go`. Where tea cannot drive the
board in a test, move what follows the board's return into a function the
interactive path calls and test that function.

**Done when:** `go test ./cmd/ploopy` is green with tests that: edit a lease's
term, model and keep and see them in `lease.Load()` after the post-board step,
leave an unchanged lease untouched, and write nothing on cancel; drive the
adjust post-board step and see the board's `l`/`w`/`p` changes in the live
run's `control.Settings` and the leases saved; assert the adjust board's
bedrock is `opencode`, `deepseek/deepseek-flash`, `high` from `liveRun`; press
`n` on a review board and get `chosen.then == "fixes"`, and on an adjust board
of a review and get `Then` written. Each fails with its removal above.

### 1.23 Test the forecast across several subscriptions and a week that does not move

**Goal:** forecast tests cover two lent subscriptions in §7 order sharing the
open units, and a metered estimate whose sessions move the week by nothing.

**Evidence:** `cmd/ploopy/forecast.go:60` — every forecast test has one
subscription, so removing the `loop.LendOrder` call or the `U -= units`
sharing stays green. `cmd/ploopy/forecast.go:147-151` — with
`WeeklyPerWindow() == 0`, `byWeek` stays unbounded; changing its start to 0
stays green since unit 3.4 deleted `TestAWeekThatBarelyMovesLeavesEveryWindow`.

**Start from:** `cmd/ploopy/forecast_test.go`; `forecastRun` and
`forecastSubscription`; §10 *Footer forecast*.

**Done when:** `go test ./cmd/ploopy` is green with a two-subscription test
(one active window, one fresh) whose footer order and unit split follow §7, and
a test with a metered estimate of `Weekly: 0`, an open lease and several open
units in which the subscription takes them all and the bedrock none. Each fails
with its mutation above.

### 1.24 Test the main menu's letters and its cancel keys

**Goal:** tests pin Esc and ctrl-c cancelling the *What now?* picker, and the
real menu's letters: the ones the README's *Start* lists, no duplicates, and
`a adjust` present exactly while a run goes on.

**Evidence:** `internal/ui/pick.go:57-58`, `:98-102` — no test drives Esc or
ctrl-c. `cmd/ploopy/main.go:73-98` — `chooseCommand`'s choices are never
tested; giving two commands the same letter, or offering `a` with no live run,
stays green.

**Start from:** `internal/ui/pick_test.go`; `chooseCommand` in
`cmd/ploopy/main.go` (move the building of its choices into a function of the
live runs if needed).

**Done when:** `go test ./internal/ui ./cmd/ploopy` is green with tests that
Esc and ctrl-c end the picker with `huh.ErrUserAborted` and no value, and that
the menu's choices carry unique letters, match the README's listing, and
include `adjust` only when a live run is passed in.

### 1.25 A running review keeps its triage

**Goal:** `ploopy review` never rewrites the triage of a review plan that is
being run; the Triage column shows locked with the reason ("the review is
running"), and neither path commits a change. `retriage`, which nothing calls,
goes, and its tests move to the code the command uses.

**Evidence:**
- `cmd/ploopy/review.go:113-128` and `:192-196` — `reviewPlan` checks
  `c.Live` only for the reviewed plan; with the review plan running in another
  terminal, a `--triage-*` flag (non-interactive) or a change on the board
  calls `writeTriage`, which rewrites and commits the plan under the running
  loop. The live guard survives only in the unused `retriage`
  (`cmd/ploopy/review.go:276`).
- `cmd/ploopy/triage_test.go:83-133` tests `retriage`, which no caller uses.
- `cmd/ploopy/review_board_test.go:85-177` — both "as this review has it"
  tests write the review with `defaultTriage` and no last triage, so they pass
  even if the review's own triage were ignored.

**Start from:** `reviewPlan`, `reviewTriage`, `retriage` and `writeTriage` in
`cmd/ploopy/review.go`; `e.find`; `cmd/ploopy/triage_test.go`;
`cmd/ploopy/review_board_test.go`.

**Done when:** one function decides whether a review's triage may change
(not ran, not live) and both write paths use it; `rg -n 'func retriage' cmd` is
empty; `go test ./cmd/ploopy` is green with tests that: a live review plan's
triage is locked and a triage flag commits nothing; the former `retriage`
cases (landed triage, unchanged triage) hold through the new function; the two
review-board tests use a review whose triage differs from the last and default
(such as `sonnet`/`low`) and see that value prefilled, and, pending, committed
when changed.

### 1.26 The fix plan's own `test:` decides, not the review's flag

**Goal:** a chained fix plan runs its units with or without the tests as its
own `test:` front matter says; the review's `--test`/`--no-test` applies only
when the fix plan gives no `test:`.

**Evidence:** `cmd/ploopy/run.go:364-367` copies the review's run flags into the
fix run; `testsOverride(f)` (`run.go:234`) makes `opts.Tests` non-nil, and
`wantsTests` (`internal/loop/loop.go:329-332`) then ignores the plan's
`test:`. `ploopy review PASS --then fixes --no-test` runs a `test: yes` fix
plan without its tests. §13 and the README's *Review*: the review's settings
apply "except what the fix plan's front matter gives".

**Start from:** `runFixPlan` in `cmd/ploopy/run.go`;
`cmd/ploopy/review_chain_test.go` (`fixesPlan`).

**Done when:** a chain test with `--no-test` and a fix plan saying `test: yes`
shows the fix unit's prompt asking for tests, and one whose fix plan has no
`test:` shows the review's flag applied; the first fails before the change;
`go test ./cmd/ploopy` is green.

### 1.27 Test the chained fix plan's settings, the default stop, and the single push

**Goal:** chain tests pin that the fix plan inherits the review's adjusted
settings, that its own front matter overrides them, that a non-interactive
review with no `--then` starts no fix plan, and that push and the end action
happen once, after the fix plan.

**Evidence:**
- `cmd/ploopy/settings.go:415-473` (`overChosen`, `applyPlanSettings`) —
  removing any branch except `Then`, or any front-matter branch except `model`,
  stays green; only `Specs[2].Model` is asserted
  (`cmd/ploopy/review_chain_test.go:170`).
- `cmd/ploopy/settings.go:396-399` — every chain test passes `--then` or
  adjusts it, so an unset default that became `fixes` stays green.
- `cmd/ploopy/run.go:143-168`, `:373-383` — no chain test passes `--push` or
  `--finish`, so pushing after the review as well stays green.

**Start from:** `cmd/ploopy/review_chain_test.go`; `runFixPlan`, `finishRun`
and the chain in `runPlan` in `cmd/ploopy/run.go`.

**Done when:** `go test ./cmd/ploopy` is green with chain tests that: adjust
`spend-all` (or push) during the review and see it in the fix run; give the fix
plan `agent`/`effort`/`subscriptions` front matter and see them win; run a
non-interactive review with no `--then` and see no fix unit start; run with
`--push` against a bare remote and see exactly one push, after the fix plan.
Each fails with its mutation above.

### 1.28 Bring the README in line with the board

**Goal:** the README no longer describes pages the board replaced, and says
what adjust's Enter applies to.

**Evidence:**
- `README.md:78` — "Who repairs is asked on a page of its own, like the
  triage": the triage is now the review board's Triage column.
- `README.md:263` — "Choose `codex` on the Agent page for runs, reviews, triage
  or usage-limit fallback": runs and reviews choose it in the board's Bedrock
  column, the triage in its Triage column, the fallback in `l`.
- `README.md:171-174` — "Enter applies the settings and any changed leases to
  every run": `applyAdjust` sends the settings to the picked run only
  (`control.Adjust(live.PID, …)`); only the leases reach every run (§10).

**Start from:** those README lines; `cmd/ploopy/board.go`.

**Done when:** `rg -n 'like the triage|Agent page|to every run\.' README.md`
is empty, the adjust paragraph says the settings go to the chosen run and the
leases to every run, and the Codex section names the board's columns.

### 1.29 Remove the settings page's dead helpers

**Goal:** `peakHours` and `weekdays`, whose only caller was the removed
`askSettings`, are deleted.

**Evidence:** `cmd/ploopy/run.go:556-591` — `rg 'peakHours|weekdays' cmd`
finds only their declarations and their call to each other; `golangci-lint run
--default=none --enable=unused --tests=false ./...` reports both. §11 removes
the settings page's machinery.

**Start from:** `cmd/ploopy/run.go`.

**Done when:** `rg -n 'func peakHours|func weekdays' cmd` is empty and the
repository's checks are green.
