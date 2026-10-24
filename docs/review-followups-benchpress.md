# Persist review follow-ups with Benchpress

Read `.agents/skills/benchpress/SKILL.md` and `.benchpress.toml` to learn the
workbench directory, kinds, areas and topic format. If the skill is elsewhere,
use the repository's documented path. Follow these review-specific instructions
alongside the skill:

- Collect every optional finding from this review pass into **one new open
  topic**, even when the findings cover different subjects. This is a collection
  for a later interactive session to split, organize or throw away.
- Create nothing when there are no optional findings. Never invent findings.
- Use a stable slug derived from the reviewed plan name and the first twelve
  characters of its recorded `review-head`, such as `pass-review-abc123456789`.
  Reuse that same file on retries. Do not merge into a different pass's topic.
- Choose an allowed kind for a collection of proposed changes (`change` when
  available) and an allowed area fitting the reviewed work (`tooling` when it
  is a tooling pass). Keep `state: open`; do not attach a plan or settle it.
- Record the reviewed plan, full reviewed revision and date in the body, then
  each optional finding's evidence, proposed improvement and why it is optional.
  Keep it self-contained so deleting the original plan loses no needed context.
- Write only this topic. Leave `NEXT.md` and other topics alone. Do not
  prioritize, split, reorganize or discard this collection during the review.
- Run `benchpress lint`. Fix errors caused by your topic. If an unrelated error
  prevents validation, report it and finish BLOCKED without editing other topics.
- Link the topic from For the author and commit it with the review summary and
  any required-fixes plan. Required fixes belong only in the fix plan.
