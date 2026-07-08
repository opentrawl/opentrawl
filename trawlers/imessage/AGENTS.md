---
written_by: ai
---

# AGENTS.md

This file governs `imsgcrawl`.

## Boundary

`imsgcrawl` is the iMessage source crawler. It owns read-only access to the
local Messages database, source-native iMessage archive state, source-local
status/search/open commands, and the small contact-export surface that clawdex
can pull.

Do not add Gmail, Telegram, WhatsApp, Apple Contacts import, source cards,
clustering, canonical people, identity merge logic, or life-orientation logic
here. Those belong in the relevant source crawler, clawdex, or a higher-level
private aggregation layer.

Product code is Go. Keep source-specific SQLite parsing in this repo. Use
`github.com/openclaw/crawlkit` only for provider-neutral mechanics such as
control metadata, status payloads, output conventions, SQLite helpers, and
future archive/snapshot mechanics.

## Privacy

Messages data is private. Never commit or publish raw conversation contents,
names, phone numbers, emails, photos, attachments, account IDs, or local paths
that identify the user or other people unless the user explicitly consents.

When the user asks in chat for raw local command output, show the raw,
unredacted local interface output in chat exactly as produced. Do not use
`sed`, `awk`, `grep`, redaction filters, LLM summarization, or hand-edited
snippets to satisfy a raw-output request. The point is for the user to see the
full input/output context naturally. This raw/unredacted output is local-chat
only and must never leave the machine for GitHub, PRs, docs, tests, fixtures,
public comments, or any other external surface without explicit user consent.
Public PRs, docs, tests, and fixtures must use fake data, counts, schemas, or
redacted examples.

## CLI Shape

Follow `docs/contract.md`. The v0 contact export stays `display_name` +
`phone_numbers` only; do not extend it until clawdex, telecrawl, wacrawl,
and imsgcrawl change together.

## Engineering

Prefer simple, source-native code over framework-shaped abstractions. Do not
add flags, modes, daemons, background services, graph terminology, or generic
crawler-to-crawler interfaces before repeated crawler evidence requires them.

Keep files under about 500 lines of code. If a file grows past that, do the smallest
sensible refactor into human-named files unless Josh explicitly says a larger
file is acceptable.

Tests must use temporary SQLite fixtures. Do not touch the live
`~/Library/Messages/chat.db` in unit tests.
