---
name: plan-unit
description: Implement exactly one unit of a task document and hand over to the next session. Use when a ploopy session hands you a work order, or when asked to carry out one named unit of a plan by hand.
---

# Implementing one plan unit

A work order is a plan's preamble plus one `### N.M` unit. Later units are
deliberately absent. You carry that one unit to a green commit, then hand over
to the session that does the next one. The plan loop decides from the
repository whether the unit landed: what counts is what you commit, and what
the checks say about it.

## The work is decided

- Implement only your unit. Do not begin a later one, even one that looks small.
  The unit's *Explicit non-goals*, when it has them, are binding.
- Every decision in the plan and its authority is already made. Implement it as
  written: do not redesign it, do not ask whether to proceed, and do not write a
  plan instead of the change. Where the plan and its authority disagree, the
  authority wins.
- Do not edit the plan, its authority or its `.state.json`, unless the unit says
  so. If a premise is wrong, say so in the handover. The loop records progress.

## Revalidate before editing

- Landmarks — paths, procedure names, line numbers — point into the source as it
  was when the plan was written. Find each with `rg` before relying on it.
- A renamed or moved landmark is ordinary drift: implement the contract against
  what is there, and name the drift in the handover and the commit body.
- An earlier unit may have landed differently from what the plan expected. The
  previous handover and the source are then the truth: preserve your unit's
  intended outcome on top of what actually exists.
- A landmark that is absent with no successor, or a decision the code makes
  impossible, is a stop: finish as blocked.

## Latitude, and its limit

- Concrete steps are a route, not a cage. Where a step is wrong against the real
  code, reach the unit's *Goal* and its completion criteria by the nearest route
  the plan allows, and say what you changed.
- A coarse unit names only an outcome and where to start. Reach it by the
  simplest route the architecture allows, and say in the commit body what you
  chose where the unit left it open.
- Latitude never widens scope.
- Constants and tuning values are starting values. Use them as given; never
  change one to make a test pass.
- No gates. Nothing waits for a manual review or a measurement. A measurement the
  unit asks for is evidence for the handover, not a pass mark.

## Tests

- Tests protect structure and contracts — ownership, lifecycle, ordering,
  invariants, determinism, algorithmic kernels — not incidental numbers. When a
  test needs a tuning value, read it from where the code defines it.
- When a unit replaces a behaviour, tests of the old behaviour are rewritten to
  the new contract or deleted, and deleted test files are named in the commit
  body. Never weaken an invariant to get green.
- Run the repository's own test commands as its instructions describe them.

## Repository rules

The repository's own agent instructions (`AGENTS.md`, `CLAUDE.md` or similar)
are binding and win over anything here. Beyond them:

- Run the checks the session prompt names. Both must be green before you commit.
- Report failures as failures, with the output.
- Update the documents the repository keeps current when the unit changes what
  they describe.
- Commits: a short subject that states the change; an optional body of at most
  three bullets. Never a unit number or plan name, no AI attribution, no
  `Co-authored-by` trailer. One unit is normally one commit.
- Never push, rebase, `reset --hard`, `clean`, or switch branches.
- Changes you did not make belong to the author, who may keep working while the
  loop runs. Never stash, revert, commit or edit them. If they leave the tree
  dirty, name them in the handover and leave them.

## When the loop retries you

If the prompt names a previous attempt's failure, fix that first. The repository
is as that attempt left it, and commits it made still count toward the unit. If
the unit's outcome is already committed and green, do not make a commit just to
have one: verify it and finish with `NOTHING-TO-DO`.

## Finishing

1. Leave the tree clean: everything that belongs to the unit committed, nothing
   else left behind.
2. Write the handover to the path the prompt gives, overwriting it. Run by hand
   with no path, put it in your final message. Under 80 lines, these sections:
   - **What landed** — the commits and what they do, briefly.
   - **Landmark drift** — what moved, and where it is now.
   - **Deviations from the work order** — what you did differently, and why.
   - **What the next session must know** — traps, facts you discovered, places
     where the plan is now wrong.
   - **Open problems** — what is unfinished or doubtful.

   The loop already knows the unit, its status and its commit hashes; do not
   spend lines on them. It is a handover, not a report.
3. End your final message with exactly one of these as its last line:
   - `DONE` — the unit is committed and green.
   - `NOTHING-TO-DO <reason>` — the outcome already exists at HEAD and you
     changed nothing.
   - `BLOCKED <reason>` — you cannot finish. Commit nothing that does not
     verify; say in the handover what stopped you and what is left uncommitted.
     A block goes to a fresh session to establish before it stops the run, so
     give a reason that one can check.
