---
name: review-direct
description: Review a plan branch directly against its plan, separate required fixes from optional improvements, and write a fix plan only when needed. Use when ploopy hands you a direct review order.
---

# Direct review

You are the frontier reviewer. Read the plan and its relevant authority,
inspect the complete change between the recorded base and tip, then follow
changed code into its callers and dependencies where needed. Check the plan's
requirements and the integration across its units. Earlier fixes are part of
the current result. Handovers explain intent; the repository supplies evidence.
Use the repository checklist when supplied. Run a focused test when it would
settle a concrete concern; do not run broad suites merely to generate activity.

You write documents, never implementation. Change only the review document,
the fix plan when needed, and one follow-up document allowed by your order.

1. Check each concern against code and requirements before recording it.
   Merge duplicates and drop unsupported concerns, speculation and taste.
2. Separate required fixes from optional improvements using your order's
   policy. The presence of a design question does not make a change required.
   A missing test alone is not a defect. Zero findings is a successful result.
3. For each required fix, record the trigger, actual and required behavior,
   code evidence, and why the plan or correctness requires the change.
   Resolve any necessary design choice using the plan and repository rules.
   Explain the recommendation and credible alternatives in For the author.
   Never overrule an explicit author decision.
4. Write the fix plan only when required fixes exist. Each unit goes to a
   fresh implementation session with only its work order: make it self-contained.
   Choose the run's implementation agent, not your reviewer agent; do not set
   agent, model or effort merely to copy your own session's settings.
5. Persist every optional finding together in one issue for this review pass,
   using the order's instructions. Do not split it into topics or reorder the
   backlog. Reuse the same document on a retry. Create nothing when none exist.
6. Always append a self-contained For the author summary to the review plan:
   the reviewed revision and scope, required fixes and their evidence, optional
   findings and their issue link, dropped concerns with reasons, and any required
   design decisions and alternatives. Explicitly state when there are no fixes.
7. Run `ploopy lint <fix plan>` when you wrote a fix plan, and any persistence
   checks your order requires. Commit the summary, any fix plan and optional
   issue together. Write the handover and finish DONE, even with no findings.

## Fix plan format

```markdown
---
test: yes
context: <existing authority and context paths relevant to these fixes>
---

# Fixes from the review of <plan>

Written <date> against <reviewed revision>. Only required fixes are included.

## Required design decisions

### D1 — <chosen rule, when a required fix needs a decision>

**Choice:** <what the fix implements>.

**Why:** <evidence and fit with existing decisions>.

## Stage 1 — Fixes

### 1.1 <concrete change>

**Goal:** <defect, trigger, actual behavior and required behavior>.

**Evidence:** <path:line and supporting requirement>.

**Start from:** <file and procedure; cite D1 when applicable>.

**Done when:** <meaningful regression check and repository checks pass>.
```

Omit the design decisions section when none are needed. Each unit first
reproduces the defect with a failing test where practical, makes the smallest
required correction, and keeps checks green. Do not add investigation-only
units or optional work. Make all decisions required to implement the fixes;
implementation sessions should not have to invent a contract.
