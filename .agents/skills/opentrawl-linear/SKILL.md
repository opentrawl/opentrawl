---
name: opentrawl-linear
description: Manage OpenTrawl delivery and research in Linear. Use when orienting, auditing, shaping, claiming, coordinating, updating, landing or releasing OpenTrawl initiatives, projects, milestones, issues and dependencies; reconciling Linear with code and agent tasks; or preparing a low-noise portfolio view.
---

# OpenTrawl Linear

Use Linear as the durable index of product direction and delivery state. Keep it
cold-readable by a principal engineer joining the project. Do not turn it into
an execution log, a notification feed or a second source-code repository.

## Start from current truth

Before acting, inspect:

- the maintainer's current instruction and newest applicable Linear comments;
- applicable `AGENTS.md` files, the current product vision and guidance they
  reference;
- the live initiative, project, milestones, issues and dependency relations;
- current code and raw behaviour, `origin/main`, relevant worktrees and active
  agent tasks; and
- `scripts/linear/linear --help` plus the relevant subcommand help.

Treat old tickets, documents and task summaries as evidence, not authority.
Resolve conflicts in this order: current maintainer direction, current raw
behaviour, current Linear product contract, then older material. Repair stale
durable state when authorised; do not carry the correction only in chat.

Stay read-only when asked to orient, audit or propose. Get the maintainer's
alignment before materially changing a product promise, project boundary,
milestone meaning or research direction. Show the user-visible consequences
and a recommendation. Routine ticket administration does not need product
steering.

## Give each Linear object one job

- An initiative states one finite programme outcome and groups the projects
  required to achieve it.
- A project owns one outcome-led workstream with a clear end. Its overview
  explains the user promise, current problem, scope, exclusions, success
  criteria, milestone sequence, cross-project dependencies and human
  checkpoints.
- A milestone is a user-observable gate. Include only work required to prove
  that gate.
- An issue is a finite executable outcome or a bounded research question. Its
  description contains the durable contract and, while active, one terse claim.
- A dependency relation records a real execution constraint. Keep the graph
  acyclic; do not use relations for loose relevance.
- Labels classify work. They do not replace projects, milestones or state.

Do not create permanent project buckets for quality, engineering hygiene or
other acceptance standards. Put the rule in repository guidance and put each
defect in the project that owns the repair. Close completed or empty project
shells once their useful work has a proper home.

## Separate delivery from research

Delivery projects ship user-observable outcomes. Their accepted issues form an
executable dependency graph.

Research projects preserve intent and make later investigation discoverable.
State the decision question, why it matters, the falsification bar and the
product decision it could change. Link the canonical private brief identified
by repository guidance, including source discussions, prior art, relevant code
and superseded approaches. Linear indexes that material; it does not duplicate
it.

Keep unstarted research planned and out of delivery dependencies. When research
becomes active, give it one bounded question and one owner. Convert it into
delivery only after the product direction is aligned.

## Maximise useful parallel work

Parallelise accepted issues only when their dependencies are complete and their
ownership boundaries do not overlap. Give every active issue one primary owner,
one task, one branch or worktree and a bounded set of modules or interfaces.
Land shared contracts before parallel consumers. If two claims need the same
central file or semantic boundary, repair the split before continuing.

Until the wrapper supports atomic claims, one project coordinator serialises
claim mutations and reads each one back before the worker starts.

Record exact files only when collision or privacy risk warrants it. Keep machine
limits explicit. A worker owns implementation, boundary inspection,
proportionate tests, independent principles review, landing and claim release.

## Keep durable state current

Update Linear when the product contract, owner, claim, dependency, genuine
blocker, landing or stop changes. Do not record debugging rounds, provisional
findings, test transcripts, rebases or routine progress.

Move an issue to Done only when its accepted outcome is proved. For code, re-read
the maintainer's comments immediately before landing, confirm the reviewed
change is on the project's published main branch and check the user-observable
result. For research, confirm the accepted decision output exists in its
canonical workspace and Linear points to it. Replace the active claim with a
terse completion statement. Release or archive a worktree only after proving
that it is clean, landed and contains no unrelated work.

## Give visibility without notification spam

Use native state, priority, project, milestone, relations and descriptions for
routine truth. Agent-authored comments notify the maintainer, so reserve them
for a decision only the maintainer can make, an external action only they can
take, or a direct reply to their comment.

A portfolio view reads current Linear, code and active tasks. Report meaningful
outcomes, current user-visible gates, ownership conflicts, risks, decisions and
next unblocked work. Do not add comments merely to manufacture the view, and do
not maintain a second tracker.

## Use the OpenTrawl bot

Write only through `scripts/linear/linear` with a clear `--as` actor. Never use
the maintainer's personal connector, browser identity or token for a mutation.
Read every mutation back through a separate command and compare it with the
intended state. Stop on mismatch.

Use the URL returned by the wrapper to make descriptive links. In a local
maintainer-facing surface, prefer a Linear desktop deep link when available.
Never present an unexplained list of ticket identifiers.

If the wrapper lacks a required operation, report one tooling gap. Do not
bypass the bot, and do not use a comment as a substitute for a missing verb.

## Stop the affected action

Stop when ownership overlaps, work is unaccepted or blocked, a product decision
is unresolved, current behaviour disproves the contract, a write fails to read
back, evidence is unsafe, independent review blocks, or landing would include
unrelated work. Keep independent work moving.
