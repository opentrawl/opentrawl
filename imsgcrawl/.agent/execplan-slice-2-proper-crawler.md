---
written_by: ai
---

# Slice 2: turn imsgcrawl into a proper source-native iMessage crawler

This ExecPlan is a living document. The sections `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` must be kept up to date as work proceeds.

This plan follows `.agent/PLANS.md` in this repository.

## Purpose / Big Picture

After Slice 2, `imsgcrawl` should be more than a contact producer. It should maintain a local source-native archive of iMessage data that can answer status, chat listing, message reads, and search without repeatedly parsing the live Apple database. The user-visible proof is that a person can run:

    imsgcrawl sync
    imsgcrawl --json status
    imsgcrawl --json chats
    imsgcrawl --json messages --chat <chat-id>
    imsgcrawl --json search <query>

and get useful source-native iMessage results from a local archive while preserving privacy and avoiding canonical people logic.

This matters because contact export only solves the clawdex identity-entrypoint problem. A proper crawler should also make iMessage history usable as source evidence for future private source-review work, just as telecrawl and wacrawl own Telegram and WhatsApp source archives.

## Progress

- [x] (2026-06-06 01:05+02:00) Defined Slice 2 as separate from the contact-export slice.
- [x] (2026-06-06) Began Slice 2 implementation in the normal checkout after Slice 1 local tests and real smoke had passed.
- [x] (2026-06-06) Designed and created the local archive schema for handles, chats, participants, messages, FTS, and sync state.
- [x] (2026-06-06) Added first `sync`, `chats`, `messages`, and `search` command implementations.
- [x] (2026-06-06) Added privacy-preserving fixture tests for `sync`, archive-aware `status`, `chats`, `messages`, and `search`.
- [x] (2026-06-06) Smoked against the real local Messages database using a temporary archive under `/tmp`; source and archive aggregate counts matched, and private read commands exited successfully without printing rows.
- [x] (2026-06-06) Ran adversarial review and resolved accepted findings: preserved `chat_message_join`, added `attributedBody` text fallback, made search snippet-only, fixed corrupt-archive status, strengthened tests, tightened smoke instructions, and kept files under 400 LOC.

## Surprises & Discoveries

- Observation: iMessage email handles exist, but Slice 1 cannot carry them.
  Evidence: local aggregate checks found email-form iMessage handles. Counts and examples are private evidence and should not be committed to this public repo.

## Decision Log

- Decision: Slice 2 should use a source-native archive, not direct clawdex writes.
  Rationale: source crawlers own source facts and clawdex owns people. Writing Messages rows directly into clawdex would mix source evidence with canonical identity state.
  Date/Author: 2026-06-06 / Codex.

- Decision: Use source-native names: handles, chats, chat participants, messages, and attachments.
  Rationale: these are Apple Messages concepts visible in the database. They are understandable and avoid model-invented graph or ontology language.
  Date/Author: 2026-06-06 / Codex.

- Decision: Keep phone fallback display names only as a v0 compatibility behavior, and revisit it before clawdex treats imported crawler names as canonical human names.
  Rationale: when Messages has no human name, using the phone number as `display_name` keeps the current shared contact-export contract working, but it may be safer long term for crawlers to emit no name and let clawdex represent an unnamed phone-only contact.
  Date/Author: 2026-06-06 / Josh/Codex.

- Decision: Archive `chat_message_join` as `chat_messages` instead of storing one chat id on each message.
  Rationale: Apple Messages models chat membership as a join table. Preserving it avoids lossy guesses and keeps `chats` and `messages --chat` source-native.
  Date/Author: 2026-06-06 / Codex.

- Decision: `search` returns source ids and snippets, not full message text.
  Rationale: broad search is easier to run accidentally than `messages --chat`, so it should provide enough context to locate evidence without dumping full private message bodies.
  Date/Author: 2026-06-06 / Codex.

- Decision: Store message text from `message.text` first, with `message.attributedBody` streamtyped decode as a fallback.
  Rationale: real Messages rows often have empty plain text and readable `attributedBody`. Without this fallback the archive would look mostly blank and search would miss ordinary iMessage bodies.
  Date/Author: 2026-06-06 / Codex.

