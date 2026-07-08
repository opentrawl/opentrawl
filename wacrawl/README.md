# 🧾 wacrawl

WhatsApp local archive and search.

Read-only local archive and search for the macOS WhatsApp Desktop app.

`wacrawl` copies WhatsApp Desktop's local SQLite databases into a temporary
snapshot, imports the useful chat data into its own SQLite archive, and gives
you scriptable commands for status, chat listing, message listing, and full-text
search.

It is for local inspection. It does not send messages, talk to WhatsApp Web, or
write back into WhatsApp's app container.

## Install

Build `trawl` from the monorepo root:

```bash
scripts/dev-bin
```

## Quick Start

First, check whether `wacrawl` can see the local WhatsApp Desktop data:

```bash
trawl whatsapp doctor
```

Sync a fresh local archive:

```bash
trawl whatsapp sync
```

Inspect what was imported:

```bash
trawl whatsapp status
trawl whatsapp chats --limit 20
trawl whatsapp unread --limit 20
trawl whatsapp messages --limit 20
```

Search message text:

```bash
trawl whatsapp search "release notes"
```

Use JSON for scripts:

```bash
trawl --json wacrawl search "invoice" --from-them --after 2026-01-01
```

## What It Reads

On macOS, WhatsApp Desktop stores app data in:

```text
~/Library/Group Containers/group.net.whatsapp.WhatsApp.shared
```

`wacrawl` currently imports from:

```text
ChatStorage.sqlite
ContactsV2.sqlite
Message/Media/
```

It writes its own archive to:

```text
~/.opentrawl/whatsapp/whatsapp.db
```

Set the WhatsApp source path in `~/.opentrawl/whatsapp/config.toml`:

```toml
source = "~/Library/Group Containers/group.net.whatsapp.WhatsApp.shared"
```

Use a temporary `HOME` when you want a separate test archive.

## Safety

- Opens WhatsApp data read-only.
- Copies SQLite database, WAL, and SHM files into a temp snapshot before import.
- Replaces only the `wacrawl` archive database.
- Does not modify WhatsApp databases, settings, contacts, chats, or media.
- Does not use the WhatsApp network protocol.
- Does not upload data during normal archive/search commands.

The archive can contain private message data. Keep `~/.opentrawl/whatsapp/whatsapp.db`
local and out of commits, backups, and shared logs unless that is intentional.

## Commands

### `doctor`

Inspect the source path and database shape:

```bash
trawl whatsapp doctor
trawl --json wacrawl doctor
```

Reports source availability, discovered database files, row counts, message date
range, and importer schema notes.

### `sync`

Snapshot WhatsApp Desktop data and replace the local archive in one transaction:

```bash
trawl whatsapp sync
```

Imports:

- chats
- contacts
- groups
- group participants
- messages
- media metadata and local media paths

By default, media paths continue to point at WhatsApp Desktop's app container.
Set `copy_media = true` in `~/.opentrawl/whatsapp/config.toml` to copy
referenced media files into `media/` next to the archive database and rewrite
copied message media paths to that archive copy.
Missing media files are counted in the import output and do not fail the import.

### `status`

Show archive counts and metadata:

```bash
trawl whatsapp status
```

Includes message, media-message, chat, unread-chat, unread-message, contact,
group, participant, source, and timestamp fields when they are available.

`status` reads the existing archive. Run `trawl whatsapp sync` when you want to
refresh from WhatsApp Desktop.

### `chats`

List chats ordered by newest message:

```bash
trawl whatsapp chats
trawl whatsapp chats --limit 100
trawl whatsapp chats --unread
```

Unread state comes from WhatsApp Desktop's per-chat unread counter. Message
rows do not expose a reliable incoming per-message "read by me" flag.

### `unread`

List only chats with unread messages:

```bash
trawl whatsapp unread
trawl whatsapp unread --limit 100
```

### `messages`

List archived messages:

```bash
trawl whatsapp messages
trawl whatsapp messages --chat 1234567890@s.whatsapp.net
trawl whatsapp messages --after 2026-01-01 --from-them
trawl whatsapp messages --has-media --json
```

Filters:

```text
--chat JID       Restrict to one chat.
--sender JID     Restrict to one sender.
--limit N        Max rows. Default: 50.
--after DATE     RFC3339 timestamp or YYYY-MM-DD.
--before DATE    RFC3339 timestamp or YYYY-MM-DD.
--from-me        Only outgoing messages.
--from-them      Only incoming messages.
--has-media      Only messages with media metadata.
--asc            Oldest first.
```

### `search`

Search the archive with SQLite FTS5:

```bash
trawl whatsapp search "launch"
trawl whatsapp search "invoice" --after 2026-01-01 --who "Alice Example"
trawl --json wacrawl search "restaurant"
```

Search uses message text, chat name, sender name, and media title fields. It
accepts `--limit`, `--after`, `--before`, and `--who`.

## Sync Behavior

`wacrawl` syncs only when you ask it to sync. Read commands such as `status`,
`chats`, `messages`, and `search` inspect the existing archive without touching
the WhatsApp Desktop source or changing local state.

Examples:

```bash
trawl whatsapp sync
trawl whatsapp status
trawl --json wacrawl messages --limit 10
```

## Global Flags

```text
--json             Emit JSON instead of human-readable output.
-v, --verbose      Stream log lines to stderr.
-vv                Stream debug log lines to stderr.
--version          Print the CLI version.
```

## Data Format Notes

WhatsApp Desktop uses CoreData-style SQLite tables. The importer currently knows
about:

```text
ZWACHATSESSION
ZWAMESSAGE
ZWAMEDIAITEM
ZWAGROUPINFO
ZWAGROUPMEMBER
```

Important details:

- WhatsApp timestamps are seconds since `2001-01-01T00:00:00Z`.
- `ZWAMESSAGE.Z_PK` is used as the source row identity.
- `ZSTANZAID` is not unique enough for archive identity.
- Group senders are resolved through `ZWAMESSAGE.ZGROUPMEMBER`.
- Media is joined through both `ZWAMESSAGE.ZMEDIAITEM` and
  `ZWAMEDIAITEM.ZMESSAGE`.
- WhatsApp's own search database uses a custom `wa_tokenizer`; `wacrawl` builds
  a portable FTS5 index instead.

## Development

Requires Go 1.26 or newer.

```bash
go test ./...
```

Regenerate sqlc wrappers after changing `internal/store/sqlc/`:

```bash
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
```

## License

MIT. See `LICENSE`.
