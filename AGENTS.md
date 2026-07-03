---
written_by: ai
---

# AGENTS.md

This repo is public. Everything committed here is published to
github.com/opentrawl/opentrawl. Read the privacy rules before any other
work.

## Privacy rules

1. Never commit personal data. That means: real message, mail, note or
   calendar content; archive databases; phone numbers; personal email
   addresses; account handles used as data (public code handles are
   fine); absolute paths containing a real username; screenshots showing
   real data; counts or statistics taken from a real person's archive.
2. Test data must be synthetic. Use `example.com` addresses, `+1555`
   phone numbers, and invented names.
3. The private workspace (`~/code/crawlers` on Josh's machine) is not
   part of this repo. Never copy a file from there into here. If logic
   must migrate, rewrite it clean; do not carry files or history across.
4. Run `scripts/check-clean` before every commit. CI runs it on every
   push. If it flags something, fix the content; never work around the
   check.
5. If you are unsure whether something is private, it is. Stop and ask.

## What this repo is

OpenTrawl: local-first crawlers for your digital life, one `trawl` CLI,
one Mac app. Read [docs/vision.md](docs/vision.md) first, then
[docs/roadmap.md](docs/roadmap.md).

## Layout and upstreams

Each crawler directory is a git subtree synced with an upstream repo.
Do not edit a subtree without knowing where the change will land; see
[docs/sync.md](docs/sync.md) and run `scripts/sync list`.

| directory | upstream | outbound path |
|---|---|---|
| crawlkit | openclaw/crawlkit | direct (maintainer) |
| imsgcrawl | openclaw/imsgcrawl | direct (admin) |
| photoscrawl | openclaw/photoscrawl | direct (admin) |
| telecrawl | openclaw/telecrawl | via joshp123/telecrawl fork + PR |
| wacrawl | openclaw/wacrawl | via joshp123/wacrawl fork + PR |
| clawdex | openclaw/clawdex | via joshp123/clawdex fork + PR |

Everything else (docs, scripts, the `trawl` CLI, the app) is
monorepo-native with no upstream.

## Documentation rules

All documentation — READMEs, docs/, PR text, error messages, anything a
person reads — follows [docs/style.md](docs/style.md) (the anti-slop
style: plain English, front-loaded, sentence case, no filler) and is
written for end users:

1. Respect the reader. Assume they are smart, busy, and new here.
2. Be conscious of their time: front-load the point, cut what does not
   help them act.
3. Write external-facing and explanatory, always — explain what and
   why, not internal status. Writing for outsiders is also what keeps
   internal thinking sharp.
4. Internal working files may exist but are held to the same standard;
   good notes produce good work.
5. Every generated document gets adversarial review against these rules
   and docs/style.md before it merges. Reviewers are told to refute,
   not to approve.

## Standards

- Go for crawlers and the CLI, SwiftUI for the Mac app.
- Code must be self-documenting: no magic constants, one obvious job per
  function. If a reader needs a comment to follow the code, rewrite the
  code.
- Keep files under about 500 lines.
- Prose (docs, PRs, commits) follows plain-language style: front-loaded,
  short sentences, sentence case, British English, no filler.
- Outputs of every crawler command must be readable by a human cold;
  that is the bar that makes them agent-safe too.
- Cognition belongs to models, shells stay deterministic. Code may do
  IO, batching, schema validation, budget caps, exact matching and
  mechanical transforms. Code must NOT rank, score, route by keyword,
  infer meaning, or judge quality with local heuristics — any semantic
  decision (is this a receipt? which item? what quality?) goes to a
  model. Deterministic where the contract demands determinism (archive
  substrate, refs, joins by exact identifiers); model-driven wherever
  meaning is extracted or judged.
- The deterministic carve-out, codified (strictly read, the rule above
  would ban search indexes — a full-text index is "ranking"; that is
  not the intent). Deterministic code MAY make a bounded semantic-ish
  choice only when all three hold: (1) same input must give the same
  output every time because agents retry against it (query-time
  stability is a contract property); (2) it operates on structure
  (string distance, term frequency, exact identifiers), not on meaning
  a model could judge better at the same layer; (3) a model call at
  that point is architecturally wrong (per-query or per-render latency)
  AND the model-driven alternative is precomputing at sync time — which
  must be considered and rejected in writing. Every carve-out is
  documented at its call site with this justification; an undocumented
  heuristic is a violation by default.
- Every agent-produced diff gets a principles review by a model against
  this file before it is committed — deterministic-vs-model boundary,
  boring/lean, output rules. Human review is for taste, not for
  catching principle violations.

## Upstream tool drift

Upstream tools such as `gogcli` and `crawlkit` move fast. Pin minimum
versions explicitly, and periodically re-check upstream for new
primitives before building workarounds. Concrete example: `gogcrawl`
originally paginated Gmail search because the pinned `gog` 0.11
predated `gog backup gmail`; the crawler now depends on the backup
pipeline instead.

## Pre-1.0: breaking changes are free

There is no external release and there are no external consumers.
Break contracts, schemas and CLIs whenever breaking is simpler than
compatibility — no shims, no deprecation periods, no migration code
for data that can be re-derived by one sync. Contract versions exist
for our own coherence. This ends at the first external release, not
before.

## Blockers are surfaced, not sat on

When work is aligned with the roadmap and ready, fire it — do not
announce and wait. When a task needs a human decision, put the
concrete question with options in front of Josh the moment it becomes
blocking, not in a status list. Track every blocked item with what
unblocks it. The inverse also holds: never invent work to look busy —
aligned and ready, or it doesn't run.

## Josh reviews inline, in the files

Josh reviews both ways: inline comments saved into the files
(`-> COMMENT` style) and chat messages. Before editing
any doc, re-read it from disk — pattern-editing against a remembered
version silently skips his commented lines and can clobber his review.
Treat comment lines as review to answer, never text to delete; remove
them only when resolving that comment with him.

## Output review protocol (suite-wide, lifted from photoscrawl per Josh)

The gate for any change that touches what a command emits is a MODEL
REVIEW, never a script. Deterministic checks own structure; quality
judgment belongs to a model. Before committing any output-shape
change:

1. Generate RAW transcripts of every permutation the change touches:
   every affected verb, JSON and human mode, crawler-direct AND
   trawl-rendered (trawl renders crawler JSON — that is the surface
   users and agents actually see). Raw means raw: full, untruncated,
   uncensored — a review over summarized output reviews nothing.
2. A model that did not write the change reviews those transcripts
   adversarially (refute, not approve) against the blind-person
   test: output must let a blind person understand it perfectly —
   what, who, where, when, with what certainty. Anything they could
   not parse (raw enums, machine ids, cache accounting) is slop;
   anything they would still have to ask about is missing.
3. crawlkit/conformance checks are tripwires that remember past
   defects. They are never sufficient and passing them proves
   nothing new. When the model review finds a defect class, add a
   tripwire so it cannot regress — but the review itself is the gate.
