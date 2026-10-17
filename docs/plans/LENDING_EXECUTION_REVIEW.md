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
