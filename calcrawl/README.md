---
written_by: ai
---

# calcrawl

`calcrawl` is a local-first Apple Calendar crawler. It snapshots the local
Calendar.app SQLite store, imports events into a private SQLite archive, and
serves the OpenTrawl control contract for status, sync, search, open, doctor
and contacts export.

It does not use Google APIs, CalDAV, EventKit or helper CLIs. It does not shell
out. It does not use the network.

## What it reads

`calcrawl sync` reads:

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
~/.calcrawl/calcrawl.db
```

It stores calendars, account/store provenance, events, start and end times,
all-day dates, summaries, descriptions, locations, organisers, attendees, RSVP
status, URLs and the recurrence flag.

The search index covers event summaries, descriptions, location title/address
and participant names/emails.

## Commands

```bash
calcrawl doctor
calcrawl sync
calcrawl status
calcrawl search "planning"
calcrawl open calcrawl:event/11111111-1111-1111-1111-111111111111
calcrawl contacts export
```

Add `--json` to any command for machine output. Flags work before or after the
search query.

### metadata

```json
{
  "schema_version": "crawlkit.control.v1",
  "contract_version": 1,
  "id": "calcrawl",
  "display_name": "Calendar Crawl",
  "capabilities": ["metadata", "status", "sync", "search", "open", "doctor", "contacts_export"]
}
```

### status

```json
{
  "app_id": "calcrawl",
  "state": "ok",
  "summary": "Archive is fresh.",
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

JSON sync output is JSONL:

```jsonl
{"event":"progress","stage":"source","done":1200,"total":1200}
{"event":"complete","state":"ok","calendars":12,"events":1200,"new_events":4,"changed_events":2,"unchanged_events":1194,"deleted_events":0}
```

Sync is idempotent. Re-running it updates changed events by event UUID and
reports how many events were new, changed and unchanged.

### search

Search returns 20 rows by default and never more than 200:

```json
{
  "query": "planning",
  "results": [
    {
      "ref": "calcrawl:event/11111111-1111-1111-1111-111111111111",
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

### open

`open` takes a ref returned by `search` and returns one bounded event object:

```json
{
  "ref": "calcrawl:event/11111111-1111-1111-1111-111111111111",
  "uuid": "11111111-1111-1111-1111-111111111111",
  "title": "Planning meeting",
  "start": "2026-03-04T10:00:00+01:00",
  "end": "2026-03-04T10:30:00+01:00",
  "calendar": {"id": "10", "title": "Work", "type": 1, "external_id": "work-calendar"},
  "account": {"name": "iCloud", "type": 1},
  "location": {"title": "Room 1", "address": "1 Example Street"},
  "attendees": [{"display_name": "Alice Example", "email": "alice@example.com", "rsvp_status": "accepted"}],
  "url": "https://example.com/event",
  "has_recurrences": true
}
```

All-day events render start and end as dates, for example `2026-05-05`.

### doctor

`doctor` checks:

- source store readable
- archive present
- archive schema current

If the Calendar store cannot be read, the remedy is to grant Full Disk Access to
your terminal or Trawl in System Settings > Privacy and Security > Full Disk
Access.

### contacts export

`contacts export` returns the crawlkit contact-export shape. The current
contract exports only identities with phone numbers:

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

Read commands open the archive read-only. If the archive is missing, they return
the missing state or a sync remedy and do not create files.

Tests and public examples use synthetic data only.

## v0 gaps

- Recurrence rules are not expanded or explained. The archive stores the event
  rows Calendar.app exposes and the `has_recurrences` flag.
- There is no curation layer. Birthdays, subscribed holidays and other noisy
  calendars are archived with the rest of the source.
- Contact export is limited by the current crawlkit shape, so attendee emails
  without phone numbers are searchable but not exported as contacts.

