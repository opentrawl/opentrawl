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
and its SQLite sidecars before decoding content.

## Commands

```sh
trawl update notes
trawl notes notes --limit 20
trawl notes notes "Work"
trawl search "project plan" --trawler notes
trawl open LINK
trawl notes versions LINK --limit 20
trawl notes versions LINK --at 2026-01-01T12:00:00Z
```

The CLI uses normal text output. List and search results are bounded. Human
output uses stable links for follow-up commands. Canonical provider record
references remain internal typed and storage values.

Recovered versions are source evidence, not edits made by OpenTrawl. A missing
or unreadable WAL is reported honestly rather than silently treated as a
complete history.

## Privacy

Notes, attachment paths and recovered text are private. Keep them out of
public examples and tests.
