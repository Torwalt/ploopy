---
name: plan-writing
description: Turn a settled discussion with the author into a task document that `ploopy` can run one unit per fresh session. Use when the author asks for a plan, an implementation plan or a task document for a pass; not for a one-commit change.
---

# Writing a plan for the plan loop

A plan is a task document in the repository's plans directory (`docs/plans/`
unless `.ploopy.toml` says otherwise). `ploopy` runs it one unit per fresh agent
session. Each session gets the `plan-unit` skill, the plan's preamble and its own
unit, and nothing else from the plan. So write for a capable engineer who has
read the repository rules but not this conversation and not the other units.

## Process

1. Read the repository's agent instructions, its design documents and the source
   the pass changes. Resolve every landmark you will name against HEAD.
2. Ask the author the questions that are genuinely theirs — direction, feel,
   what to keep or delete — a few at a time, over as many rounds as it takes. Do
   not ask what the documents, the code or a sensible default already settle.
3. Record the answers as *Decisions settled with the author (date)*. Never cite
   a document's rationale as the author's intent unless the author wrote it. Ask
   instead.
4. Choose the shape (below) and write the plan.
5. Check `ploopy status`. If another plan is still in flight, say so to the
   author rather than closing it.
6. Run `ploopy lint <plan>` until it reports no errors, and read at least one
   unit as the executor will see it: `ploopy show <plan> <id>`.
7. Commit the plan, and tell the author to run `ploopy`: with no arguments it
   offers the open plans and asks for the rest.

## Shapes

**One file** — `<PASS>.md`. A preamble holding the status line, the settled
decisions and anything particular to the pass, then stages of coarse units. Use
it when the decisions fit in the preamble and each unit can be stated as an
outcome.

**Two files** — `<PASS>.md` is the authority and `<PASS>_EXECUTION.md` the unit
list. The authority holds the settled decisions (D1, D2, …), conflicts with
existing documents, assumptions, the model with exact algorithms and worked
examples, and constants, in numbered `## 1.` sections and no `## Stage`
headings. The loop finds the authority beside `_EXECUTION.md` by name, or from
an `authority:` front-matter line, and lists it in every session's reading. Use
it when many units share contracts, or when the pass has real algorithms to pin
down. Units then cite the contracts by number instead of restating them.

## The format the loop parses

```
---
test: yes
context: docs/SOME_DESIGN.md
---

# Title

Status: active task document, written <date> against `<short HEAD>`.

## Decisions settled with the author (<date>)

## Stage 1 — Title

### 1.1 Title

Test: yes

**Goal:** …

**Start from:** …

**Done when:** …
```

- Front matter is optional. Keys: `authority`, `context` (space-separated paths
  added to every session's reading), `test` (`yes` runs the repository's test
  command after every unit), `agent`, `effort`, `model` (defaults for `run`).
- Everything before the first `## Stage N` heading is the preamble, and every
  session gets it.
- Units are `### N.M Title` inside `## Stage N — Title`. Ids are unique,
  ascending, and `N` matches the stage.
- Optional fields go directly under a unit's heading, before its first prose:
  `Test: yes|no` overrides the plan's `test`; `Context: path …` adds reading for
  that unit only.
- Every unit states its goal as `**Goal:**` or `#### Goal`, and should say when
  it is done as `**Done when:**` or `#### Completion criteria`. Lint enforces the
  first and warns on the second.
- A `##` section that is not a stage, placed after the stages, is seen by no
  session. Use it for notes to the author, never for something a unit needs.
- No `Status:` lines on units. The loop records progress in `<PLAN>.state.json`
  beside the plan; never write that file.

## Writing units

- **Self-contained.** A session sees only the preamble and its unit. Never write
  "as in 2.3" or rely on a later unit's text. A contract two units share belongs
  in the preamble or the authority.
- **One unit, one coherent commit**, sized for one session. Split a unit whose
  description needs "and then" across unrelated parts of the code.
- **Green at every boundary.** Order units so that each leaves the repository's
  checks and tests passing.
- **Landmarks** name files, types and procedures as they are at HEAD, and the
  status line records which HEAD. The executor revalidates them; give them
  enough to find the place, not line numbers alone.
- **Done when** is checkable: a command, an `rg` that must come back empty, a
  test that must exist, an output that must show something.
- **Non-goals** where a neighbouring unit owns adjacent work, so a session does
  not reach into it.
- **Coarse or detailed.** A coarse unit is *Goal*, *Start from* and *Done when*.
  A detailed unit adds `####` sections: Goal, Preconditions, Current-code
  revalidation, Concrete implementation work, Explicit non-goals, Tests,
  Validation commands, Completion criteria, Expected commit boundary. Prefer
  coarse units; go detailed when the route matters or a step is easy to get
  wrong.
- **No gates.** No unit waits for a manual review or a measurement, and no unit
  invents a pass mark.

## What not to write

The `plan-unit` skill already tells every session how to revalidate landmarks,
treat drift, use and limit latitude, test, follow the repository rules, commit,
and hand over. Do not restate it in the preamble. The preamble carries only what
is particular to the pass: which layers a unit may or may not touch, fixtures,
procedures and traps specific to this code, and what earlier passes left that
this one depends on.

## Changing a plan in flight

- Edit units that have not landed freely, even while the loop is running: the
  plan is read again before every unit.
- Never renumber a landed unit: progress is keyed by id. Add new units with new
  ids.
- Drop a unit with `ploopy mark <plan> <id> skipped`, and redo one with
  `ploopy mark <plan> <id> pending`.
- `ploopy status <plan>` shows where the plan stands.

## Closing a pass

When the last unit lands, move what is still true into the long-term documents,
then delete the plan, its authority and its `.state.json`. Git keeps what was
planned.
