---
written_by: ai
---

# Telegram

The Telegram crawler imports local Telegram Desktop `tdata` or native Telegram
for macOS Postbox data into a searchable SQLite archive.

## Source and storage

On macOS, sync prefers the native Postbox store when available and otherwise
uses Telegram Desktop:

```text
~/Library/Group Containers/6N38VWS5BX.ru.keepcoder.Telegram
~/Library/Application Support/Telegram Desktop/tdata
```

The archive is `~/.opentrawl/telegram/telegram.db`; archived media is under
`~/.opentrawl/telegram/media/`. Normal sync copies cached local media. Add
`--fetch-media` to request missing cloud media through an existing Telegram
session. That option does not launch Telegram or start a login flow.

Sync defaults to the latest 200 dialogs and 500 messages per dialog. Set either
limit to `0` for no limit:

```sh
trawl sync telegram --dialogs-limit 0 --messages-limit 0
```

Use `trawl sync telegram --path /path/to/copied/source` to import a copied
source explicitly.

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
