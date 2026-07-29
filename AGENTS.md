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
- Install the repository hooks with `scripts/install-hooks`. Run
  `scripts/check-clean` before every commit.
- If material might identify a person or derive from a private archive, keep it
  out of this repository.

## Safeguards

- Do not bypass a repository check, weaken it, add a suppression or alter a
  fixture merely to make a change pass. If a safeguard is noisy, slow, obsolete
  or rewards worse code, bring Josh concrete evidence. With Josh's approval,
  simplify or remove it instead of gaming it.
- Safeguards are replaceable engineering policy, not product truth. A green
  check proves only its named mechanical property; it does not prove product
  quality, architecture, privacy or correctness.
- Safeguard rules and tests use generic synthetic canaries. Never embed real
  private values in a check.

## Prove the real operating path first

Prove the smallest clear product path end to end against the user's real
archive data before you add anything that protects or simulates that path.

- Do not add guardrails, tests, regression checks, mutation checks,
  compatibility protections, fixtures, mocks or synthetic data before the
  real path works.
- Run the actual product command or app surface against the real archive.
  Inspect the complete human-facing result and follow its next action through
  to completion.
- A fixture, example, mock, snapshot or synthetic archive cannot prove product
  behaviour.
- If the real path fails, fix only the observed failure and run the real path
  again. Do not build protection around behaviour that has not worked.
- Keep real archive content and proof outside this public repository. Record
  only the accepted product behaviour with invented private values.
- After Josh accepts the real path, add only the smallest tests or checks that
  preserve that accepted behaviour.
- Delete synthetic proof created before the real path worked. Do not keep it
  as evidence or turn it into a test.

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
