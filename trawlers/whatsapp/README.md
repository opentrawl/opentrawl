---
written_by: ai
---

# WhatsApp

The WhatsApp crawler snapshots the macOS WhatsApp Desktop databases and imports
chats, contacts, groups, messages and media metadata into a local SQLite
archive. It does not send messages, use WhatsApp Web or write to the app
container.

## Source and storage

The default source is:

```text
~/Library/Group Containers/group.net.whatsapp.WhatsApp.shared
```

Sync reads `ChatStorage.sqlite`, `ContactsV2.sqlite` and available media through
a temporary snapshot. The archive is
`~/.opentrawl/whatsapp/whatsapp.db`.

By default, media rows point to files in the WhatsApp container. Set
`copy_media = true` in `~/.opentrawl/whatsapp/config.toml` to copy referenced
files beside the archive. Missing files are reported but do not fail the
import.

## Commands

```sh
trawl whatsapp sync
trawl whatsapp status
trawl whatsapp chats --limit 20
trawl whatsapp messages --chat CHAT_JID --after 2026-01-01
trawl whatsapp search "release notes" --who "Alice Example"
trawl whatsapp open whatsapp:msg/REF
```

Add `--json` for structured output. Message listing supports chat, sender,
date, direction and media filters. Search covers message text, chat and sender
names, and media titles.

Read commands inspect the existing archive without touching WhatsApp. Run sync
explicitly to refresh it.

## Privacy

The archive contains private conversations and media paths. Keep it out of
commits, shared logs and public backups. Tests use temporary snapshots and
synthetic data.
