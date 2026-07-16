---
written_by: ai
---

# Gmail

The Gmail crawler imports an authenticated `gog` backup into a local searchable
archive. It delegates Gmail access, resume and checkpointing to `gog`; it does
not call Google APIs directly.

## Requirements and storage

- `gog` 0.31 or newer
- a valid `gog` Gmail login

The SQLite archive is `~/.opentrawl/gmail/gmail.db`. The encrypted backup is
`~/.opentrawl/gmail/backup` and must not have a Git remote. Sync invokes:

```sh
gog backup gmail push --no-push --gmail-cache --repo ~/.opentrawl/gmail/backup
```

The archive stores message and thread IDs, time, sender, recipients, subject,
labels, plain-text body and attachment metadata. It does not store attachment
bytes.

## Commands

```sh
trawl sync gmail
trawl gmail status
trawl gmail search "project sync"
trawl gmail open gmail:msg/GMAIL_MESSAGE_ID
```

Add `--json` for structured output. During sync, Gmail contributes people from
`gog contacts list` to the shared People index.

## Privacy

Mail, contact data and the backup remain local. Tokens, authentication rows and
other credential material never appear in output. Public tests use synthetic
addresses and phone numbers.
