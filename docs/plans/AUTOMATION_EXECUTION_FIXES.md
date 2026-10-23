---
test: yes
context: docs/plans/AUTOMATION.md README.md
---

# Fixes from the review of AUTOMATION_EXECUTION

Status: active task document, written 2026-10-23 against `a0bd85e` by the triage
of `docs/plans/AUTOMATION_EXECUTION_REVIEW.md`.

Each unit fixes a defect a review found and the triage confirmed. Any design
decisions below are the triage's recommendations. This plan implements them as
written unless the author revises the plan before running it.

`docs/plans/AUTOMATION.md` is the authority for the work being fixed; units cite
its sections as §N and its decisions as Dn. This plan's own decisions are named
F-D1 and F-D2 so they do not collide with those. A unit that changes what the
author sees updates the README section and the `skills/ploopy` text describing
it in the same commit. The loop's tests in `internal/loop` are the
specification of the judge: add to them, and change an existing one only where
a unit below says so.

## Recommended design decisions

### F-D1 — A run limited by `--until` or `--units` never follows up

**Choice:** a run started with `--until` or `--units` does not go on to the
next plan of its chain, even when the limit leaves the plan complete (a
one-unit plan with `--until 1.1`, or `--until` naming the last unit). Its
start line promises no follow-up. The chain also requires the run itself to
have ended `done`: a run whose `loop.Result.Status` is `failed` never chains,
even when the state file says every unit landed. The plans a chain starts
already clear `--until` and `--units` (`runChainedPlan`), so the rule applies
to the first plan only.

**Why:** the reviewed plan's unit 2.2 says "a plan stopped early (`--until`, a
block or `adjust --stop`) does not go on to its review", and §5 chains only a
plan that "was not stopped". The loop's `--until`/`--units` return is
`finish(...)` without `Result.Stopped`, so whether the plan happens to be
complete decides today, which the author cannot see when typing the flag.
Reading the run's own limit is the rule the author can predict; treating a
failed run as a stop keeps its error from being replaced by the chain's later
success.

### F-D2 — A rescue's decisions are stored in full beside the cut report

**Choice:** `state.Rescue` gains an additive field `decisions` (omitted when
empty) holding the report's *Decisions* section in full for a `decided`
rescue. `report` keeps its 4000-byte cut. `ploopy report`'s *Rescued* section
and the run's closing summary read `decisions` and fall back to the report's
`## Decisions` section for records written before the field existed.

**Why:** §9 asks for both the cut report and the decisions "in full" in the
report and under the summary's `decided` line, and a long *Diagnosis* puts the
decisions past the cut. An additive field is what the state file's frozen
format allows, keeps the cut that bounds the file, and is reversible: dropping
the field later loses nothing the report did not already hold.

Every unit:

- first shows the defect with a test that fails, where a test can show it, then
  fixes it and keeps the test;
- makes the smallest change that fixes the defect, in the code the unit names;
- implements cited recommended decisions as written and never makes another
  design decision itself; when the fix turns out to need one the plan does not
  make, it finishes BLOCKED and names the decision.

## Stage 1 — Settings

### 1.1 Count a file's agent, model and effort as prefilled, not given

**Goal:** a model and an effort that came from `.ploopy.toml` are reset to the
new harness's defaults when the author changes the agent on the board, as
values from the last run already are. Only flags and the plan's front matter
make `given.harness`, `given.model` and `given.effort` true (§3: the file
prefills, it does not lock).

**Evidence:** `cmd/ploopy/settings.go:160` — `g.harness, g.model, g.effort =
s.Harness != "", s.Model != "", s.Effort != ""` runs after the file fallbacks
above it, so a file value counts as given. With `[run] agent = "opencode"`,
`model = "custom/model"`, `effort = "max"` and the agent changed to claude on
the board, `normalize` (`cmd/ploopy/board.go:228-252`) keeps `custom/model`
and `max`, and Enter fails with `--effort for claude must be one of medium,
high, xhigh`. The Triage column already does it right: `triageDraft.given`
holds only flags.

**Start from:** `givenSettings` in `cmd/ploopy/settings.go`; `fill` (it already
skips a prefilled value by `g.prefilled`); `normalize` in
`cmd/ploopy/board.go`; `TestChangingTheAgentKeepsAGivenModelOrEffort` in
`cmd/ploopy/board_test.go` and the board tests in
`cmd/ploopy/settings_file_test.go`.

