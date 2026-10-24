---
name: plan-review
description: Review one landed unit of a plan, or the seams between its units, and write findings without changing anything. Use when a ploopy review session hands you a review order.
---

# Reviewing landed work

A review order names work another session already landed: one unit's commits,
its work order and its handover, or the seams between all the units of a plan.
You find what is wrong with it and write it down. A stronger model then decides
what holds and what to do about it, and a fresh session fixes what is fixed. Your
findings are only as useful as the evidence in them.

## You change nothing

- No edits, no commits, no stash, no checkout of another revision, no formatter
  run that rewrites files. The loop looks at the repository after you: a commit
  stops the review, and a dirty tree fails you.
- You may read anything, search, use read-only git (`git show`, `git diff`,
  `git log`, `git blame`), and run the repository's checks or a single test when
  a finding depends on what they say. Ignored build output does not count.
- Something worth fixing is a finding. Write it down; never fix it, however small.

## The steps

Work through them in order. On a small unit each is short; do not skip one
because the unit looks simple.

1. **What was asked.** Read the work order: its goal, its *Done when*, its
   non-goals, and the decisions in the plan's preamble that the unit touches.
   Read the handover: what the implementer says it did, and where it says it
   departed from the order.
2. **The change.** Read `git diff <range>` in full. Then read the code each hunk
   depends on and nothing more: the callers of a changed procedure, the type it
   changes, the owner whose state it touches.
3. **Conformance.** Go through the goal and every *Done when* criterion: done,
   missing, or done differently. Done differently and not named in the handover
   is a finding. A non-goal that was done anyway is a finding.
4. **Correctness, procedure by procedure.** For every changed procedure check:
   - each condition, and what happens when it is false;
   - boundaries: empty, one, full, the first and the last element;
   - the order of operations, and state left half-updated on an early return;
   - references, indices or pointers held across something that removes, moves or
     reallocates what they point at;
   - units of measure: a rate compared with an amount, milliseconds with seconds;
   - conversions, overflow, division by zero;
   - every error and early-return path.
5. **The repository's checklist**, when the order includes one. Go through every
   item against the change.
6. **Tests.** For each behaviour the unit added or changed, name the most obvious
   way to break it — reverse a condition, drop a call, skip an update — and find
   the test that would then fail. When none would, that is a finding. So is a test
   that cannot fail, or one that asserts something the change does not do.
7. **Write the findings.**

## What a finding is

A defect you can show: a place in the code, a concrete situation, what happens
there, and what should happen instead. Do not report:

- style, layout, naming taste, or anything the repository's formatter decides;
- a design you would have preferred where the plan's decisions chose this one;
- performance away from the paths the repository calls hot, without a number;
- features the order did not ask for;
- anything you cannot tie to a file and a line.

Suspecting is not finding. When you suspect something, show it — read further,
run a test — or leave it out. Two real findings beat ten guesses, and no finding
at all is a good outcome when the work is right. At most eight, the most severe
first.

## Classes

Propose one for each finding; the triage decides.

- **slip** — the intent is right and the code is wrong in one place: a reversed
  condition, an off-by-one, a missed update, a missing test. Fixing it changes
  no contract.
- **gap** — the intent is right but something is missing: an unhandled case, a
  cost nobody bounded, a behaviour with no test, work the order asked for and the
  unit did not do. The fix stays inside the code the unit owns.
- **design** — fixing it changes a contract that other code, another unit or a
  document relies on, or needs a decision the plan did not make: state kept by
  the wrong owner, one fact kept in two places, an approach that cannot meet a
  stated requirement. The size of the diff does not decide this; whether a
  decision is needed does.

## The findings file

At the path the order gives, in exactly this shape:

```
# Findings: <what was reviewed>

Reviewed: <range>, <N> files. Steps: all done | <which were cut short, and why>.

## F1 — <the defect, in one line>

- Class: slip | gap | design
- Confidence: high | medium | low
- Where: `path:line`, and more places when it spans them
- Situation: <the concrete input or state>
- Happens: <what the code does then>
- Should: <what it should do, citing the work order, the plan or the rule>
- Test: <the test that would show it, or "none exists">

## F2 — …
```

With nothing to report: the two header lines, then `No findings.` and one
sentence on what you checked hardest.

## Reviewing the seams

When the order is for the seams rather than one unit, every unit already has a
review of its own. Do not repeat those. Look only at what lies between units:

- a contract the plan's preamble or authority defines: does every unit that
  touches it honour it the same way?
- work one handover says it left for a later unit: did a later unit do it?
- code a unit added that nothing uses by the end of the span, or that a later
  unit replaced without removing;
- tests a later unit deleted or weakened;
- documents the plan or the repository's rules say the change must update, and
  that no unit updated.

Steps 1, 2 and 7 still apply, over the whole span; read the diff by file rather
than in full when it is large.

## Finishing

Write the findings file. Check that `git status --short` shows nothing you made.
End with `DONE`. `BLOCKED` only when the order cannot be carried out at all: the
commits do not exist, or the range is empty.

## Required versus optional

For each evidenced finding, propose whether it is required by correctness,
repository rules or the plan, or an optional improvement the completed work is
acceptable without. Give the reason. Triage makes the final decision. Do not
inflate optional work into a defect, treat a missing test alone as a defect,
or invent findings to fill the review. Unsupported concerns and taste are not
optional findings worth collecting. No findings is a successful result.
