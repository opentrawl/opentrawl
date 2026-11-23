# Contributing to OpenTrawl

OpenTrawl is fully open source (MIT). You are welcome to fork it, inspect it,
run it, and build your own external plugins against `trawlkit/pluginruntime`
(`trawl plugin install`).

At the same time, OpenTrawl is maintained by a small team with a tight
architectural vision. **We are not accepting unsolicited feature pull requests.**

We are bottlenecked by review capacity and architectural coherence, not by
code or tokens. In almost every case, reviewing a pull request takes more
effort than writing it ourselves.

## What we do not accept (closed without review)

Unless a maintainer explicitly asked you to open a PR, we will close pull
requests for:

- New features
- New built-in trawlers or source providers (build an external standalone
  plugin using `trawl plugin install` instead)
- UI or Mac app overhauls
- Architecture changes (storage schemas, protobuf contracts, plugin/sandbox
  runtime, auth flows, or sync mechanisms)
- Support for additional operating systems or platforms
- AI-generated drive-by refactors, lint sweeps, or synthetic test suites

A pull request that changes how the product behaves is a feature, even if it is
labelled as a fix.

## How to help

- **Report bugs** in GitHub Issues with clear reproduction steps and your
  OpenTrawl version (`trawl --version`). Never paste personal archive content,
  real messages, contacts, or identifiers into public issues.
- **Share ideas** in GitHub Issues or Discussions instead of opening a PR.
- **Fix a real bug.** Daily users hitting a concrete crash, regression, or
  measurable performance defect may open a small, focused PR fixing that one
  bug.

## Rules for bug-fix pull requests

Every external PR — including drafts — must follow these rules. Following them
does not guarantee review or merge; we may close any PR without review.

1. **One bug per PR.** Keep the diff minimal and link the issue for anything
   beyond an obvious one-line fix.
2. **No private or synthetic data.** Read [AGENTS.md](AGENTS.md). Never commit
   real archive data, redacted personal records, or synthetic/mock archive
   fixtures.
3. **Pass local checks.** Run `scripts/check-clean`, `scripts/check-lint`, and
   `scripts/check-test` locally before opening the PR.
4. **Disclose AI assistance** and verify every line yourself against real local
   usage. Unreviewed AI-generated PRs will be closed immediately.
