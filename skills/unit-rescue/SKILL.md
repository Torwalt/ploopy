---
name: unit-rescue
description: Diagnose why the plan loop stopped at a unit, decide whether it was the work, the provider or the author, and write a report the loop acts on. Read-only: never edit, commit or land anything.
---

# Rescuing a unit

You are the rescue session. The loop stopped at one unit after its usual
retries, transient limit or confirmed block. You diagnose the stop; you do not
carry out the unit. You may read anything in the repository, run read-only
commands, and read the attempt logs. You write one report and nothing else.

You must not edit, stage, commit, stash, revert or land anything. The loop
compares the head and the working tree before and after your session: any
change fails the rescue, stops the run, and stays for the author to inspect.

## Tell the failure from the work

Read the attempts, the logs and the repository before deciding. A failure that
came from outside the unit's work order is not a reason to stop for the author.

Use `RETRY` when the cause lies outside the unit's work order. The signals:

- a provider failure: an empty completion, a request that stalled, a session
  killed after its work was already committed, a usage limit, an authentication
  or network failure that is no longer present;
- the same check failed for a reason unrelated to the change, or a flaky check
  that passes when run again;
- the session ended without a marker or with an exit status that does not
  describe the work, while the repository shows the unit was not attempted;
- the unit's base or worktree was in a state a fresh session can simply retry.

Use `DECIDED` when the unit stopped on a question the author would answer: a
design choice, an ambiguity in the work order, two reasonable implementations
with different costs, or a constraint the documents do not spell out but the
repository can settle. Decide it from the repository's documents and code, and
write the decision down. You stand in for the author; do not ask a question
back.

Use `BLOCKED` only when the author alone can unblock the unit: missing
credentials or access, a contradiction in the plan that cannot be resolved
from the documents, or a decision whose cost the documents do not settle. A
provider failure is never `BLOCKED`. A design choice the repository answers is
never `BLOCKED`.

## The report

Write the report to the path the prompt gives, outside the repository index, in
exactly these sections:

```
## Diagnosis
<what happened, with the evidence: log lines, commits, check results>

## Decisions
<DECIDED only: each decision, the options, why this one>

## Next attempt
<what the next session must know, addressed to it>
```

On `RETRY` you may end the report with one optional line:

```
Wait: 30m          # at most 2h
```

The loop waits that long instead of its usual backoff before the next attempt.
Without it, or with a longer duration, it uses its backoff or caps it at two
hours.

`Diagnosis` and `Next attempt` are required on every verdict. `Decisions` is
required on `DECIDED`, and the loop hands it to every later attempt of the
unit: it is settled, not a suggestion.

## The marker

The last line of your final message is exactly one of:

- `RETRY <one-line reason>`
- `DECIDED <one-line summary>`
- `BLOCKED <one-line reason>`

That line is what the loop reads. Keep it short and make the reason the one the
author needs to see. A session without one of these markers fails the rescue
and stops the run.

## After a retry or a decision

For `RETRY`, the next attempt is told what you diagnosed and reads the report's
*Next attempt*. Give it the evidence and the next step, not the unit's work
order again. For `DECIDED`, the next attempt is told the decisions are settled
and must record them in its handover. Do not reopen them.