## Outcomes & Retrospective

In progress. Slice 2 now has a reviewed, tested archive/sync/read/search implementation.
The remaining work is final diff review, commit, and push.

## Context and Orientation

Slice 1 reads the live Messages database through a temporary snapshot and returns contact rows. Slice 2 should introduce an archive database under an `imsgcrawl` runtime directory, likely `~/.imsgcrawl/archive.db`, so read commands do not need to touch the live Apple database every time.

The live Messages database has at least these source tables: `handle`, `chat`, `chat_handle_join`, `message`, `chat_message_join`, `attachment`, and `message_attachment_join`. A source-native archive should preserve stable Apple IDs such as `ROWID` and `guid` where useful, service names such as `iMessage`, `SMS`, and `RCS`, timestamps, chat membership, and enough message fields for search and evidence. It should not create canonical people or merged identities.

## Plan of Work

First add an internal archive store, probably under `internal/store`, with a small schema:

- `handles`: source handle row id, handle value, service, uncanonicalized value when present.
- `chats`: source chat row id, guid, chat identifier, service name, display name, room name, archive flags.
- `chat_participants`: chat row id plus handle row id.
- `chat_messages`: chat row id plus message row id, preserving Apple's source join.
- `messages`: source message row id, guid, handle row id, date, service, from-me flag, text, attachment flag, and searchable body.
- `sync_state`: last sync time, source database path, source database modified time, and schema version.

Then add `sync`, which snapshots the live SQLite triad, reads source tables in deterministic order, and replaces or upserts the archive. Keep the first sync simple and full-replace unless real performance evidence requires incremental cursors.

After sync exists, add read commands:

- `status`: report archive counts, source freshness, latest message date, and privacy warnings.
- `chats`: list chats with source ids, display names when available, participant counts, message counts, and latest message date.
- `messages --chat <id>`: list messages for a selected chat with bounded limit and ascending/descending order.
- `search <query>`: use SQLite FTS over message text and return snippets plus source ids.

Only after these commands are stable should the repo consider a TUI or crawlkit snapshot/backup/mirror support.

## Concrete Steps

From the repository root, after Slice 1 passes:

    go test ./...
    imsgcrawl sync
    imsgcrawl --json status
    imsgcrawl --json chats --limit 1 >/dev/null
    imsgcrawl --json messages --chat <chat-id> --limit 1 >/dev/null
    imsgcrawl --json search --limit 1 <query> >/dev/null

The real local read/search smoke can print private chat titles, message text, or snippets. Send those outputs to `/dev/null` or a private temporary file unless Josh explicitly asks for raw local output in chat. Do not commit or publish that output.

## Validation and Acceptance

Acceptance requires fixture-backed tests for sync, chats, messages, and search; a real local smoke against a Messages snapshot; status output that distinguishes live source DB readability from archive freshness; and command output that does not leak private data in public docs.

Search must prove that a query returns a source message row with a stable source id, optional chat id, and snippet without returning full text. `messages --chat` must prove that a selected chat can be read without touching the live database after sync.

## Idempotence and Recovery

`sync` should be safe to rerun. A failed sync should not corrupt the previous archive; write into a transaction or temporary archive and swap only after success. If a schema changes, migrate explicitly or rebuild from the live Messages source.

## Artifacts and Notes

The first archive should be boring SQLite. Do not add embeddings, summaries, clustering, remote publishing, or background scheduling in this slice.

## Interfaces and Dependencies

Use `crawlkit/control` for metadata and status shape, `crawlkit/store` for SQLite hygiene, and app-owned packages for Messages schema parsing. If snapshot or backup export becomes necessary, use crawlkit snapshot/mirror mechanics after the archive schema is stable.

Revision note: Initial future-slice plan created alongside Slice 1 to keep the contact exporter from expanding into a full crawler prematurely.

Revision note: Removed an exact local checkout path from the plan so the public repo does not contain a private machine-specific path.
