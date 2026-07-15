---
written_by: ai
---

# iMessage

The iMessage crawler snapshots Apple Messages read-only and builds a local
SQLite archive for chat browsing, message search, person resolution and contact
export.

## Source and storage

The source is `~/Library/Messages/chat.db` with its SQLite sidecars. Sync copies
the database to a temporary private snapshot before reading it; it never writes
to Messages.

The archive is `~/.opentrawl/imessage/imessage.db`. It contains private message
text, participants, chat metadata and attachment references. Keep it local.

## Commands

```sh
trawl imessage doctor
trawl imessage sync
trawl imessage status
trawl imessage chats --limit 20
trawl imessage messages --chat CHAT_ID --limit 20
trawl imessage who "Alice Example"
trawl imessage search "candles budget" --who "Alice Example"
trawl imessage open imessage:msg/8831
trawl imessage contacts export
```

Add `--json` for structured output. List commands are bounded and state how to
request more rows. Search accepts a query, `--who`, `--after` and `--before`;
one of those is required.

`open` returns the matched message with a bounded window from its chat. Contact
export is intentionally narrow: display name and phone numbers only.

## Privacy and development

Never publish output from a real Messages database. Public examples and tests
use synthetic names, identifiers and temporary SQLite files.

Build from the monorepo root with `scripts/dev-bin`.
