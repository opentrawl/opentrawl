---
written_by: ai
---

# The golden-path bar

The per-crawler definition of done. A crawler is v1-ready when every
box below is checked and proven — "proven" means command output,
conformance-harness results, or tests, not assertion. Derived from the
crawler quality rubric and the iMessage crawler evaluation (the
cleanest slice so far).

## Archive

- [ ] real archive database exists with source parity: counts in the
      archive match counts in the source store, proven by a comparison
      the crawler itself can print
- [ ] full-text search index with parity against archived rows
- [ ] explicit schema with a version; migrations are forward-only
- [ ] sync is incremental and resumable; a killed sync leaves a
      consistent archive
- [ ] empty source, missing source and corrupt archive all produce
      correct states (`empty`, `missing`, `error`) — not crashes, not
      lies

## Contract

- [ ] passes the conformance harness against a real archive
- [ ] all required commands present, grammar `verb args --json`
- [ ] metadata declares capabilities truthfully (no advertised verb
      missing, no working verb unadvertised)
- [ ] status declares headline counts worth showing a human
- [ ] contact export implemented and validated

## Output quality

- [ ] every field human-readable cold: real timestamps, display names,
      meaningful keys — no machine row IDs, no epochs
- [ ] all outputs bounded with explicit truncation
- [ ] no secret material in any output, error or log
- [ ] reads never mutate: search/open/status trigger no sync, no
      import, no writes
- [ ] renders properly across terminal widths: wrapping and column
      layout tested at narrow and wide sizes, no broken tables
- [ ] the screenshot test: a screenshot of any command's output is
      grokkable on its own — someone who has never seen the tool
      understands what they are looking at


## Product

- [ ] installs as a real binary (dev shell and tap); `go run` is not a
      product
- [ ] doctor diagnoses the real failure modes (missing grants, expired
      auth, missing source app) with exact remedies
- [ ] tests cover parity, empty, corrupt and truncation cases against
      real-shaped data
- [ ] README states what the crawler reads, what it stores, and what
      never leaves the machine
