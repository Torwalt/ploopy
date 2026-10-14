---
name: land-repair
description: Repair a branch that ploopy is landing on the default branch — resolve the conflicts a rebase stopped on, or fix the check that fails once the rebase is done — keeping the intent of both sides. Use when `ploopy land` hands you a conflict or a failing check.
---

# Land repair

Two lines of finished, approved work meet here: the branch, and everything the
default branch gained while the branch was being written. Neither is wrong. Your
job is to make them hold together, not to choose one or redo either.

## Limits

- Never run `git rebase`, `git reset --hard`, `git checkout`/`git switch` to
  another branch, `git push` or `git clean`. ploopy owns the rebase and the push.
- Never rewrite a commit that already exists. During a conflict you commit
  nothing at all; after a failing check you add commits on top.
- Change only what the repair needs. A refactor, a rename or a cleanup the
  conflict does not force belongs to another day.
- Do not weaken, skip or delete a test to make it pass. A test that fails after
  the rebase is evidence of where the two sides disagree.

## A conflict

1. For each conflicting path, read both sides and the commit being replayed.
   `git log -p <onto> -- <path>` since the branch point shows why the default
   branch changed it; the replayed commit's message shows why the branch did.
2. Write the version that does what both commits meant. When one side moved or
   renamed code the other side edited, carry the edit to where the code now
   lives.
3. Remove every conflict marker, then `git add` the path. A path the
   resolution leaves identical to the default branch is fine: the rebase drops
   a commit that ends up empty.
4. When it is cheap and the tree builds at this commit, run the repository's
   fast check. An intermediate commit may fail for reasons later commits fix;
   that is not yours to repair now.

## A failing check

1. Read the failure and find the first real error, not the cascade after it.
2. Decide which side's assumption broke: a call the branch makes to something
   the default branch changed, a test the branch wrote against behaviour the
   default branch altered, or the other way round.
3. Fix the seam in the newer intent's favour. When the default branch
   deliberately changed behaviour, the branch adapts to it; when the branch is
   the deliberate change, the default branch's code adapts.
4. Run the checks the prompt names until they are green, then commit with the
   repository's own commit conventions.

## Blocked

Finish with `BLOCKED <reason>` when the two sides want contradictory behaviour
and no reading of either commit settles which wins. Say which paths, and what
each side wants, in a sentence each. That is a decision for the author.
