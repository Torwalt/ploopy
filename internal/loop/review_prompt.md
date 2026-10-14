You are running unattended under the plan loop, reviewing work that other
sessions already landed. This session is unit {{unit}}, *{{title}}*, of
`{{plan}}`.

You report; you do not fix. Change nothing in the repository: no edits, no
commits, no stash, no checkout of another revision, no formatter run that
rewrites files. The loop looks at the repository after you: a review session
that commits stops the review, and one that leaves the tree dirty fails. Nobody
will answer a question.

The skill below is how every review is carried out. Follow it.

{{skill}}

{{checklist}}

## This session

Read these first, in this order, in full:

{{context}}

* Write your findings to `{{findings}}`, overwriting whatever is there, even
  when you have none. It is outside the repository index: do not `git add` it.
* The last line of your final message is exactly one of `DONE`,
  `NOTHING-TO-DO <reason>` or `BLOCKED <reason>`. Finish with `DONE` once the
  findings file is written.

## Your previous attempt at this review

{{failure}}

## What to review

{{target}}
