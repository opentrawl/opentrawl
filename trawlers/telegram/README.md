---
written_by: ai
---

# Telegram

The Telegram crawler imports Telegram for macOS Postbox data into a searchable
SQLite archive.

## Source and storage

Sync reads the native Postbox store:

```text
~/Library/Group Containers/6N38VWS5BX.ru.keepcoder.Telegram
```

The archive is `~/.opentrawl/telegram/telegram.db`; archived media is under
`~/.opentrawl/telegram/media/`. Normal sync copies cached local media. Add
`--fetch-media` to request missing cloud media through an existing Telegram
session. That option does not launch Telegram or start a login flow.

Use `trawl sync telegram --path /path/to/copied/source` to import a copied
Postbox source explicitly.

## Commands

```sh
trawl sync telegram
trawl sync telegram --fetch-media
trawl telegram status
trawl telegram folders
trawl telegram contacts
trawl telegram chats --limit 20
trawl telegram topics --chat CHAT_ID
trawl telegram messages --chat CHAT_ID --after 2026-01-01
trawl telegram search "invoice"
trawl telegram open telegram:msg/REF
```

Add `--json` for structured output. The archive preserves available folders,
topics, replies, pins, edits, forwards, reactions and media metadata as
source-native Telegram facts; it does not turn them into a cross-source schema.

## Privacy

Message text, chat and sender names, phone numbers, usernames, media metadata
and local paths remain private. Normal archive and search commands do not
upload them. A `--fetch-media` sync makes the explicit Telegram media request
described above.
