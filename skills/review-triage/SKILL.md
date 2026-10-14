---
name: review-triage
description: Weigh the findings review sessions collected on a plan's landed work — confirm or drop each, merge duplicates, class them — then write a fix plan for the slips and gaps and put the design questions to the author. Use when a ploopy review hands you its triage unit.
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
   Between gap and design, choose design: asking the author costs less than
   undoing a wrong fix.
4. **Write the fix plan** for the slips and gaps, when any hold.
5. **Write the section for the author** at the end of the review plan.
6. **Lint and commit.** `ploopy lint <fix plan>` until it reports no errors. Then
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

Each unit fixes a defect a review found and the triage confirmed. Every unit:

- first shows the defect with a test that fails, where a test can show it, then
  fixes it and keeps the test;
- makes the smallest change that fixes the defect, in the code the unit names;
- never redesigns: when the fix turns out to need a decision the unit does not
  make — a contract another owner relies on, a change to a decision of the plan —
  it finishes BLOCKED and names the decision.

## Stage 1 — Fixes

### 1.1 <the fix, as a change: "Refuse an empty name before changing anything">

**Goal:** <what is wrong, where, and what is right instead>

**Evidence:** `path:line` — <situation>; <what happens>; <what should>.

**Start from:** <the file and procedure>

**Done when:** <a named test fails before the change and passes after; the
repository's checks are green>
```

- One unit per slip, or several slips in one procedure or file together. One
  unit per gap. Order units so each leaves the checks green.
- *Done when* is checkable: a named test, a command, an `rg` that must come back
  empty.
- No unit for a design question, and no unit that only investigates.
- Hold nothing back for a later pass and invent no gate: what holds and is a slip
  or a gap gets a unit.

## For the author

Append this to the review plan, after its last stage:

```
## For the author

<N> findings: <a> slips and <b> gaps go to `<fix plan>` in <u> units, <c> need a
decision below, <d> were dropped.

### <the design question, as a question>

<Where, what happens, and why it is not a local fix, in three to six lines. The
options you see, one line each, and which you would lean to and why. No plan.>

### Dropped

- F2 from 1.3: <one line on why>.
```

When nothing needs a decision, say so in one line and keep *Dropped*. The author
reads this section rather than the findings, so it must stand on its own.

## Handover

A few lines: the counts, what went where, and any finding you were unsure of.
