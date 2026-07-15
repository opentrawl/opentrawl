---
written_by: ai
---

# AGENTS.md

This repository is public. Everything committed here is published at
github.com/opentrawl/opentrawl.

## Public-data boundary

- Never commit personal archive content, private databases, real messages,
  contacts, locations, account identifiers, archive-derived counts, secrets or
  screenshots of real data.
- Test fixtures and examples are synthetic. Use invented people,
  `example.com` addresses and `+1555` phone numbers.
- Never copy private working context, archive content or task history here.
  Reimplement accepted product logic cleanly.
- Run `scripts/check-clean` before every commit. Fix findings; do not bypass the
  check.
- If material might identify a person or derive from a private archive, keep it
  out of this repository.

## Product and repository invariants

OpenTrawl is a local-first crawler suite: one `trawl` CLI and one Mac app over
source-native archives. Read [docs/vision.md](docs/vision.md) for the product
direction. Stable behaviour belongs in the relevant public contract, source
documentation and tests.

- Crawlers and the CLI use Go. The Mac app uses Swift and SwiftUI.
- Keep source archives separate and couple surfaces through the shared control
  contract. Derived layers consume that contract, not source internals.
- Prefer the smallest complete design: simple, explicit code; deep modules with
  small interfaces; one obvious path; no speculative knobs, fallbacks or
  compatibility machinery.
- Deterministic code owns IO, storage and mechanical facts. Semantic
  interpretation belongs to a model behind an explicit product seam.
- Human and machine output is bounded, clear when read cold and free of secrets
  and internal identifiers.
- Providers, credentials, endpoints and network effects come from accepted
  product scope and explicit configuration, never an ad hoc library default.
