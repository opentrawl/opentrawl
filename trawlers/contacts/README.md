---
written_by: ai
---

# Contacts

Contacts is OpenTrawl's People index. It stores people and identifiers in its
own local archive:

```text
~/.opentrawl/contacts/contacts.db
```

The SQLite archive groups source identities from Apple Contacts and messaging
archives without flattening their original records. Strong identifiers such as
phone numbers, email addresses and source accounts connect identities. The
grouping link can be changed without deleting the source records, and user
annotations survive later syncs.

## Sync

Normal OpenTrawl sync reads Apple Contacts automatically and creates or updates
the People archive:

```sh
trawl sync contacts
```

Later source snapshots replace only that source's values. Values from other
sources and user annotations remain intact.

## Import

Explicit imports remain available for files and older archives:

```sh
trawl contacts import apple --input contacts.ndjson
trawl contacts import google --account ada@example.com
trawl contacts import birdclaw --db /path/to/birdclaw.sqlite
trawl contacts import discrawl --db /path/to/discrawl.sqlite
```

For an existing Git-backed share, `import-legacy` performs a read-only,
idempotent import. It does not run Git or write beside the old share:

```sh
trawl contacts import-legacy --from /path/to/share
```

## Commands

```sh
trawl contacts status
trawl contacts search Ada
trawl contacts who Ada
trawl contacts person list
trawl contacts person show ada@example.com
trawl contacts person annotate person_123 "Ada is the project accountant"
```

Use the normal text output for people and agents. Add `--json` only for scripts.
OpenTrawl never writes back to Apple Contacts or another address book.

The archive contains private contact and annotation data. Public fixtures use
invented people, `example.com` addresses and `+1555` phone numbers.
