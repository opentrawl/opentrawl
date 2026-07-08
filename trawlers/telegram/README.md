# ✈️ telecrawl

Telegram archive CLI.

`telecrawl` reads local Telegram Desktop `tdata` archives and native Telegram
for macOS Postbox databases, stores a searchable SQLite archive in
`~/.opentrawl/telegram/telegram.db`.

It is local-first:

- Normal archive/search commands do not upload data.
- Telegram message text, chat names, sender names, contact phone numbers,
  contact usernames, avatar path metadata, and media metadata stay local.

## Install

Build `trawl` from the monorepo root:

```bash
scripts/dev-bin
```

## Setup

No language runtime setup is required. `telecrawl` imports Telegram Desktop
`tdata` and native macOS Postbox data through `trawl`.

## Sync

```bash
trawl telegram doctor
trawl telegram sync
trawl telegram status
```

Sync defaults to:

- latest `200` dialogs
- latest `500` messages per dialog

Use `0` for no limit:

```bash
trawl telegram sync --dialogs-limit 0 --messages-limit 0
```

Add `--fetch-media` when you also want Telegram cloud media that is not cached
locally:

```bash
trawl telegram sync --dialogs-limit 0 --messages-limit 0 --fetch-media
```

Remote media fetches are bounded best-effort operations. Run with `-v` to see
how many remote media candidates were attempted, downloaded, still missing,
unavailable, timed out, or errored.

Repeat syncs reuse existing archived media for the same source before remote
fetch is attempted, so `--fetch-media` only tries media that is not already in
the local archive.

Native Postbox can tag link previews, polls, geo/live-geo, service messages, or
deleted messages as broad media candidates. `telecrawl` archives their decoded
message metadata separately from binary media, and only keeps them as media rows
when Telegram returns a downloadable file.
`metadata_json` is a local source-native Postbox payload for later rendering or
search; it is not a cross-source schema and can contain private Telegram
metadata.

When no `--path` is provided on macOS, `telecrawl` checks Telegram Desktop
`tdata` first, then the native Telegram for macOS group container. No backend
flag is needed. To import a copied archive directly:

```bash
trawl telegram sync --path "$HOME/Library/Group Containers/6N38VWS5BX.ru.keepcoder.Telegram"
```

Native macOS syncs include every local `account-*` database they find; if more
than one account is present, stored chat and sender IDs are account-scoped to
avoid collisions. They archive cached media by default and store Telegram peer
records as contacts for message enrichment. Contacts can include phone numbers,
usernames, and archived avatar paths when those values exist locally, and are
visible through `trawl telegram contacts`. `--fetch-media` also uses the existing
native Telegram session to fetch missing cloud media when account auth data is
present; this does not launch Telegram or start a login/2FA flow.

Useful reads:

```bash
trawl telegram folders
trawl telegram contacts
trawl telegram chats --limit 20
trawl telegram chats --folder FOLDER_ID
trawl telegram chats --unread
trawl telegram topics --chat CHAT_ID
trawl telegram messages --limit 20
trawl telegram messages --chat CHAT_ID --after 2026-01-01
trawl telegram messages --chat CHAT_ID --topic TOPIC_ID
trawl telegram messages --chat CHAT_ID --pinned
trawl telegram search "query"
trawl telegram search "query" --chat CHAT_ID --topic TOPIC_ID
```

Telegram folders, forum topics, reply/thread IDs, pinned messages, edits,
forwards, reactions, view/reply counts, and richer media titles are archived
when the local source or Telegram API exposes them for the active account.
Folder rows include explicit membership from Telegram dialog filters; dynamic
folder rules are recorded as metadata and may not expand to every matching
chat.

Add `--json` before the command for machine-readable output:

```bash
trawl --json telecrawl status
trawl --json telecrawl search "invoice"
```

## Data Paths

Defaults:

- Telegram Desktop source: `~/Library/Application Support/Telegram Desktop/tdata`
- native macOS Postbox source:
  `~/Library/Group Containers/6N38VWS5BX.ru.keepcoder.Telegram`
- archive DB: `~/.opentrawl/telegram/telegram.db`
- archived media copied from local Telegram caches, plus Telegram cloud media
  when `--fetch-media` is used: `~/.opentrawl/telegram/media/`
- config: `~/.opentrawl/telegram/config.toml`

Use a temporary home for tests:

```bash
test_home="$(mktemp -d)"
HOME="$test_home" trawl telegram status
```

Override the Telegram source:

```bash
trawl telegram doctor --path "/path/to/tdata"
trawl telegram sync --path "/path/to/tdata"
trawl telegram sync --path "/path/to/6N38VWS5BX.ru.keepcoder.Telegram"
```

## Reset

Remove local state:

```bash
rm -rf ~/.opentrawl/telegram
```

Remove only the archive:

```bash
rm -f ~/.opentrawl/telegram/telegram.db ~/.opentrawl/telegram/telegram.db-*
```
