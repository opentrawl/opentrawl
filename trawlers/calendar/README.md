---
written_by: ai
---

# calcrawl

`calcrawl` is a local-first Apple Calendar crawler. It snapshots the local
Calendar.app SQLite store, imports events into a private SQLite archive, and
serves the OpenTrawl control contract for status, sync, search, open, doctor,
contacts export, `--who` filtering and short refs.

It does not use Google APIs, CalDAV, EventKit or helper CLIs. It does not shell
out. It does not use the network.

## What it reads

`trawl calendar sync` reads:

```text
~/Library/Group Containers/group.com.apple.calendar/Calendar.sqlitedb
```

It copies that database and any `-wal` or `-shm` siblings to a private temporary
directory, opens the copy read-only, imports from the copy, then deletes the
temporary snapshot.

Calendar.app already stores every synced calendar there, including iCloud,
Google, local and subscribed calendars. v1 archives all event calendars except
the Reminders store. There is no include or exclude configuration.

## What it stores

The archive lives at:

```text
~/.opentrawl/calendar/calendar.db
```

Logs live under:

```text
~/.opentrawl/calendar/logs/calendar.log
```

The archive stores calendars, account/store provenance, events, start and end
times, all-day midnight timestamps, summaries, descriptions, locations,
organisers, attendees, RSVP status, URLs and the recurrence flag.

The search index covers event summaries, descriptions, location title/address
and participant names/emails.

The short-ref index is derived from canonical event refs and can be rebuilt at
any time.

## Commands

```bash
trawl calendar doctor
trawl calendar sync
trawl calendar status
trawl calendar search "planning"
trawl calendar search "planning" --who "Alice Example"
trawl calendar search --who alice@example.com
trawl calendar who alice
trawl calendar open t7k3f
trawl calendar open calendar:event/11111111-1111-1111-1111-111111111111
trawl calendar contacts export
```

Add `--json` to any command for machine output. Flags work before or after the
search query.

### metadata

```json
{
  "schema_version": "crawlkit.control.v1",
  "contract_version": 1,
  "id": "calendar",
  "display_name": "Calendar",
  "capabilities": ["metadata", "status", "sync", "search", "open", "doctor", "contacts_export", "who", "short_refs"]
}
```

### status

```json
{
  "app_id": "calendar",
  "state": "ok",
  "summary": "Archive is fresh.",
  "freshness": {"last_sync": "2026-07-02T14:03:11+02:00"},
  "counts": [
    {"id": "events", "label": "events", "value": 1200},
    {"id": "calendars", "label": "calendars", "value": 12},
    {"id": "since", "label": "since", "value": 2018}
  ]
}
```

`state` is `missing` before the first sync, `empty` when the archive has no
events, `stale` when the source database changed or the last sync is older than
one day, and `ok` when the archive is current.

### sync

JSON sync output reports generic change counts:

```json
{"added":4,"updated":2,"removed":0}
```

Sync is idempotent. Re-running it updates changed events by event UUID and
reports how many events were added, updated and removed.

### search

Search returns 20 rows by default. Use `--limit N` to choose a different count:

```json
{
  "query": "planning",
  "results": [
    {
      "ref": "calendar:event/11111111-1111-1111-1111-111111111111",
      "short_ref": "3qdr5",
      "time": "2026-03-04T10:00:00+01:00",
      "who": "Alice Example",
      "where": "Room 1",
      "snippet": "Planning meeting - Room 1, 1 Example Street"
    }
  ],
  "total_matches": 1,
  "truncated": false
}
```

Use `--limit`, `--after` and `--before` to narrow results.

Use `--who` to filter to events where the organiser or an attendee is that
person. Exact email addresses, phone numbers and source addresses filter
directly. Names resolve first. One resolved person adds `who_resolved`:

```json
{
  "query": "planning",
  "who_resolved": {"who": "Alice Example", "identifiers": ["alice@example.com", "+15550100"]},
  "results": [],
  "total_matches": 0,
  "truncated": false
}
```

Ambiguous names do not search. They return `ambiguous_who` with candidates.
Unknown people return `unknown_who` with `did_you_mean` candidates, or a hint to
search without `--who` when nothing is close.

The query is optional when `--who`, `--after` or `--before` is present. A
filter-only search lists the newest matching events.

Search text and JSON include short refs when the alias index is available.
Search JSON keeps `ref` as the full canonical ref.

### who

`who` resolves a name fragment against archived organisers and attendees:

```json
{
  "query": "alice",
  "candidates": [
    {
      "who": "Alice Example",
      "identifiers": ["alice@example.com", "+15550100"],
      "last_seen": "2026-03-04T10:00:00+01:00",
      "messages": 1
    }
  ]
}
```

Matching is generous over names and identifiers: case-insensitive exact, prefix,
substring and close spellings. Search resolves a single exact, prefix or
substring match. A close-spelling-only match stays `unknown_who` and appears in
`did_you_mean`. Candidates are deduped by identity, so one person with several
handles appears once.

### open

`open` takes a full ref or short alias returned by search text. It never guesses:
unknown aliases return `unknown_short_ref`, and ambiguous aliases return
`ambiguous_short_ref`.

`open` returns one bounded event object:

```json
{
  "ref": "calendar:event/11111111-1111-1111-1111-111111111111",
  "uuid": "11111111-1111-1111-1111-111111111111",
  "title": "Planning meeting",
  "start": "2026-03-04T10:00:00+01:00",
  "end": "2026-03-04T10:30:00+01:00",
  "calendar": "Work",
  "account": "iCloud",
  "location": {"title": "Room 1", "address": "1 Example Street"},
  "attendees": [{"display_name": "Alice Example", "email": "alice@example.com", "rsvp_status": "accepted"}],
  "status": "confirmed",
  "url": "https://example.com/event",
  "has_recurrences": true
}
```

All-day events render start and end as RFC 3339 local-midnight timestamps, for
example `2026-05-05T00:00:00+02:00`.

### doctor

`doctor` checks:

- source store readable
- archive present
- archive schema current

Text output includes the last logged run and the most recent logged error when
the log exists.

If the Calendar store cannot be read, the remedy is to grant Full Disk Access to
your terminal or Trawl in System Settings > Privacy and Security > Full Disk
Access.

### contacts export

`contacts export` returns the crawlkit contact-export shape. The current
contact-export contract requires at least one phone number on each exported
contact:

```json
{
  "contacts": [
    {"display_name": "Alice Example", "phone_numbers": ["+15550100"]}
  ]
}
```

## Privacy

All reads and writes are local. `calcrawl` does not send calendar content,
metadata, contacts, paths or counts to any service.

Read commands do not sync or change source content. If the archive is missing,
they return the missing state or a sync remedy and do not create the archive.

Tests and public examples use synthetic data only.

## v0 gaps

- Recurrence rules are not expanded or explained. The archive stores the event
  rows Calendar.app exposes and the `has_recurrences` flag.
- There is no curation layer. Birthdays, subscribed holidays and other noisy
  calendars are archived with the rest of the source.
- Contact export is limited by the current crawlkit shape, so attendee emails
  without phone numbers are searchable but not exported as contacts.
