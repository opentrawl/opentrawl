---
written_by: ai
---

# Calendar

The Calendar crawler snapshots the local Apple Calendar database and builds a
private SQLite archive of events, calendars, participants and source
provenance. It does not use EventKit, CalDAV, Google APIs or the network.

## Source and storage

The source is:

```text
~/Library/Group Containers/group.com.apple.calendar/Calendar.sqlitedb
```

Sync copies the database and its SQLite sidecars to a temporary private
directory before reading them. The archive is
`~/.opentrawl/calendar/calendar.db`; logs are under
`~/.opentrawl/calendar/logs/`.

The crawler archives event calendars visible to Calendar.app, including iCloud,
Google, local and subscribed calendars. It excludes the Reminders store.

## Commands

```sh
trawl calendar doctor
trawl calendar sync
trawl calendar status
trawl calendar search "planning" --who "Alice Example"
trawl calendar search --who alice@example.com
trawl calendar who alice
trawl calendar open calendar:event/11111111-1111-1111-1111-111111111111
trawl calendar contacts export
```

Add `--json` for structured output. Search covers event titles, descriptions,
locations and participant names or addresses. It accepts `--limit`, `--after`,
`--before` and `--who`; a filter-only search lists the newest matching events.

Human search output may show a short ref. `open` accepts the short or canonical
ref without guessing and returns one bounded event with people, time, location,
calendar and recurrence state.

Contact export follows the shared narrow shape and therefore includes only
people with a display name and phone number. Participant email addresses remain
searchable even when they cannot be exported.

## Limits and privacy

Recurrence rules are not expanded into a separate series explanation. The
archive stores the event rows and recurrence flag exposed by Calendar.

All reads and writes are local. Read commands do not refresh the archive or
change Calendar. Public examples and tests use synthetic events and temporary
databases.
