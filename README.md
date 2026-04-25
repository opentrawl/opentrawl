# wacrawl

Read-only local archive and search for the macOS WhatsApp Desktop app.

`wacrawl` copies WhatsApp Desktop's local SQLite databases into a temporary
snapshot, imports the useful chat data into its own SQLite archive, and gives
you scriptable commands for status, chat listing, message listing, and full-text
search.

It is for local inspection. It does not send messages, decrypt backups, talk to
WhatsApp Web, or write back into WhatsApp's app container.

## Install

Homebrew is the easiest path. Install directly from my tap:

```bash
brew install steipete/tap/wacrawl
```

After that, upgrades stay simple:

```bash
brew update
brew upgrade steipete/tap/wacrawl
```

Or from source:

```bash
go install github.com/steipete/wacrawl/cmd/wacrawl@latest
```

Check the installed binary:

```bash
wacrawl --version
```

## Quick Start

First, check whether `wacrawl` can see the local WhatsApp Desktop data:

```bash
wacrawl doctor
```

Import a fresh local archive:

```bash
wacrawl import
```

Inspect what was imported:

```bash
wacrawl status
wacrawl chats --limit 20
wacrawl messages --limit 20
```

Search message text:

```bash
wacrawl search "release notes"
```

Use JSON for scripts:

```bash
wacrawl --json search "invoice" --from-them --after 2026-01-01
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
~/.wacrawl/wacrawl.db
```

Override either path when needed:

```bash
wacrawl --source "$HOME/Library/Group Containers/group.net.whatsapp.WhatsApp.shared" doctor
wacrawl --db /tmp/wacrawl.db import
```

## Safety

- Opens WhatsApp data read-only.
- Copies SQLite database, WAL, and SHM files into a temp snapshot before import.
- Replaces only the `wacrawl` archive database.
- Does not modify WhatsApp databases, settings, contacts, chats, or media.
- Does not use the WhatsApp network protocol.
- Does not upload data.

The archive can contain private message data. Keep `~/.wacrawl/wacrawl.db`
local and out of commits, backups, and shared logs unless that is intentional.

## Commands

### `doctor`

Inspect the source path and database shape:

```bash
wacrawl doctor
wacrawl --json doctor
```

Reports source availability, discovered database files, row counts, message date
range, and importer schema notes.

### `import`

Snapshot WhatsApp Desktop data and replace the local archive in one transaction:

```bash
wacrawl import
```

Imports:

- chats
- contacts
- groups
- group participants
- messages
- media metadata and local media paths

### `status`

Show archive counts and import metadata:

```bash
wacrawl status
```

Includes chat, contact, group, participant, message, media-message, oldest,
newest, last-import, and source fields.

### `chats`

List chats ordered by newest message:

```bash
wacrawl chats
wacrawl chats --limit 100
```

### `messages`

List archived messages:

```bash
wacrawl messages
wacrawl messages --chat 1234567890@s.whatsapp.net
wacrawl messages --after 2026-01-01 --from-them
wacrawl messages --has-media --json
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
wacrawl search "launch"
wacrawl search "invoice" --from-them --after 2026-01-01
wacrawl --json search "restaurant"
```

Search uses message text, chat name, sender name, and media title fields. It
accepts the same filters as `messages`.

## Global Flags

```text
--db PATH       Archive database path. Default: ~/.wacrawl/wacrawl.db
--source PATH   WhatsApp Desktop source path.
--json          Emit JSON instead of human-readable output.
--version       Print the CLI version.
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
make check
```

Runs:

```bash
golangci-lint run ./...
./scripts/coverage.sh 85.0
go build -o bin/wacrawl ./cmd/wacrawl
```

Extra release-parity checks:

```bash
go test -count=1 -race ./...
goreleaser release --snapshot --clean --skip=publish
```

Coverage must stay at or above 85%.

## Release

Releases are tag-driven through GoReleaser.

```bash
git tag -a v0.1.0 -m "Release 0.1.0"
git push origin main --tags
```

CI publishes GitHub release artifacts for:

```text
darwin/amd64
darwin/arm64
linux/amd64
linux/arm64
windows/amd64
windows/arm64
```

The Homebrew formula lives in:

```text
~/Projects/homebrew-tap/Formula/wacrawl.rb
```

## License

MIT. See `LICENSE`.
