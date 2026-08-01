---
written_by: ai
---

# iMessage

The iMessage crawler snapshots Apple Messages read-only and builds a local
SQLite archive for chat browsing, message search, person resolution and contact
export.

## Source and storage

The source is `~/Library/Messages/chat.db` with its SQLite sidecars. Update copies
the database to a temporary private snapshot before reading it; it never writes
to Messages.

The archive is `~/.opentrawl/imessage/imessage.db`. It contains private message
text, participants, chat metadata and attachment references. Keep it local.

## Commands

```sh
trawl update imessage
trawl imessage conversations --limit 20
trawl imessage messages --conversation LINK --limit 20
trawl search "words" --trawler imessage
trawl open LINK
```

The CLI uses normal text output. List commands are bounded and state how to
request more rows. Root `search` accepts `--who`, `--after` and `--before`;
one of those or search words is required.

`open` returns the matched message with a bounded window from its chat. Contact
export is intentionally narrow: display name and phone numbers only.

## Privacy and development

Never publish output from a real Messages database. Public examples and tests
use synthetic names, identifiers and temporary SQLite files.

Build from the monorepo root with `scripts/dev-bin`.
