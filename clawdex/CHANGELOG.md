# Changelog

## Unreleased

### Removed

- Breaking: remove the manual contact-management commands — `person add`, `person edit`, `person avatar`, `note add`, `note list`, and `timeline`. People come from imports; `person list`, `person show`, `who`, and `search` read them. Existing notes stay searchable.
- Remove the GitHub Pages site (index.html, docs/, llms.txt) from the repo.

## 0.1.0 - 2026-05-08

- Initial `clawdex` CLI with markdown-backed people, timestamped notes, search, timeline, Git helpers, vCard export, and repair for damaged frontmatter.
- Added Apple Contacts import on macOS, Google Contacts import through `gog`, Discord DM backfill through Discrawl, and X/Twitter DM backfill through Birdclaw.
- Added local avatar support with manual avatar commands, Apple and Google avatar backfill, avatar repair checks, and optional vCard `PHOTO` export.
- Added CI with lint, tests, 90% coverage enforcement, race tests, dependency checks, secret scanning, and GoReleaser snapshot validation.
- Added GoReleaser config and release workflow that publishes cross-platform binaries and dispatches the Homebrew tap formula updater.
