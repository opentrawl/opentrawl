---
written_by: ai
---

# AGENTS.md

## Purpose

`photoscrawl` is a local-first OpenTrawl/trawlkit crawler for Apple Photos. It
builds a provenance-backed `photoscrawl.db` archive from a user's Photos
library without uploading private media by default.

## Stack

- Product code is Go.
- Use `github.com/opentrawl/opentrawl/trawlkit` for SQLite hygiene, JSON output, status
  shape, snapshots, state cursors, vector/embedding primitives when needed, and
  future TUI pieces.
- This is a trawlkit-family repo. Follow current trawlkit conventions for
  config, data, cache, logs, control/status metadata, and runtime paths. If the
  repo drifts from trawlkit conventions, fix the drift when touching nearby
  code; do not only report it.
- Do not add legacy compatibility paths, fallback runtime roots, or repo-local
  path shims. Migrate old photoscrawl path handling to current trawlkit
  semantics instead of preserving `~/.photoscrawl` or `PHOTOSCRAWL_HOME` as
  product behavior.
- Darwin-only cgo bridges to Apple frameworks are allowed when PhotoKit, Vision,
  CoreLocation, or Core ML require them. Keep the bridge narrow and expose a Go
  interface.
- Do not add Swift, Python, Node, shell pipelines, or ad-hoc scripts to the
  product path.
- Tests must not touch the live Photos library. Use temp SQLite files and small
  synthetic fixtures only.
- Boy Scout rule: every touched path should be simpler, more consistent, or
  better aligned with trawlkit than before. Small cleanup beats TODO drift.

## Product Boundaries

- NO PRIVATE DATA IN THE REPO. Do not commit, stage, copy, or write private
  Photos data into this checkout: Photos libraries, `photoscrawl.db`, snapshots,
  thumbnails, originals, exported media, extracted metadata dumps, GPS dumps,
  face data, OCR text, classifier output, logs containing asset metadata, or
  any other user-derived archive material.
- Keep private crawl artifacts outside the repo under the current trawlkit
  runtime data/cache/state dirs, or `/tmp/` for short-lived fixtures. Existing
  local dotdir artifacts are migration inputs, not product-path conventions.
- If verification needs real Photos access, run it read-only and report counts
  or synthetic examples only. Do not paste or save private asset identifiers,
  filenames, locations, OCR text, people labels, or media-derived content into
  tracked files.
- If Josh explicitly asks to see real example inputs/outputs in the chat, use
  real user-supplied/local data and reproduce the tool/provider output
  verbatim. Do not summarize, redact, paraphrase, normalize, or "clean up" those
  examples unless he asks for redaction or transformation. This exception is for
  conversation output only; never commit private examples or private provider
  results to the repo.
- Keep public repo language user-helping and privacy-first. Do not add framing
  that makes the project sound like coercive profiling, public-sector targeting,
  data-broker tooling, dossier building, investigations, or unrelated casework.
  This is open source software for users to understand their own Photos data.
- Read from Apple Photos only through explicit read-only/snapshot flows.
- Never mutate Photos, albums, metadata, faces, or iCloud state.
- Cloud model calls are opt-in only and must identify exactly which assets or
  derived thumbnails leave the machine.
- Store observations, internal provenance, and candidate signals. Do not create
  durable person, place, trip, relationship, or life-event truth tables in v1.
- CPU is acceptable when it buys signal quality. Disk pressure is not; classify
  originals through a bounded local cache/ringbuffer when downloads are needed.

## File Storage And Eval Artifacts

- Do not hide product/design work in random private state dirs. Private media,
  raw model outputs, OCR dumps, GPS dumps, and live-library eval results stay
  outside the repo; reusable prompts, prompt versions, eval harnesses, scoring
  rubrics, schemas, synthetic fixtures, and non-private design decisions belong
  in the repo.
- Private eval directories are scratch only. When an experiment teaches
  something durable, extract the non-private prompt/code/rubric into tracked
  files and leave the private directory as disposable run evidence.
- Do not create repo-local private `AGENTS.md` copies, `.agents-private`
  directories, ignored design docs, or `.git/info/exclude` rules as a substitute
  for proper tracked prompts/code. Use ignored/private files only for secrets,
  raw private artifacts, and one-off scratch that must never be committed.
- Model prompts should be first-class project artifacts. Keep the current
  classifier prompt text and prompt-change rationale in tracked files with no
  private examples. Use synthetic examples or heavily generalized examples when
  a prompt needs tests.
- Eval harnesses may run against a real local Photos library, but their outputs
  must default outside the repo. Do not commit real eval manifests, rendered
  images, metadata sidecars, OCR/barcode extracts, model responses, summaries, or
  reports.
