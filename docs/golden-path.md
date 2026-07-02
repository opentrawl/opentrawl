---
written_by: ai
---

# The golden-path bar

The per-crawler definition of done. A crawler is v1-ready when every
box below is checked and proven — "proven" means command output,
conformance-harness results, or tests, not assertion.

This checklist is the operational form of [vision.md](vision.md): each
section enforces named design and engineering principles. It derives
from the crawler quality rubric and the iMessage crawler evaluation,
the cleanest slice so far. Use it three ways: as the lane brief when a
crawler is being hardened, as the review contract when the work comes
back, and as the conformance harness's manual counterpart — the
harness automates what can be automated; this file is the whole bar.

## Archive

Enforces: local first; federated, not unified — the archive is the
product, and it must be trustworthy before anything builds on it.

- [ ] real archive database exists with source parity: counts in the
      archive match counts in the source store, proven by a comparison
      the crawler itself can print
- [ ] full-text search index with parity against archived rows
- [ ] explicit schema with a version; migrations are forward-only
- [ ] sync is incremental and resumable: a second sync moves only the
      delta, a killed sync leaves a consistent archive, and both are
      proven by test
- [ ] empty source, missing source and corrupt archive all produce
      correct states (`empty`, `missing`, `error`) — not crashes, not
      lies
- [ ] freshness is honest: `status` reports when the archive last
      matched the source, not when a sync was last attempted

## Contract

Enforces: contract first; one obvious way — the contract is the plugin
API, and drift in one crawler taxes every consumer.

- [ ] passes the conformance harness against a real archive
- [ ] all required commands present, grammar `verb args --json`
      ([contract.md](contract.md))
- [ ] metadata declares capabilities truthfully: no advertised verb
      missing, no working verb unadvertised, contract version stated
- [ ] status declares headline counts worth showing a human — the
      Mac app renders these verbatim
- [ ] contact export implemented and validated against the crawlkit
      shape; every person-shaped record the source holds is exportable
- [ ] built on the crawlkit substrate (config, store, snapshot, sync
      state) rather than private reimplementations — observability and
      backup mechanics arrive free, and fixes land once

## Output quality

Enforces: agent first, human readable — down to the field; bounded
output everywhere.

- [ ] every field human-readable cold: real timestamps, display names,
      meaningful keys — no machine row IDs, no epochs, no null soup
- [ ] all outputs bounded with explicit truncation and a hint for
      narrowing; nothing unbounded under any flag combination
- [ ] no secret material in any output, error or log — auth state is
      booleans and expiry only
- [ ] reads never mutate: search/open/status trigger no sync, no
      import, no writes
- [ ] renders properly across terminal widths: wrapping and column
      layout tested at narrow and wide sizes, no broken tables
- [ ] the screenshot test: a screenshot of any command's output is
      grokkable on its own — someone who has never seen the tool
      understands what they are looking at

## Trust and proof

Enforces: local first, privacy by design; test against real inputs and
outputs.

- [ ] README states plainly what the crawler reads, what it stores,
      and what never leaves the machine
- [ ] the crawler can prove its own integrity on demand: parity
      comparison and archive checks are commands, not claims
- [ ] tests cover parity, empty, corrupt and truncation cases, and the
      critical paths are exercised against real-shaped, untruncated
      data — not hand-massaged fixtures
- [ ] no command prompts interactively; everything is safe for an
      agent to run headless

## Product

Enforces: agents run unimpeded; declarative, minimal install surface.

- [ ] installs as a real binary (dev shell and tap); `go run` is not a
      product
- [ ] doctor diagnoses the real failure modes — missing TCC grant (by
      canary read, per [tcc.md](tcc.md)), expired auth, missing source
      app, stale schema — each with the exact remedy command
- [ ] a fresh machine reaches a working crawl with at most the
      documented one-time setup; an agent reaches it with zero humans
      in the loop after that
- [ ] shows up correctly in `trawl status`, `trawl search` and the
      app, end to end
