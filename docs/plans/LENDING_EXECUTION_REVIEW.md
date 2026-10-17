---
reviews: docs/plans/LENDING_EXECUTION.md
test: no
---

# Review of LENDING_EXECUTION

Status: written by `ploopy review` on 2026-10-17 against `cc16d017`. It reviews
15 landed units of `docs/plans/LENDING_EXECUTION.md`.

Each collecting unit reads one landed unit's work order, handover and commits,
and writes its findings without changing the repository. The seams unit looks
only at what lies between the units. The triage unit weighs every finding,
writes the fix plan `docs/plans/LENDING_EXECUTION_FIXES.md` for every finding
that holds using its recommended design choices, and explains those choices and
their alternatives under *For the author*.

## Stage 1 — Collect

### 1.1 Review 1.1 Fake agents

Review: 1.1

**Goal:** findings on unit 1.1 of `docs/plans/LENDING_EXECUTION.md`, *Fake
agents*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.2 Review 1.2 The sandbox

Review: 1.2

**Goal:** findings on unit 1.2 of `docs/plans/LENDING_EXECUTION.md`, *The
sandbox*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.3 Review 2.1 Hand uncommitted changes to the session instead of refusing

Review: 2.1

**Goal:** findings on unit 2.1 of `docs/plans/LENDING_EXECUTION.md`, *Hand
uncommitted changes to the session instead of refusing*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.4 Review 2.2 Carry uncommitted changes into the worktree on hand-off

Review: 2.2

**Goal:** findings on unit 2.2 of `docs/plans/LENDING_EXECUTION.md`, *Carry
uncommitted changes into the worktree on hand-off*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.5 Review 3.1 The lease file

Review: 3.1

**Goal:** findings on unit 3.1 of `docs/plans/LENDING_EXECUTION.md`, *The lease
file*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.6 Review 3.2 The loop runs on leases

Review: 3.2

**Goal:** findings on unit 3.2 of `docs/plans/LENDING_EXECUTION.md`, *The loop
runs on leases*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.7 Review 3.3 The shrinking reserve and the order among subscriptions

Review: 3.3

**Goal:** findings on unit 3.3 of `docs/plans/LENDING_EXECUTION.md`, *The
shrinking reserve and the order among subscriptions*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.8 Review 3.4 Runs read the machine's leases; `lend` and `reclaim`

Review: 3.4

**Goal:** findings on unit 3.4 of `docs/plans/LENDING_EXECUTION.md`, *Runs read
the machine's leases; `lend` and `reclaim`*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.9 Review 3.5 Which subscriptions a run uses; spend all

Review: 3.5

**Goal:** findings on unit 3.5 of `docs/plans/LENDING_EXECUTION.md`, *Which
subscriptions a run uses; spend all*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.10 Review 4.1 The board component

Review: 4.1

**Goal:** findings on unit 4.1 of `docs/plans/LENDING_EXECUTION.md`, *The board
component*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.11 Review 4.2 Run settings on the board

Review: 4.2

**Goal:** findings on unit 4.2 of `docs/plans/LENDING_EXECUTION.md`, *Run
settings on the board*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.12 Review 4.3 `ploopy adjust` on the board

Review: 4.3

**Goal:** findings on unit 4.3 of `docs/plans/LENDING_EXECUTION.md`, *`ploopy
adjust` on the board*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.13 Review 4.4 One-key shortcuts in the main menu

Review: 4.4

**Goal:** findings on unit 4.4 of `docs/plans/LENDING_EXECUTION.md`, *One-key
shortcuts in the main menu*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.14 Review 5.1 One board for a review

Review: 5.1

**Goal:** findings on unit 5.1 of `docs/plans/LENDING_EXECUTION.md`, *One board
for a review*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.15 Review 5.2 A review goes on to its fix plan

Review: 5.2

**Goal:** findings on unit 5.2 of `docs/plans/LENDING_EXECUTION.md`, *A review
goes on to its fix plan*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.16 Review the seams between the units

Review: seams

