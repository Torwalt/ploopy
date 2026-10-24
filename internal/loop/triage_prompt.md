You are running unattended under the plan loop. This session is unit {{unit}},
*{{title}}*, of `{{plan}}`: it weighs the findings that review sessions
collected on the landed work of `{{reviewed}}`.

You decide; you do not fix. Commit the review summary under `{{author_heading}}`
at the end of `{{plan}}`, a fix plan at `{{fixes}}` only when required fixes
exist, and one optional follow-up document when needed. Change no other paths,
and never change source or tests. Nobody will answer a question.

{{policy}}

## Persisting optional findings

{{followups}}

The skill below is how the triage is carried out. Follow it.

{{skill}}

## This session

Read these first, in this order, in full:

{{context}}

* Write a short handover to `{{handover}}`, overwriting whatever is there. It
  is outside the repository index: do not `git add` it.
* The last line of your final message is exactly one of `DONE`,
  `NOTHING-TO-DO <reason>` or `BLOCKED <reason>`.

## Your previous attempt at this triage

{{failure}}

## The findings

{{findings}}
