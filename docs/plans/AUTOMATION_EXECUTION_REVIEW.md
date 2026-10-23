---
reviews: docs/plans/AUTOMATION_EXECUTION.md
test: no
---

# Review of AUTOMATION_EXECUTION

Status: written by `ploopy review` on 2026-10-23 against `0d9eb2b3`. It reviews
7 landed units of `docs/plans/AUTOMATION_EXECUTION.md`.

Each collecting unit reads one landed unit's work order, handover and commits,
and writes its findings without changing the repository. The seams unit looks
only at what lies between the units. The triage unit weighs every finding,
writes the fix plan `docs/plans/AUTOMATION_EXECUTION_FIXES.md` for every finding
that holds using its recommended design choices, and explains those choices and
their alternatives under *For the author*.

## Stage 1 — Collect

### 1.1 Review 1.1 `[run]` and `[review]` tables

Review: 1.1

**Goal:** findings on unit 1.1 of `docs/plans/AUTOMATION_EXECUTION.md`, *`[run]`
and `[review]` tables*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.2 Review 2.1 `then` becomes the follow-up setting

Review: 2.1

**Goal:** findings on unit 2.1 of `docs/plans/AUTOMATION_EXECUTION.md`, *`then`
becomes the follow-up setting*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.3 Review 2.2 A finished plan goes on to its review and fix plan

Review: 2.2

**Goal:** findings on unit 2.2 of `docs/plans/AUTOMATION_EXECUTION.md`, *A
finished plan goes on to its review and fix plan*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.4 Review 3.1 The rescue session in the loop

Review: 3.1

**Goal:** findings on unit 3.1 of `docs/plans/AUTOMATION_EXECUTION.md`, *The
rescue session in the loop*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.5 Review 3.2 Rescues on record

Review: 3.2

**Goal:** findings on unit 3.2 of `docs/plans/AUTOMATION_EXECUTION.md`, *Rescues
on record*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.6 Review 3.3 Choosing the rescue

Review: 3.3

**Goal:** findings on unit 3.3 of `docs/plans/AUTOMATION_EXECUTION.md`,
*Choosing the rescue*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.7 Review 4.1 Skills and README

Review: 4.1

**Goal:** findings on unit 4.1 of `docs/plans/AUTOMATION_EXECUTION.md`, *Skills
and README*, against its work order.

**Done when:** the findings file is written and the repository is unchanged.

### 1.8 Review the seams between the units

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

**Done when:** one commit touches only
`docs/plans/AUTOMATION_EXECUTION_FIXES.md` and this document, this document ends
with a *For the author* section, and the fix plan, when there is one, lints
clean.