**Done when:** a test `TestChangingTheAgentResetsAFileModelAndEffort` opens the
board from a file agent, model and effort, changes the agent, and finds the new
harness's default model and effort; it fails before the change and passes after.
`TestChangingTheAgentKeepsAGivenModelOrEffort` still passes. `go test
./cmd/ploopy` is green.

### 1.2 Check a file effort against the harness the run settles on, naming the key

**Goal:** an `[run] effort` the harness does not offer is refused with a
message naming `[run] effort`, also when `[run] agent` is absent and the
harness comes from the last run or the default. An `[review] triage_effort`
the triage harness does not offer is refused naming `[review] triage_effort`.
§3: validation uses the flag's message with the key in place of the flag.

**Evidence:**
- `cmd/ploopy/settings.go:299-304` checks a file effort only when `s.Harness`
  is already known; with no `[run] agent`, `fill` (`:328-349`) picks the
  harness later and keeps the prefilled effort. With `[run] effort = "max"`
  and a last run on claude, the board carries `max` until Enter fails with
  `--effort for claude must be one of ...`, naming a flag nobody gave. Once
  unit 1.1 lands, `normalize` replaces it silently instead.
- `cmd/ploopy/triage.go:116-123` — with `[review] triage_effort = "max"` and
  no `triage_agent`, `validateTriage` fails with `the triage effort for claude
  must be one of ...`, not naming the key.

**Start from:** `givenSettings`, `fill` and `chooseSettings` in
`cmd/ploopy/settings.go`; `chooseTriageWithSources` and `validateTriage` in
`cmd/ploopy/triage.go` (its `sources` map says which values came from the
file).

**Done when:** tests show `[run] effort = "max"` with no `[run] agent` and a
last run on claude refused before the board opens with an error containing
`[run] effort`, and `[review] triage_effort = "max"` with no `triage_agent`
refused with an error containing `[review] triage_effort`; both fail before
the change. `go test ./cmd/ploopy` is green.

### 1.3 Say "from .ploopy.toml" only while a field holds the file's value

**Goal:** the board's source label "from .ploopy.toml" appears only while the
field still holds the value the file gave. Once the author changes the field,
or `normalize`/`normalizeTriage`/`normalizeRescue` replaces the value, the
label is gone; it comes back if the field returns to the file's value. §3: the
board shows where a value came from.

**Evidence:**
- `bedrockColumn` (`cmd/ploopy/board.go:351`: agent, model, effort, limit,
  peak), `automationColumn` (`:408`: follow up, rescue, model, effort),
  `runColumn` (`:446`) and `triageColumn` (`:325`) take `Source` from
  `given.prefilled(key)` or `triageDraft.sources`, fixed when the board opens.
- With `[review] triage_agent = "opencode"`, `triage_model`, `triage_effort`
  and the triage agent changed to claude, the board shows `opus · from
  .ploopy.toml` and `high · from .ploopy.toml`, values the file never held.
- With `[automation] follow_up = true` and follow-up turned off with `n`, the
  field reads `no · from .ploopy.toml`.

