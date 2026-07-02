---
written_by: ai
---

# gogcrawl

`gogcrawl` archives Gmail locally through the authenticated `gog` CLI.
It does not call Google APIs itself.

The archive lives at `~/.gogcrawl/gogcrawl.db` by default. It stores
Gmail message IDs, thread IDs, message time, sender name and address,
subject, labels and the plain-text body returned by:

```sh
gog gmail messages search "-in:chats" --json --max 100 --include-body
```

The query keeps the archive broad while excluding Gmail chat records.
Sync starts at the newest page, writes one SQLite transaction per page,
and stops at the first already-archived page after a completed sync. If
a previous sync did not complete, the next run crawls through instead of
using that early stop.

## Commands

```sh
gogcrawl metadata --json
gogcrawl status --json
gogcrawl sync --json
gogcrawl search "project sync" --json
gogcrawl open gogcrawl:msg/<gmail-message-id> --json
gogcrawl doctor --json
gogcrawl contacts export --json
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

## v0 gap

`gog gmail messages search --include-body` does not return a `to`
header. `gogcrawl` stores `to_address` as an empty string in v0 rather
than calling `gog gmail get` for every message during sync. `open` is an
archive-only read and does not refresh headers from `gog`.
