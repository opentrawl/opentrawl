---
written_by: ai
---

# Notes

The Notes crawler snapshots Apple Notes and builds a local SQLite archive of
notes, folders, attachments and recoverable versions. It reads Notes without
changing the source database or iCloud state.

## Source and storage

The default source is:

```text
~/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite
```

The archive is `~/.opentrawl/notes/notes.db`. Update snapshots the source database
and its SQLite sidecars before decoding content. `import-store` can import one
copied or mounted `NoteStore.sqlite` explicitly.

## Commands

```sh
trawl update notes
trawl notes import-store /path/to/NoteStore.sqlite --label copied-store
trawl notes status
trawl notes list --limit 20
trawl notes list "Work"
trawl notes search "project plan"
trawl notes open LINK
trawl notes versions LINK
trawl notes at-time LINK --time 2026-01-01T12:00:00Z
```

The CLI uses normal text output. List and search results are bounded. Human
output uses stable links for follow-up commands. Canonical provider record
references remain internal typed and storage values.

Recovered versions are source evidence, not edits made by OpenTrawl. A missing
or unreadable WAL is reported honestly rather than silently treated as a
complete history.

## Privacy

Notes, attachment paths and recovered text are private. Public examples and
tests use synthetic notes and temporary SQLite files.