**Goal:** findings on what lies between the reviewed units: the contracts they
share, work one left for another, leftovers no unit owns, documents none
updated.

**Done when:** the findings file is written and the repository is unchanged.

## Stage 2 — Triage

### 2.1 Weigh the findings

Review: triage
Agent: claude
Model: opus
Effort: high

**Goal:** every finding confirmed or dropped, duplicates merged, each one
classed as a slip, a gap or a design question; a fix plan for every finding that
holds, with recommended design choices already applied; those choices and their
alternatives put to the author.

**Done when:** one commit touches only `docs/plans/LENDING_EXECUTION_FIXES.md`
and this document, this document ends with a *For the author* section, and the
fix plan, when there is one, lints clean.

## For the author

63 findings: 12 slips, 42 gaps and 2 design findings go to
`docs/plans/LENDING_EXECUTION_FIXES.md` in 29 units, after folding 4 duplicates
into them (1.2 F1 into 1.1 F1, 1.7 F2 absorbs 1.6 F5, 1.14 F2 and F3 into
1.11 F1); 2 design recommendations are explained below; 3 were dropped. Most
gaps are missing tests the collectors showed by mutation; the fix plan groups
them by file. Its decisions are named F-D1 and F-D2 so they do not collide with
`LENDING.md`'s D1–D14.

### When the pop into the new worktree fails, where do the branch's changes go?

`env.handOff` (`cmd/ploopy/worktree.go:143-149`) stashes the branch's
uncommitted changes, hands the branch to a new worktree, and pops there. If
that pop fails, it pops the stash in the main checkout, which `HandOff` has
already switched to the default branch, so the branch's changes land on master's
working tree. §3 step 4 says "switch back to the branch, then pop", but was
written for a failed `HandOff`; after a successful one the branch is checked out
in the new worktree and main cannot switch back without undoing the hand-off.
The path runs only when a pop at the same commit fails (permissions, disk), so
it is rare, but it moves work onto the wrong branch.

- **Recommended:** undo the hand-off: remove the new worktree (it holds only
  the half-applied pop), switch main back to the branch, pop there, return the
  error. The changes end where they started, which is what §3 intends.
- **Alternatives:** leave the worktree and the stash as they are and fail with
  a message naming the stash — nothing is undone, but the author has to recover
  by hand from a shared stash stack; or retry with `git stash apply` in the
  worktree and drop on success — still needs a recovery when that fails too.
- **Planned as:** F-D1, unit 1.8.

### May the board change a value the author gave as a flag?

`normalize` and `normalizeTriage` (`cmd/ploopy/board.go:211-217`, `:236-239`)
reset model and effort to the new harness's defaults whenever the agent
changes, including a model or effort given with `--model`, `--effort` or
`--triage-*`, while the field still shows locked "from a flag". They also
replace an invalid given effort silently. Separately, since the review board
landed, `normalize` no longer runs before the first frame, so a last-run
secondary that names the bedrock is shown and started with. The README says a
given setting is locked and says where it came from; §10 says a key on a locked
field changes nothing; no document says what an agent change does to a locked
model.

- **Recommended:** the board never changes a given value; normalize corrects
  only values the author did not give, and runs once before the first frame.
  An invalid combination of given values is refused on Enter by the check that
  already refuses it when the flags are given together. This keeps the lock
  honest and reuses existing validation.
- **Alternatives:** lock the agent too whenever a model or effort is given — no
  invalid combinations, but `--effort high` alone would freeze the agent
  needlessly; or let the agent change reset the value and unlock it ("no
  longer from a flag") — the board always shows a valid run, but a flag the
  author typed is discarded by one key press.
- **Planned as:** F-D2, unit 1.20.

### Dropped

- F3 from 1.2: asserting the default scenario's individual values pins
  incidental numbers, not a contract; the table names it does check are the
  structure.
- F3 from 1.6: the `--night` bridge it cites was removed by unit 3.4; the code
  no longer exists.
- F1 from 1.13: `q` cancelling the menu is deliberate and matches §10's board
  keys (Esc, `q` and ctrl-c cancel); no command uses `q`.
