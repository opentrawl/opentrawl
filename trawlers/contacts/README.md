---
written_by: ai
---

# Contacts

Contacts stores people in the standard OpenTrawl archive:

```text
~/.opentrawl/contacts/contacts.db
```

The archive is local SQLite. It stores people, email addresses, phone
numbers, addresses, source handles, notes, user annotations, search indexes
and short refs.

## Import

Import a reviewed source into the archive:

```bash
trawl contacts import apple --input contacts.ndjson
trawl contacts import google --account ada@example.com
trawl contacts import birdclaw --db /path/to/birdclaw.sqlite
trawl contacts import discrawl --db /path/to/discrawl.sqlite
```

The old cross-crawler `import contacts` path is still retired. Import one
source at a time.

## Legacy cutover

Import the old Git-backed share directory once:

```bash
trawl contacts import-legacy --from /path/to/share
```

Without `--from`, contacts reads the legacy `repo_path` from
`~/.opentrawl/contacts/config.toml`; if that key is absent, it tries
`~/.opentrawl/contacts/share`.

The importer reads the legacy directory only. It does not run `git`, write
files beside the legacy share, or rebuild the old derived index. Rerunning it
upserts the same people and notes.

## Use

```bash
trawl contacts status
trawl contacts search Ada
trawl contacts who Ada
trawl contacts person list
trawl contacts person show ada@example.com
trawl contacts person annotate person_123 "Ada is the project accountant"
trawl contacts contacts export
```

`trawl contacts sync apple` and `trawl contacts sync google` remain preview
commands. Remote address-book writes need a conflict report before they become
active.
