## Required fixes, optional improvements, and dropped concerns

Use the same threshold in direct review and collected triage:

- Required: an evidenced defect, regression, violated repository rule, or unmet
  plan requirement. State the concrete trigger, actual and required behavior,
  and the code evidence. Only these go into the fix plan.
- Optional: a useful improvement, but the completed work is acceptable without
  it. Persist these together in one issue for this review pass. Leave organizing,
  splitting, prioritizing or discarding that collection to an interactive session.
- Dropped: speculation, unsupported concerns, taste, or duplicates. Briefly say why.

Size and severity do not decide whether a change is required. A missing test
alone is not a defect: explain the required behavior whose coverage is missing.
A design question alone is not permission to expand scope. Resolve design
choices needed for required fixes in the fix plan; defer optional design work.
Zero fixes and zero optional findings is a successful result. Never invent work
or create an empty fix plan or an empty follow-up issue to demonstrate effort.
The For the author summary always records the outcome, including zero findings,
and links the fix plan and follow-up document when present. Commit it even when
there is nothing else to write, then finish DONE.
