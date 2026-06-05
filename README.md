---
written_by: ai
---

# imsgcrawl

`imsgcrawl` is a local-first iMessage source crawler. It reads the local
Messages SQLite database through a temporary read-only snapshot, syncs a small
source-native archive, and exposes crawlkit-style metadata, status, read, search,
and contact-export commands.

## Commands

```bash
imsgcrawl --json metadata
imsgcrawl --json status
imsgcrawl --json sync
imsgcrawl --json chats --limit 20
imsgcrawl --json messages --chat 123 --limit 50
imsgcrawl --json search --limit 20 "launch notes"
imsgcrawl --json contacts export
```

`metadata` prints the crawlkit control manifest. `status` reports aggregate
readability and row counts without leaking handles. `sync` creates or refreshes
the local archive at `~/.imsgcrawl/archive.db`. `chats`, `messages`, and
`search` read from that archive.

`contacts export` prints the shared v0 contact-export shape:

```json
{
  "contacts": [
    {
      "display_name": "0118 999 881 999 119 725 3",
      "phone_numbers": ["0118 999 881 999 119 725 3"]
    }
  ]
}
```

The v0 contact contract is intentionally narrow: root key `contacts`, with only
`display_name` and `phone_numbers` on each contact. When Messages has no human
name, the current exporter uses the phone number as `display_name`; downstream
importers should treat that as an unnamed phone-only contact rather than a
canonical human name.

## Privacy

Messages data contains private names, phone numbers, emails, and conversation
contents. Do not publish raw output from a real Messages database. Tests and
public examples must use fake fixture data.