- Eval code must respect the stack boundary. Product-path code is Go/trawlkit;
  temporary shell/Python snippets are acceptable only during exploration and
  should be promoted to Go or removed once the shape is known.

## Docs And Decisions

- Docs must stay current with the latest verified decision. If code, provider
  output, command output, or Josh's correction conflicts with docs, treat the
  docs as stale and update them.
- Do not cite docs as the only architectural authority for current decisions.
  Re-verify against code, live/private artifacts, provider docs, and Josh's
  latest direction.
- Update docs when behavior, API, or architecture decisions change; keep notes
  short.
- Do not document legacy paths, old env vars, or temporary compatibility
  behavior as supported architecture. If docs mention behavior the code should
  not keep, fix the code and docs in the same slice.

## Codex Goal Workflow

- When Josh asks to set, rewrite, or improve a goal, use the
  `session-goal-writer` skill from the Codex skills tree.
- Goals should preserve Josh's vision first, then state the current slice,
  constraints, deliverable, review proof, and completion proof. A plan,
  scaffold, or budget-limited answer is not completion.
- If a goal budget ends before the work is actually handled, do not shrink the
  task into a partial handoff. Re-align the goal with Josh and continue or state
  the exact blocker.

## Query Surface

Keep crawl-family commands:

- `metadata`
- `status`
- `doctor`
- `sync`
- `classify`
- `search`
- `open`

Research-only `photoscrawl-lab` verbs are not part of this query surface.

Provenance tables, evidence refs, raw provider responses, and model responses
are machine-internal pipeline storage. Do not expose an `evidence` command or
evidence refs/counts in `open`.

## Output Review Protocol (mandatory, all agents including subagents)

The gate for any change that touches what a command emits is a MODEL REVIEW,
never a script. ZFC: deterministic checks own structure; quality judgment
belongs to a model. Before committing any output-shape change:

1. Generate RAW transcripts of every permutation the change touches: every
   affected verb, JSON and human mode, photoscrawl-direct AND trawl-rendered
   (`trawl open`/`trawl search` render our JSON — that is the surface users
   and agents actually see). Include the model-input sidecar when the change
   touches classification. Raw means raw: full, untruncated, uncensored — a
   review over summarized output reviews nothing.
2. Have a model that did not write the change review those transcripts
   adversarially (refute, not approve) against the blind-person test below.
3. The contract regexes are tripwires that remember past defects. They are
   never sufficient and passing them proves nothing new. When the model
   review finds a defect class, add a tripwire so it cannot regress — but the
   review itself is the gate.

The blind-person test: both the card we SEND to the model (mechanical
context) and the card we STORE from it must let a blind person understand
the scene perfectly and exhaustively — what, where, when, with what device,
in what context, with what certainty. Anything a blind person could not
parse (raw enums, float noise, machine ids, cache accounting, provenance
strings) is slop; anything they would still have to ask about is missing.

## No invented label ontologies (ZFC)

Never mint new deterministic label kinds/enums to carry meaning a model
should express (ruled 2026-07-04, the "family home" case). Deterministic
kinds exist only where code must gate on them mechanically, and the
existing set is the ceiling until a gate genuinely needs more. Context
that is meaning, not gating — whose house, what relationship, why a
place matters — flows to the model as plain-language phrases and to
readers as prose, never as new enum vocabulary.

## Standing principles pass (recurring, not optional)

Slop compounds. After every few landed slices — and always before a
milestone claim — run a deslopify/ZFC/engineering-principles review
over the recent diff range (not the whole tree): a non-authoring model
reads the changes against this file, ../docs/vision.md's engineering
principles, and the no-ontologies rule, and files defects. Workers
drift (sandbox reverts, invented heuristics, knob creep) even with
good prompts; only a recurring pass catches the drift before failures
compound. Review evidence is RAW or it is not review: full unpiped,
untruncated, unmodified inputs and outputs — no greps, no head/tail,
no summaries between the artifact and the reviewer. Benchmark truth
commands are themselves audited the same way: run the check raw, open
every truth ref raw, confirm it is the real event before the truth is
frozen. Coordinate diff ranges with the crawlers session so passes
cover everything once and nothing twice.

## Observability Rule (ratified 2026-07-04)

Every pipeline phase and every per-item outcome logs one structured line with
its duration — successes included, not just failures. Silence is a defect: any
stage that can consume seconds must have its cost readable from the run log,
so bottlenecks are found by reading logs, never by profilers, CPU samplers, or
guessing. When a diagnosis needed evidence the log didn't have, fixing the
logging is part of the same change. This rule exists because a batch-selection
query silently burned minutes of CPU per batch for a full day (found only via
`sample`), and success-path carding ran for hours with zero log lines.
