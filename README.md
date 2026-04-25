# wacrawl

`wacrawl` is a read-only command line archive for the macOS WhatsApp Desktop app.

It snapshots WhatsApp's local SQLite databases, imports messages into a separate
SQLite archive, and gives you fast local listing and search without mutating
WhatsApp's app container.

## Why

WhatsApp Desktop keeps useful local state, but the data lives in CoreData-shaped
SQLite files with Apple-epoch timestamps, group sender indirection, media joins,
and an app-owned FTS database that depends on WhatsApp's custom tokenizer.

`wacrawl` turns that into a stable archive you can query from scripts.

## Safety Model

- Read-only against WhatsApp data.
- Copies SQLite database triads to a temp snapshot before reading.
- Writes only to `~/.wacrawl/wacrawl.db` by default.
- Does not use WhatsApp private APIs.
- Does not decrypt, send, upload, or modify messages.
- Does not currently write back to WhatsApp.

## Requirements

- macOS with WhatsApp Desktop installed.
- Go 1.26 or newer.
- Local filesystem access to WhatsApp's group container.

Default WhatsApp source:

```text
~/Library/Group Containers/group.net.whatsapp.WhatsApp.shared
```

Default archive:

```text
~/.wacrawl/wacrawl.db
```

## Install

From source:

```bash
go install github.com/steipete/wacrawl/cmd/wacrawl@latest
```

From this checkout:

```bash
make build
./bin/wacrawl help
```

## Quick Start

Inspect the WhatsApp source:

```bash
wacrawl doctor
```

Import a fresh archive snapshot:

```bash
wacrawl import
```

Check archive counts:

```bash
wacrawl status
```

List recent chats:

```bash
wacrawl chats --limit 20
```

List recent messages:

```bash
wacrawl messages --limit 20
```

Search message text:

```bash
wacrawl search "clawcon"
```

## Global Flags

```text
--db PATH       Archive database path. Default: ~/.wacrawl/wacrawl.db
--source PATH   WhatsApp Desktop source path.
--json          Emit JSON instead of human-readable output.
```

Examples:

```bash
wacrawl --json status
wacrawl --db /tmp/wa.db import
wacrawl --source "$HOME/Library/Group Containers/group.net.whatsapp.WhatsApp.shared" doctor
```

## Commands

### `doctor`

Reports whether the WhatsApp Desktop source is available, which database files
exist, row counts, message date range, and schema notes.

```bash
wacrawl --json doctor
```

### `import`

Creates a temporary copy of WhatsApp's SQLite files, extracts chats, contacts,
groups, participants, messages, and media references, then replaces the archive
contents in one transaction.

```bash
wacrawl import
```

### `status`

Shows archive counts, oldest/newest message timestamps, last import time, and
the source used for the last import.

```bash
wacrawl status
```

### `chats`

Lists chats ordered by newest message.

```bash
wacrawl chats --limit 100
```

### `messages`

Lists archived messages. Filters:

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

Examples:

```bash
wacrawl messages --chat 1234567890@s.whatsapp.net --limit 25
wacrawl messages --after 2026-01-01 --from-them
wacrawl messages --has-media --json
```

### `search`

Runs FTS5 search over the archive's message text, chat name, sender name, and
media title fields. It accepts the same message filters as `messages`.

```bash
wacrawl search "release notes" --after 2026-01-01
wacrawl search "invoice" --from-them --json
```

## WhatsApp Data Shape

Current macOS WhatsApp Desktop data lives under:

```text
~/Library/Group Containers/group.net.whatsapp.WhatsApp.shared
```

Important files:

```text
ChatStorage.sqlite
ContactsV2.sqlite
fts/ChatSearchV5f.sqlite
Message/Media/
```

Important CoreData tables:

```text
ZWACHATSESSION
ZWAMESSAGE
ZWAMEDIAITEM
ZWAGROUPINFO
ZWAGROUPMEMBER
```

Notes from the current importer:

- WhatsApp timestamps are seconds since `2001-01-01T00:00:00Z`.
- `ZWAMESSAGE.Z_PK` is used as the stable source row identity.
- `ZSTANZAID` is not globally unique enough for archive identity.
- Group senders are resolved through `ZWAMESSAGE.ZGROUPMEMBER`.
- Media joins are checked through both `ZWAMESSAGE.ZMEDIAITEM` and
  `ZWAMEDIAITEM.ZMESSAGE`.
- WhatsApp's own search DB uses a custom `wa_tokenizer`; `wacrawl` builds its
  own portable FTS5 table instead.

## Development

```bash
make check
```

Runs:

```bash
golangci-lint run ./...
./scripts/coverage.sh 85.0
go build -o bin/wacrawl ./cmd/wacrawl
```

The coverage gate fails below 85% total statement coverage.

## License

MIT. See `LICENSE`.
