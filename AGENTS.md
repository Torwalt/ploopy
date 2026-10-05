# AGENTS.md

Shared guidance for AI coding agents working in this repo. This file is
committed and is the source of truth; `CLAUDE.md` just imports it.

## What this project is

ploopy drives a task document one unit per fresh coding-agent session. A
higher-intelligence agent writes the plan with the `plan-writing` skill; ploopy
then runs it, handing each unit to a fresh session of a *coding harness*
(Claude Code, opencode) and deciding from the repository whether the unit
landed.

ploopy is a **supervisor, never an agent loop**. It owns no model, no context
window, no tool implementations, no prompt caching.

## Ownership boundaries

- **The harness** owns the agent loop: context, tools, permissions, provider
  auth, retries, MCP, skills.
- **git** owns what is true. Whether a unit landed is read from the repository
  — commits since the unit's base, a clean tree, green checks — never from the
  agent's own account of itself.
- **ploopy** owns unit selection, judgement, retry policy, durable state, the
  prompt it hands over, and the terminal.

Never implement a tool call. Never talk to a model provider. Never trust a
session's self-report where the repository can be asked instead.

## Package boundaries

- `internal/plan` — parse and lint the Markdown task document; slice one unit's
  work order. Pure text: no git, no process execution.
- `internal/state` — `<PLAN>.state.json` beside the plan. The format is frozen;
  new fields are additive and omitted when empty.
- `internal/repo` — git process integration: head, dirt, commits since a base,
  ancestry, single-path commits, branches, worktree hand-off and the push at
  the end of a run. Must not
  import `loop` or `harness`.
- `internal/harness` — the `Harness`/`Session`/`Event` contract, the marker and
  limit parsers, the child-process runner, and the adapters under
  `harness/claude` and `harness/opencode`. Must not import `loop`.
- `internal/guard` — which git commands an unattended session may not run.
  Reads the invocation, not the string it was written as.
- `internal/config` — `.ploopy.toml`.
- `internal/catalog` — every plan of the repository and where its progress
  lives: checkouts, unmerged branches read through git objects, runs going on.
- `internal/control` — the runtime registry `ploopy adjust` reaches a running
  run through: one status file and one change file per process. Must not
  import `loop` or `harness`.
- `internal/loop` — sequencing, judgement, retry and wait policy, the prompt.
  Depends on everything above; knows no adapter by name.
- `internal/ui` — the selection form, the run renderer, the end actions.
  Nothing else writes to the terminal.

## Conventions

- Adapt at the **event** level, not the command line. The loop must never learn
  which harness it is driving.
- Interactive selection is the primary interface: `ploopy` with no arguments
  asks. Flags exist for non-interactive use and are never the path the author
  is expected to type.
- Keep the dependency set small: cobra, BurntSushi/toml, charmbracelet
  (huh, lipgloss), google/uuid. No viper, no logging framework.
- `context.Context` for every child process; process groups so a kill takes the
  whole tree. Signals cancel, and a cancelled run still judges and records the
  session it was running.
- Prefer simple, explicit code; no speculative abstractions. Comments explain
  *why*, not *what* — sparse and terse, one fact per sentence, no narration.
- Commits: a short subject stating the change, an optional body of at most
  three bullets. No AI attribution, no `Co-authored-by`, no plan or unit names.

## Tests

`internal/harness/fake` is a scripted harness: it acts on the repository the
way a session would, then reports whatever outcome a test asked for. The loop's
tests drive real git repositories in temporary directories through it, and they
are the specification of the judge — change one only when the contract changes.

The adapters are tested against a script named `claude` or `opencode` placed on
`PATH`, so argument building and output parsing are covered together.

## Verification

```
go build -o /dev/null ./...     # compile check without stray binaries
go vet ./... && gofmt -l .      # vet and formatting
go test ./...                   # unit tests (temp git repos, fake harnesses)
nix develop                     # go, claude-code, opencode (direnv auto-loads)
nix flake check                 # sandboxed build + tests
```
