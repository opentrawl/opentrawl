---
written_by: ai
---

# Roadmap

Companion to [vision.md](vision.md). Phases overlap; the contract and
the single entry point lead, per-crawler hardening runs in parallel
behind them.

## Decisions already made

- Hybrid ownership: fork only where blocked. crawlkit, imsgcrawl and
  photoscrawl sync directly with openclaw (maintainer access); telecrawl,
  wacrawl and clawdex sync through joshp123 forks; the Gmail, Calendar,
  Apple Notes and Signal crawlers are monorepo-native.
- One monorepo under the opentrawl org, open source (MIT), with
  attribution. Crawler directories are git subtrees; `scripts/sync`
  moves changes both ways.
- The Mac app is built from scratch in SwiftUI, minimum macOS one below
  current. Upstream crawlbar is a cherry-pick source (its control
  protocol doc and quality rubric are worth keeping; its settings UI is
  not).
- Federated architecture: per-source databases, one `trawl` CLI on top.
  No shared schema.
- Agent first, human readable, local first, no knobs, read only in v1.
- Privacy split: this public monorepo carries code and public docs only.
  Private working context stays out; `scripts/check-clean` enforces the
  mechanical part in CI.

## Phase 0: monorepo and hygiene

Mostly done at creation (2026-07-02): subtree imports with full history
for the six existing crawlers, sync script, privacy check in CI.

Remaining:

- Gmail and Calendar crawlers restart clean in the monorepo: the
  private prototypes' logic migrates by rewrite, not by history import.
- retire the local crawlbar PR worktrees after confirming their content
  landed upstream.

## Phase 1: contract v1 and the trawl skeleton

Goal: the single entry point exists from day one, and the contract
behind it is written down, versioned and testable. Everything that
follows is agent-testable through `trawl` from the start.

What exists versus what is missing: crawlkit's `control` package
already defines the manifest, the status envelope (including declared
counts) and the contact-export contract. What does not exist yet: read
surfaces (`search`, `open`) in the contract, bounded and paginated
output rules, the secrets rule (booleans and expiry only), human-shaped
output rules (real timestamps, human names, semantically meaningful
field names), error and progress shapes, and anything that enforces
contract compliance — today every crawler drifts its own way (different flag
grammars, missing metadata, unbounded dumps).

