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
grouping link can be changed without deleting the source records, and existing
user annotations survive later updates.

## Update

The OpenTrawl update command reads Apple Contacts and creates or updates
the People archive:

```sh
trawl update contacts
```

Later source snapshots replace only that source's values. Values from other
sources and existing user annotations remain intact.

## Commands

```sh
trawl contacts people --query QUERY
trawl contacts person QUERY
trawl who NAME
```

The CLI uses normal text output for people and agents. OpenTrawl never writes
back to Apple Contacts or another address book.
