---
name: review-triage
description: Weigh the findings review sessions collected on a plan's landed work — confirm or drop each, merge duplicates, class them — then write a fix plan that applies recommended resolutions and show the author the choices and alternatives. Use when a ploopy review hands you its triage unit.
---

# Triage

You are the expensive model in this loop, and you are here for judgement: not to
dig, and not to fix. Cheaper sessions collected the findings; a cheaper session
will carry out the fixes. Spend your effort deciding.

## Limits

- Change only the two documents your order names. Never edit source, tests or
  another document, and never fix a finding yourself, not even a one-line slip:
  it goes into the fix plan. The loop looks at your commit and stops the review
  if it touches anything else.
- For each finding, read the code it cites — the lines around it and the
  definitions they depend on — and nothing beyond what deciding it needs.
- Do not review the work afresh. What the collectors missed stays missed, unless
  you trip over a defect while checking a finding; then add it, marked as yours.
- Do not run the test suite. Run one focused test only when a finding hangs on it
  and reading cannot settle it.

## Steps

1. **Read every finding before checking any.** Group those about one defect —
   the same place, or the same cause seen from two units. Keep the best-evidenced
   one and fold the others' evidence into it.
2. **Check each against the code.** Decide:
   - **holds** — the code does what the finding says, and that is wrong by the
     work order, the plan, the repository's rules or plain correctness;
   - **drop** — the code does not do that; or it does and the plan decided so;
     or it is style, taste or speculation; or its *Should* is only the
     collector's preference. Keep one line on why;
   - **unsure** — reading cannot settle it in reasonable time. If it would matter,
     it holds, and its fix unit proves it with a failing test before fixing.
     Otherwise drop it.
3. **Class what holds.** The collector's class is a proposal:
   - **slip** — right intent, wrong code in one place; the fix changes no
     contract;
   - **gap** — right intent, something missing; the fix stays inside the code
     the unit owns;
   - **design** — the fix changes a contract that other code, another unit or a
     document relies on, or needs a decision the plan did not make.

   One question decides design: *does the fix need a decision nobody has made?*
   If it does, it is design, however small the diff — a reversed condition is
   design when it encodes the wrong rule rather than mistyping the right one.
   Between gap and design, choose design so the triage's recommendation becomes
   an explicit, reviewable plan decision instead of an implicit local fix.

   Before you class anything as design, establish when the cited path runs:
   read the guards of its callers and the code that chooses that branch. A
   question built on a path that runs more often, or less often, than it really
   does sends the author to decide the wrong thing.
4. **Resolve each design finding.** Choose the option you recommend from the
   code, the plan and the repository's rules. Treat that recommendation as a
   provisional decision: put it in the fix plan and plan all of the work it
   requires. Uncertainty belongs in your rationale; it is not a reason to omit
   the finding or wait for an answer. Do not choose an option that contradicts
   an explicit decision of the author; when two such decisions conflict, choose
   the smallest reversible resolution and name the conflict.
5. **Write the fix plan** for every finding that holds, when any do.
6. **Write the section for the author** at the end of the review plan. Show each
   recommended design choice, its alternatives and where the fix plan applies
   it, so the plan can run immediately but the author can revise it first.
7. **Lint and commit.** `ploopy lint <fix plan>` until it reports no errors. Then
   one commit holding the fix plan and the review plan and nothing else, the
   handover, and `DONE`.

## The fix plan

A plan the loop runs like any other: each unit goes to a fresh session with the
`plan-unit` skill, the plan's preamble and that unit, and nothing else — not the
findings, not the review. Write every unit to stand alone.

```
---
test: yes
context: <the reviewed plan's authority and context documents, while they exist>
---

# Fixes from the review of <plan>

Status: active task document, written <date> against `<short HEAD>` by the triage
of `<review plan>`.

Each unit fixes a defect a review found and the triage confirmed. Any design
decisions below are the triage's recommendations. This plan implements them as
written unless the author revises the plan before running it.

## Recommended design decisions

### D1 — <the chosen rule, as a statement>

**Choice:** <the option this plan implements>.

**Why:** <why this is the best fit for the evidence, existing decisions and
repository rules>.

Every unit:

- first shows the defect with a test that fails, where a test can show it, then
  fixes it and keeps the test;
- makes the smallest change that fixes the defect, in the code the unit names;
- implements cited recommended decisions as written and never makes another
  design decision itself; when the fix turns out to need one the plan does not
  make, it finishes BLOCKED and names the decision.

## Stage 1 — Fixes

### 1.1 <the fix, as a change: "Refuse an empty name before changing anything">

**Goal:** <what is wrong, where, and what is right instead>

**Evidence:** `path:line` — <situation>; <what happens>; <what should>.

**Start from:** <the file and procedure>

**Done when:** <a named test fails before the change and passes after; the
repository's checks are green>
```

- One unit per slip, or several slips in one procedure or file together. One
  unit per gap. Give a design finding as many units as its recommended choice
  needs, and cite its decision (D1, D2, ...) in each one. Order units so each
  leaves the checks green.
- *Done when* is checkable: a named test, a command, an `rg` that must come back
  empty.
- No unit that only investigates.
- Hold nothing back for a later pass and invent no gate: every finding that
  holds gets the units needed to implement it.
- Omit *Recommended design decisions* when no design finding holds.

## For the author

Append this to the review plan, after its last stage:

```
## For the author

<N> findings: <a> slips, <b> gaps and <c> design findings go to `<fix plan>` in
<u> units; <c> design recommendations are explained below; <d> were dropped.

### <the design question, as a question>

<Where, what happens, and why it is not a local fix, in three to six lines.>

- **Recommended:** <the option already used by D1 and its units, and why>.
- **Alternatives:** <each other credible option and its tradeoff>.
- **Planned as:** <D1 and the unit or units that implement it>.

### Dropped

- F2 from 1.3: <one line on why>.
```

When no design choice was needed, say so in one line and keep *Dropped*. The
author reads this section rather than the findings, so it must stand on its own.
The recommended choice is already actionable; do not ask the author a question
or make running the fix plan wait for an answer.

## Handover

A few lines: the counts, what went where, and any finding you were unsure of.