**Start from:** `given.fromFile`/`prefilled` in `cmd/ploopy/settings.go`
(record the file's value beside the label), `chooseTriageWithSources` in
`cmd/ploopy/triage.go`, and the column builders in `cmd/ploopy/board.go`.
`internal/ui/board.go` renders `Source` as given and needs no change.

**Done when:** tests change a prefilled Run field, the Automation follow-up
field and the triage agent on the board and find each changed field's
`Source` empty, and a field set back to the file's value labelled again; they
fail before the change. `go test ./cmd/ploopy` is green.

### 1.4 Show the chosen rescue reaching `loop.Options.Rescue`

**Goal:** a test proves that the rescue a run settles on reaches
`loop.Options.Rescue`, not only the control status the steering reads. The
reviewed unit 3.3's *Done when* asks for it; the current test proves the
steering path alone.

**Evidence:** `cmd/ploopy/run.go:348` (`Rescue: pick.rescue`) is the only
assignment to `loop.Options.Rescue`, and `Loop.steer()`
(`internal/loop/loop.go:607-613`) reads it only when `Options.Steering` is nil,
which happens when `control.Register` fails (`cmd/ploopy/steer.go:66-71`).
Deleting `Rescue: pick.rescue` leaves `TestARescueFromTheFileRunsAtTheStopPoint`
(`cmd/ploopy/run_chain_test.go:252`) and the rest of the suite green.

**Start from:** the `loop.Options` literal in `runOnce` in `cmd/ploopy/run.go`;
the rescue tests in `cmd/ploopy/settings_file_test.go` (file, flag and last run
already reach `chosenRun.rescue`). Moving the literal into a function of
`chosenRun` and the flags, called by `runOnce`, is enough to test it.

**Done when:** a test builds the run's `loop.Options` from a `chosenRun`
holding a rescue and finds `Options.Rescue` set to that agent, and with no
rescue finds it nil; with `Rescue: pick.rescue` deleted the test fails. `go
test ./cmd/ploopy` is green.

## Stage 2 — The chain

### 2.1 Follow up only after a run that ended done and was not limited (F-D1)

**Goal:** implement F-D1. `followsUp` refuses a run whose `loop.Result.Status`
is not `done` and a run started with `--until` or `--units`. The start line's
follow-up suffix appears only when the run may follow up. The follow-up text
of `skills/ploopy/SKILL.md` (*Automation*) and the README's `follow_up` bullet
say that `--until` and `--units` end the chain.

**Evidence:**
- `cmd/ploopy/run.go:209-212` — `followsUp` checks `complete`,
  `result.Stopped` and follow-up. `--until`'s return in
  `internal/loop/loop.go:490-493` is `l.finish(...)` without `Stopped`, so
  `ploopy run --plan PASS --follow-up --until 1.1` on a one-unit plan writes
  and runs the review. The existing chain test passes only because its plan is
  left incomplete.
- `cmd/ploopy/run.go:389-393` — the start line calls
  `followUpSuffix(pick.followUp, p)` and promises ", then its review and fix
  plan" under `--until` on a plan it will leave open.
- Same procedure: when the last unit lands and a later git step fails
  (`commitState`'s `CommitOnly`, `internal/loop/loop.go:1391-1408`, or
  `l.reload()` at `:500`), `loop.Run` returns `failed` while `complete`
  (`cmd/ploopy/run.go:406-408`) reads true from the state file; the chain runs,
  and if the review and fix plan succeed, `runPlan`'s final check sees only
  their `done` and the original failure is lost.
- `followUpSuffix` (`cmd/ploopy/run.go:624-636`) has no test of its three
  cases.

**Start from:** `followsUp`, `planRun`, `runOnce` and `followUpSuffix` in
`cmd/ploopy/run.go` (`planRun` needs to know whether its run was limited);
`cmd/ploopy/run_chain_test.go`; `finishedPlan` there.

**Done when:** tests show: `finishedPlan` run with `--follow-up --until 1.1`
starts no review session and writes no review file; a run whose loop fails
after its last unit landed starts no review and exits with the loop's error;
`followUpSuffix` (or the start line) for an ordinary plan, a review, a fix
plan and a limited run. The first two fail before the change. `go test
./cmd/ploopy` is green.

### 2.2 Say the chain's next step truthfully for a fix plan and a finished review

**Goal:** a chained fix plan's live status carries no follow-up text, since a
fix plan never chains (D3). A finished review whose fix plan the chain is about
to run says it runs next, instead of telling the author to run it by hand.

**Evidence:**
- `cmd/ploopy/run.go:372-383` registers every plan with `FollowUp:
  &pick.followUp`, and `control.Settings.Describe`
  (`internal/control/control.go:130-137`) takes its ordinary-plan branch for a
  fix plan, so `ploopy status`/`ploopy adjust` during a chained fix plan read
  `no end action · then review and fix`. `followUpSuffix` already says nothing
  for a fix plan.
- `cmd/ploopy/run.go:410-414` calls `reviewOutcome`
  (`cmd/ploopy/review.go:327-339`) for every complete review; it prints "the
  fix plan is PASS_FIXES.md: run it with `ploopy run --plan PASS_FIXES.md`",
  and `chainedPlan` then prints "running its fix plan" and starts it.

**Start from:** the `announce` call in `runOnce` (register a nil `FollowUp`
when `review.IsFix(p.Path)`); `reviewOutcome` and its caller in `runOnce`,
which can tell from `pick.followUp` and the run's result whether the chain
takes the fix plan next. `Describe` keeps its signature.

**Done when:** a test runs a review with `--follow-up`, reads `control.List`
while a fix-plan session runs, and finds no "review and fix" in its
`Wanted.Describe`; a test captures the finished review's output under
`--follow-up` and finds no `run it with` line but a line saying the fix plan
runs next, and without follow-up finds the `run it with` line. Both fail before
the change. `go test ./cmd/ploopy` is green.

### 2.3 Apply `--triage-*` to a leftover review the chain resumes

**Goal:** when the chain finds the plan's review already written and not
complete, it resolves the triage as non-interactive `ploopy review` does on the
same review: `reviewTriage`, then `changeTriage`, so `--triage-*` flags rewrite
a triage that has not run, and a triage that has run stays as it is.

**Evidence:** `cmd/ploopy/run.go:215-232` (`chainedReview`) says "already
written; running it on" and calls `runChainedPlan` directly. `ploopy review`
on the same file (`cmd/ploopy/review.go:110-143`) calls `reviewTriage` and
`changeTriage`. After a run stopped before the review's triage landed, `ploopy
run --plan PASS --follow-up --triage-agent fake --triage-model fake-model`
runs the triage with the front matter's old agent.

**Start from:** `chainedReview` in `cmd/ploopy/run.go`; `reviewTriage`,
`changeTriage` and `triageLock` in `cmd/ploopy/review.go` (`chainedReview`
already builds a `reviewFlags` from the run's flags).

**Done when:** a test writes a review for a finished plan, runs it with
`--follow-up` and new `--triage-*` flags so the chain resumes it, and finds the
triage session started with the flags' agent and model and the review's front
matter rewritten; it fails before the change. `go test ./cmd/ploopy` is green.

### 2.4 Show the chain pushing and taking its end action once, after the fix plan

**Goal:** `TestAPlanWithFollowUpRunsItsReviewAndFixPlan` proves what the
reviewed unit 2.2's *Done when* requires: the push and the end action happen
once, after the fix plan.

**Evidence:** `cmd/ploopy/run_chain_test.go:141-149` counts `push` lines in
`pushes.log` but never asks the remote which commits arrived, and never sets
`--finish`. Moving `finishRun` before the fix plan, or taking the end action
per plan, leaves the suite green. `TestAPushHappensOnceAfterTheFixPlan`
(`cmd/ploopy/review_chain_test.go:472-509`) shows how to check the remote.

**Start from:** `cmd/ploopy/run_chain_test.go`; the remote and `systemctl`
fakes the review-chain tests use in `cmd/ploopy/review_chain_test.go`.

**Done when:** the test asserts the remote branch holds the fix plan's commits
and that a fake `systemctl` ran once with `--finish suspend --grace 0`; with
`finishRun` called after the review instead, it fails. `go test ./cmd/ploopy`
is green.

## Stage 3 — Rescue

### 3.1 Fail a rescue only for what the rescue itself changed

**Goal:** the rescue's judge compares the tree with the head and the dirt
taken before the rescue session, as `touched` does, so dirt the unit left
before the rescue does not fail it. §7: the loop takes the head and the dirt
before the session and compares afterwards.

**Evidence:** `internal/loop/rescue.go:119` takes only the head;
`judgeRescue` (`:261-283`) fails on any `DirtLines`. A unit session that leaves
`partial.txt` uncommitted and fails its retries gets a rescue that answers
RETRY and changes nothing, and the run stops with "the working tree is dirty
after the rescue". `failedAttempt` and `blockedAttempt`
(`internal/loop/loop.go:1473-1515`) leave the tree as the attempt left it, so
this is the common case.

**Start from:** `runRescue` and `judgeRescue` in `internal/loop/rescue.go`;
`touched` in `internal/loop/loop.go:1248-1261`;
`TestARescueThatLeavesTheTreeDirtyFailsAndLeavesIt` in
`internal/loop/rescue_test.go`.

**Done when:** a test whose unit leaves an uncommitted file and whose rescue
answers RETRY without touching the tree lands the unit on the next attempt; it
fails before the change. A rescue that adds dirt of its own still fails (the
existing test passes). `go test ./internal/loop` is green.

### 3.2 Keep a rescue's decisions in the prompt after a usage limit or a stop

**Goal:** after a DECIDED rescue, every later attempt of the unit is told the
decisions, including the session after a usage limit cut one off and the
session after the run was stopped mid-unit. §7: "every later attempt of the
unit in this run keeps the decisions".

**Evidence:** `internal/loop/loop.go:939-945` sets `failure = cutOffMidUnit`
and `:1280` sets `l.progress.Failure = stoppedMidUnit`, neither through
`withDecisions` (`:1490-1496`), which only `failedAttempt` and
`blockedAttempt` call. After a decided rescue, a session that changes
something and hits a usage limit hands the next session a prompt without the
decisions, though `progress.Decisions` still holds them.

**Start from:** the usage-limit branch of `runUnit` and `interrupted` in
`internal/loop/loop.go`; `withDecisions`;
`TestARescueDecisionLandsTheUnitAndKeepsTheDecisions` in
`internal/loop/rescue_test.go`.

**Done when:** a test with a decided rescue whose next session commits and then
hits a usage limit finds the decisions in the prompt of the session after it;
it fails before the change. `go test ./internal/loop` is green.

### 3.3 Keep the commits on a unit a rescue settles blocked

**Goal:** a unit settled blocked by a BLOCKED rescue records the commits git
shows for it, as a normal blocked settlement does. §7 settles it with the
rescue's reason; the record still says what git shows.

**Evidence:** `internal/loop/loop.go:1112-1113` (block trigger) replaces the
verdict with `Verdict{Kind: Blocked, Reason: result.reason, Marker:
harness.Blocked, Notes: verdict.Notes}`, dropping `verdict.Commits`; the
failed trigger (`:1172-1173`) and the transient trigger (`:1019`) build the
same verdict. `entry` (`:1357-1360`) writes `Commits` from the verdict, so a
session that committed and then blocked leaves `commits: []` beside notes
saying it committed.

**Start from:** the three `rescueBlocked` cases in `runUnit`; `entry` and
`settle` in `internal/loop/loop.go`; `l.repo.Commits(ctx, base)` gives what
the unit committed since its base.

**Done when:** a test where a session commits, then blocks, the second opinion
confirms and the rescue answers BLOCKED finds the commit on the blocked entry's
`Commits`; it fails before the change. `go test ./internal/loop` is green.

### 3.4 Keep a unit's earlier rescues when it settles again

**Goal:** when a unit that already has an entry with rescues settles again,
its entry keeps the earlier rescues and adds this run's. §9 / D7: every rescue
is recorded.

**Evidence:** `entry` (`internal/loop/loop.go:1387`) sets `Rescues:
l.progress.Rescues`; `settle` clears `progress.Rescues` after a blocked
settlement (`:1444`) and replaces the entry with `s.Set` (`:1438`, `:1452`). A
unit settled blocked after a BLOCKED rescue in run A and landed in run B
(`state.Open` treats blocked as open) loses run A's rescue from the state file,
`ploopy report` and the summary.

**Start from:** `entry` and `settle` in `internal/loop/loop.go` (`settle`
already reloads the state, so the existing entry is at hand).

**Done when:** a test settles a unit blocked through a BLOCKED rescue, resumes
the run so the unit lands, and finds both rescues on the landed entry; it fails
before the change. `go test ./internal/loop` is green.

### 3.5 Store a decided rescue's decisions in full (F-D2)

**Goal:** implement F-D2: `state.Rescue.Decisions` (`json:"decisions,omitempty"`)
holds the full *Decisions* section of a `decided` rescue; `recordRescue` fills
it; the *Rescued* section of `ploopy report` and the closing summary read it
and fall back to `Section("Decisions")` when it is empty.

**Evidence:** `internal/loop/rescue.go:231-243` cuts the report at 4000 bytes
before storing it; `internal/stats/report.go:468` and
`internal/ui/summary.go:138` take the decisions from that cut text through
`state.Rescue.Section` (`internal/state/state.go:71-85`). A Diagnosis longer
than 4000 bytes removes the `## Decisions` heading from the record, so the
report and the summary show no decisions, though the loop used them.

**Start from:** `state.Rescue` in `internal/state/state.go`; `recordRescue` and
the DECIDED case of `runRescue` in `internal/loop/rescue.go`; the *Rescued*
section in `internal/stats/report.go`; `rescues` in `internal/ui/summary.go`.

**Done when:** a loop test with a decided rescue whose Diagnosis exceeds 4000
bytes finds the full decisions on the entry's `decisions`; report and summary
tests with such a record show the decisions in full, and with an old record
holding only `report` show the section from it. The state round-trip test
covers the new field, and an old state file loads unchanged. The first fails
before the change. `go test ./...` is green.

### 3.6 List the rescues of a unit the run stopped on in the closing summary

**Goal:** the run's closing summary lists every rescue of the run, including
those on a unit the run stopped on without settling it (a failed rescue on the
`failed` or `transient` trigger, or retries that ran out after a RETRY). §9:
"the run's closing summary lists every rescue of the run". Those rescues stay
in `progress` and go onto the entry when the unit settles in a later run, as
§9 says; `ploopy report` is unchanged.

**Evidence:** `internal/loop/rescue.go:231-243` records into
`progress.Rescues`; `runUnit` returns an error without `settle`, so no entry
holds them. `cmd/ploopy/run.go:416` calls `Summary`, whose `rescues`
(`internal/ui/summary.go:110-145`) reads only state entries, so the run closes
with no *Rescued* line and no pointer for a rescue it ran and paid for.

**Start from:** `loop.Result` and `Run` in `internal/loop/loop.go` (the run can
return the unit id and `progress.Rescues` when it stops on an unsettled unit);
`Reporter.Summary` and `rescues` in `internal/ui/summary.go`; the summary call
in `runOnce`.

**Done when:** a loop test whose rescue fails on the `failed` trigger finds the
rescue in the returned `Result`; a summary test given such a rescue prints its
line under *Rescued* and the `review these` pointer; both fail before the
change. `go test ./...` is green.

### 3.7 Make the estimate test fail when rescues count

**Goal:** the test of `history.Estimates` leaving rescue sessions out fails
when the exclusion is removed.

**Evidence:** `internal/loop/rescue_test.go:373-375` asserts that the rescue
agent has no estimated units; rescue records carry no `landed` verdict, so
that holds with or without `if r.Role == "rescue" { continue }` in
`internal/history/estimate.go:118-120`. What the skip protects is the unit's
own agent: without it the rescue's agent marks the unit `mixed`, and the unit
drops out of its own agent's estimate.

**Start from:** `TestARescueIsMarkedInStatsAndHistoryAndLeftOutOfEstimates` in
`internal/loop/rescue_test.go`; `Estimates` in `internal/history/estimate.go`
and its tests.

**Done when:** the test asserts the unit's own agent has the landed unit in its
estimate (and the rescue's seconds are not in it); with the `rescue` skip
deleted from `Estimates`, it fails. `go test ./internal/loop
./internal/history` is green.

### 3.8 Remove the rescue fields nothing reads

**Goal:** `rescueAttempt.Info` and `.Tail`, `rescueResult.report` and
`.diagnosis`, and the `noteAttempt` parameters that exist only for them are
gone. No behaviour changes.

**Evidence:** `internal/loop/rescue.go:28-37`, `:61-69` and `:93-99` — the
fields are written and never read: `recordRescue` takes the report text it read
directly, and the prompt builder uses an attempt's `Outcome`, `Stem` and `Log`.

**Start from:** `internal/loop/rescue.go` and the `noteAttempt` callers in
`internal/loop/loop.go`.

**Done when:** `rg -n '\.Tail\b|\bInfo:|diagnosis' internal/loop/rescue.go`
finds none of the four fields; `go build -o /dev/null ./...`, `go vet ./...`
and `go test ./internal/loop` are green.

## Stage 4 — Documents

### 4.1 Say what leaving `rescue` out of `.ploopy.toml` does

Test: no

**Goal:** the README says that a `.ploopy.toml` without `rescue` falls back to
the last run's rescue and then to none, and that `rescue = "none"` turns it
off; `skills/ploopy/SKILL.md` agrees where it describes the key.

**Evidence:** `README.md:206-207` says "leaving `rescue` out, or setting it to
`none`, turns the rescue off". `fill` (`cmd/ploopy/settings.go:353-354`)
applies the last run's rescue when the file has none, so after an interactive
run with a rescue, removing the key does not turn it off.

**Start from:** the *Settings* section of `README.md`; the *Automation* section
of `skills/ploopy/SKILL.md`; `fill` in `cmd/ploopy/settings.go`.

**Done when:** `rg -n 'leaving .rescue. out' README.md skills` comes back
empty, and both documents name `none` as the way to turn the rescue off and the
last run as the fallback when the key is absent.