- design the full `trawl` CLI surface first, to
  [clig.dev](https://clig.dev) standard: `trawl status`, `trawl sync`,
  `trawl search`, `trawl open`, `trawl doctor`. The surface is the API;
  curate it hard and keep it small.
- ship a walking-skeleton `trawl` immediately: discovery via crawler
  manifests, `status` and `doctor` federation over whatever crawlers
  are installed. Thin, but real — the single entry point everything
  else plugs into and every agent tests through.
- extend the crawlkit control contract with the missing pieces above,
  pushed upstream as we go.
- define the golden-path bar as a checklist derived from the crawler
  quality rubric and the imsgcrawl evaluation, so "done" is mechanical
  per crawler.
- build contract checks that verify the shared shapes, bounds, secret
  leaks, and empty and corrupt archive behaviour. This is what makes the
  plugin story real.
- solve macOS TCC (privacy permissions) holistically before the
  architecture hardens. Crawlers read TCC-protected stores (Messages,
  Notes, Photos), so decide once who holds Full Disk Access — the Mac
  app with crawlers running as its children, a helper process the CLI
  talks to, or the user's terminal — and build that shape in from the
  start. [permiso](https://github.com/zats/permiso) covers the
  app-side permission UX; the terminal story needs its own answer.
  Getting this right early is what lets everything downstream run
  without bashing into permission walls.

## Phase 2: per-crawler hardening to the bar

Goal: every v1 crawler passes the contract checks and shows up
correctly in `trawl`. Highly parallel.

| crawler | state | main gaps |
|---|---|---|
| imsgcrawl | golden path: archive with source parity, FTS proven | trawl registration, human-shaped IDs and timestamps, attachment handling |
| telecrawl | works: archive and media proven | metadata drift, status envelope |
| wacrawl | archive works, readiness unproven | readiness proof, stop auto-sync on read, status envelope; watch WhatsApp passkey pairing risk |
| gogcrawl (new) | private prototype exists | rebuild clean in monorepo on top of upstream `gogcli`, which already owns Gmail auth, backup and export — gogcrawl adds the archive and contract layer, not another Gmail client |
| calcrawl (new) | private scaffold exists | rebuild clean in monorepo. Primary source: the local Calendar.app store (Calendar.sqlitedb in its group container — carries iCloud, Google and local calendars; snapshot pattern per docs/tcc.md). Google-direct via `gogcli` secondary. Archive, search |
| clawdex | contact layer works | adopt the crawlkit contact-export contract, contract compliance, import loop from all v1 crawlers; storage review: markdown-as-truth is right for human-curated identities, but verify it holds at import scale — crawler contact dumps must merge into curated people, never auto-generate thousands of person files |

Contact export is a v1 requirement for every crawler, because identity
joins are the horizon and re-crawling later is the expensive path.

Before fanning out, cut the lane boundaries and standardise the work
itself: one brief template per crawler lane — same golden-path
checklist, same PR shape, same review gates, same proof format — so
parallel work lands uniformly instead of as six bespoke
interpretations.

## Phase 2.5: the proof — smoke suite

Goal: demonstrate, not assert, that the suite works. This is the
acceptance gate for everything: Josh reviews smoke results, not diffs.
"Works" means all four of these, per crawler and across them:

1. Fresh: every archive syncs to now, and freshness reporting is
   honest (status says when the archive last matched the source).
2. Useful: real questions get real answers through `trawl` — find a
   specific conversation across sources, who said what when, what
   happened last week, who is in which group — with correct,
   verifiable results, not plausible ones.
3. Correct: parity between archive and source proven by the crawler's
   own commands wherever the source store is countable.
4. Documented truthfully: each crawler's README claims match observed
   behaviour — commands exist as documented, outputs look as shown,
   privacy statements hold. Doc drift is a smoke failure.

Mechanics: `scripts/smoke` lives in the repo and is generic (no
personal data in the script or in anything committed); its output is a
local-only report for Josh. Runs after every substantial change; the
contract checks cover the shared shapes, the smoke suite checks
reality.

## Phase 3: full federation

Goal: `trawl` grows from skeleton to the complete surface: `trawl
search <query> --source a,b`, `trawl open <ref>`, `trawl sync
<source>`, with output that interleaves sources and carries provenance
on every row.

- search federates across per-source FTS; the contract is the only
  coupling to crawlers.
- every surface stays clig.dev compliant and curated; adding a flag is
  a design decision, not a default response to a feature request.
- design hook for v2 deltas: sync cursors already exist in crawlkit, so
  `trawl diff --since 24h` is a federation feature later, not
  per-crawler work.

## Phase 4: the Mac app

Gated on two proofs (decided 2026-07-02): the TCC signing and
inheritance spike is done (it is — see docs/tcc.md), and phase 2.5
passes — the app builds on trust, so trust gets built first.

Also decided 2026-07-02: upstream PRs to openclaw are held until the
product is stable — one coherent pass per repo later instead of
drip-feeding changes that may still move. And contract v1 is ratified:
Josh's review of contract.md and cli.md is complete, so the command
grammar, field names, bounds and state enum are frozen; changes now
require a version bump, not an edit.

Goal: a consumer-grade menu bar app a human likes and trusts. Handles
authorisation flows, runs syncs, and shows per-crawler health at a
glance. No settings maze.

- each crawler self-declares its headline metrics through the counts it
  already reports in the status envelope — "N messages, M people, K
  chats since 2014" for iMessage, "N mails, M attachments, K senders"
  for Gmail. The app renders the top few declared counts plus freshness
  and auth state; it invents nothing per-crawler. Keep it KISS: no
  metrics framework, just declared counts.
- from scratch, SwiftUI, SwiftPM without an Xcode project, minimum
  macOS one below current.
- keep upstream crawlbar's quality rubric as the review contract for the
  app, and its control protocol as the compatibility line so upstream
  crawlers work unchanged.
- every UI change ships with before and after visual proof.

## v1.5 and v2

- v1.5: Apple Notes. Recovering note history is proven feasible
  (current-body decoding, WAL replay, snapshot and backup recovery); the
  extractor lands here when it works.
- v2: Signal spike (Signal Desktop keeps an SQLCipher database with the
  key in the OS keychain; assess a snapshot approach before committing),
  Photos, X, daily deltas, write capability through the upstream access
  CLIs, published plugin API, MCP or Executor adapter.

## Way of working

- roles: the newest Codex model (currently gpt-5.5) implements;
  Claude (Fable/Opus tier) orchestrates, reviews and owns taste.
  Anything user-facing needs the reviewer bar, not just the implementer
  bar.
- adversarial review on every substantial change: independent reviewers
  told to refute, not confirm. Reviews check explicitly against the
  vision doc's engineering principles, against the user's stated
  intent, and against real inputs and outputs — raw, unmodified,
  untruncated.
- all prose (docs, PRs, commits) follows plain-language style. Code must
  read as self-documenting or it does not merge.
- verification over assertion: a crawler change is done when the
  contract checks pass against a real archive AND a
  non-authoring model review of raw output transcripts passes the
  blind-person bar — not when it builds.
