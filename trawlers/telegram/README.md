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
`~/.opentrawl/telegram/media/`. Normal sync starts with the messages and media
cached by Telegram on this Mac.

Run `trawl sync telegram --full-history` to download older messages into
OpenTrawl through the existing Telegram session. The download is resumable.
After it completes, normal sync keeps the downloaded message archive current.
Attachments are separate: add `--fetch-media` to download missing files for
messages already in the archive. Neither option launches Telegram or starts a
login flow.

Use `trawl sync telegram --path /path/to/copied/source` to import a copied
Postbox source explicitly.

## Commands

```sh
trawl sync telegram
trawl sync telegram --full-history
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
and local paths remain private. Search and browse commands do not upload them.
Before full history is enabled, normal sync reads Telegram's local Postbox.
Full-history sync and later message syncs contact Telegram to keep that archive
current. A `--fetch-media` sync separately requests missing attachments.
