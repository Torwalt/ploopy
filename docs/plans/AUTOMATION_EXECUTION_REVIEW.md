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

## For the author

27 findings: 11 slips, 6 gaps and 2 design findings go to
`docs/plans/AUTOMATION_EXECUTION_FIXES.md` in 17 units, after folding 3
duplicates into them (1.3 F1 into 1.2 F1, 1.2 F3 into 1.1 F3, 1.3 F5 into
1.8 F1); 2 design recommendations are explained below; 5 were dropped. The fix
plan's decisions are named F-D1 and F-D2 so they do not collide with
`AUTOMATION.md`'s D1–D9.

### Does a run limited by `--until` or `--units` follow up when the limit leaves the plan complete?

`followsUp` (`cmd/ploopy/run.go:209-212`) chains when the plan is complete, the
result is not `Stopped` and follow-up is on. The loop's `--until`/`--units`
return never sets `Stopped`, so a one-unit plan run with `--until 1.1
--follow-up`, or `--until` naming the last unit, writes and runs its review,
while the same flags on a longer plan do not. The 2.2 work order lists
`--until` among the early stops that do not chain but did not say what happens
when the limit and the plan's end coincide. The same check also lets a run that
failed after its last unit landed chain on and lose its error.

- **Recommended:** a run started with `--until` or `--units` never follows up,
  and its start line promises nothing; a run whose result is not `done` never
  chains. The author can predict it from the command line alone.
- **Alternatives:** follow up whenever the plan ends complete, whatever the
  limit (today's behaviour; only the start line needs fixing, but `--until` on
  the last unit no longer means "stop here"); or have the loop report whether
  the limit actually cut the run short and chain only when it did not (precise,
  but a new `Result` field for a rare case).
- **Planned as:** F-D1, unit 2.1.

### How does a decided rescue's record keep its decisions when the report is cut at 4000 bytes?

§9 stores the rescue's report cut at 4000 bytes and also asks `ploopy report`
and the closing summary to show a `decided` rescue's *Decisions* in full. Both
readers take the decisions from the cut text (`state.Rescue.Section`), so a
long *Diagnosis* — §7 asks for log lines and check output — removes them from
the record while the loop used them. The plan's two requirements conflict.

- **Recommended:** an additive `decisions` field on `state.Rescue` holding the
  section in full; `report` keeps its cut; readers prefer the field and fall
  back to the report for old records. Allowed by the frozen state format and
  reversible.
- **Alternatives:** raise or drop the cut (simplest, but the state file grows
  with every long report); cut the report section by section so *Decisions*
  and *Next attempt* survive and *Diagnosis* is shortened (no new field, but a
  parser decides what the author loses); store only a path to the report file
  (smallest record, but the file lives outside the index and does not reach the
  default branch with the report).
- **Planned as:** F-D2, unit 3.5.

### Dropped

- F4 from 1.1: the secondary trio and front matter over file resolve correctly;
  the work order did not ask for these tests.
- F2 from 1.2: the explicit file `false` and the last-run prefill resolve
  correctly; the work order did not ask for these tests.
- F2 from 1.4: a DECIDED report without *Next attempt* still hands the next
  attempt its decisions, which are enforced; failing an overnight run for a
  missing section §7 does not make mandatory would cost more than it protects.
- F4 from 1.5: a completed run prints the full report, *Rescued* section
  included, which is what the pointer leads to.
- F5 from 1.5: the retry, blocked and failed records are written; 3.2's work
  order asked only for the decided record's test, and units 3.3, 3.4 and 3.6
  add tests on the blocked and failed records.
- From F1 of 1.5, the `ploopy report` half: §9 puts rescues on the entry when
  the unit settles, which a later run does; only the closing summary is planned
  (unit 3.6).
