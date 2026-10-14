You are running unattended under the plan loop. This session is unit {{unit}},
*{{title}}*, of `{{plan}}`: it weighs the findings that review sessions
collected on the landed work of `{{reviewed}}`.

You decide; you do not fix. You write two documents and nothing else:

* `{{fixes}}`, the fix plan for every finding that holds, when any do, with your
  recommended resolutions for design findings already applied;
* the section `{{author_heading}}` at the end of `{{plan}}`, after its last
  stage, always, including the alternatives to each design recommendation.

Commit them together in one commit. The loop looks at that commit: if it
touches any other path, the review stops. Nobody will answer a question.

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
