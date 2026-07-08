---
written_by: ai
---

# gogcrawl

`gogcrawl` archives Gmail locally through the authenticated `gog` CLI.
It does not call Google APIs itself.

Requirements:

- `gog` 0.31 or newer
- a valid `gog` Gmail login

`gogcli` releases weekly. Check the installed version with:

```sh
gog --version
```

The archive lives at `~/.opentrawl/gmail/gmail.db` by default. `gogcrawl`
also owns a local encrypted backup repo at `~/.opentrawl/gmail/backup`.

Sync runs:

```sh
gog backup gmail push --no-push --repo ~/.opentrawl/gmail/backup
```

`gog` owns Gmail fetch, cache resume and checkpointing. `gogcrawl`
decrypts new or changed backup shards with `gog backup cat`, then writes
derived SQLite rows for search and open. The backup repo must not have a
git remote, because `gogcrawl` never pushes mail data anywhere.

The SQLite archive stores Gmail message IDs, thread IDs, precise message
time from `internalDate`, sender, `to`, `cc`, subject, label IDs, labels,
plain-text body and attachment metadata. It does not store attachment
bytes.

## Commands

```sh
trawl gmail metadata --json
trawl gmail status --json
trawl gmail sync --json
trawl gmail sync --query "from:me" --max 25 --json
trawl gmail search "project sync" --json
trawl gmail open gmail:msg/<gmail-message-id> --json
trawl gmail doctor --json
trawl gmail contacts export --json
```

`contacts export` uses `gog contacts list --json`. It exports only
contacts with a display name and at least one phone number, matching the
crawlkit contact-export contract.

`status` reports the archive as stale when the last completed sync is
more than 24 hours old.

## Privacy

All message content and contact data stays on the local machine.
`gogcrawl` never prints OAuth tokens, refresh tokens, account rows from
`gog auth list`, or any other secret material.

This repository is public. Tests and examples use synthetic data only,
such as `alice@example.com`, `bob@example.com` and `+15550101000`.
