---
written_by: ai
---

# Control contract v1

The contract every crawler implements. This is the plugin API: a
crawler is any binary, in any language, that speaks these commands. The
`trawl` CLI and the Mac app couple to nothing else.

The Go types live in crawlkit's `control` package (manifest, status
envelope, contact export are already there); this document is the
authority for what v1 adds. Changes go upstream to crawlkit.

All examples use synthetic data.

## Command grammar

```
<binary> <verb> [arguments] [flags]
```

Verbs first, flags last, `--json` available everywhere. (This fixes
today's drift where some crawlers use `--json metadata` and others
`metadata --json`.) Human text is the default output; it is a
first-class surface held to the same rules as JSON.

## Required commands

| command | purpose |
|---|---|
| `metadata --json` | identity, capabilities, contract version |
| `status --json` | archive state, freshness, declared counts |
| `sync --json` | crawl the source into the archive |
| `search <query> --json` | bounded FTS over the archive |
| `open <ref> --json` | one item, full detail |
| `doctor --json` | diagnostics with remedies |

Optional capability: `contacts export --json` (crawlkit
`ContactExport` shape). Declare it in metadata; v1 crawlers in this
repo all implement it.

## metadata

The manifest from crawlkit `control.Manifest`, plus:

```json
{
  "schema_version": 1,
  "contract_version": 1,
  "id": "examplecrawl",
  "display_name": "Example",
  "version": "0.3.0",
  "capabilities": ["status", "sync", "search", "open", "doctor", "contacts_export"]
}
```

## status

The crawlkit `control.Status` envelope. Counts are the crawler's
self-declared headline metrics, in display order — the app and
`trawl status` render them verbatim:

```json
{
  "app_id": "examplecrawl",
  "state": "ok",
  "summary": "archive fresh",
  "freshness": {"last_sync": "2026-07-02T14:03:11+02:00"},
  "counts": [
    {"id": "messages", "label": "messages", "value": 12345},
    {"id": "chats", "label": "chats", "value": 87},
    {"id": "since", "label": "since", "value": 2014}
  ],
  "auth": {"authorized": true, "expires": null}
}
```

`state` is one of `ok`, `stale`, `empty`, `error`, `missing`.

## search

```json
{
  "query": "boat trip",
  "results": [
    {
      "ref": "examplecrawl:msg/8842",
      "time": "2026-05-14T09:12:00+02:00",
      "who": "Alice",
      "where": "Family chat",
      "snippet": "…the boat trip is on Saturday…"
    }
  ],
  "total_matches": 332,
  "truncated": true
}
```

`--limit` defaults to 20, hard maximum 200. `--after`/`--before`
narrow by time.

v1.1 adds `--who <identity>` for crawlers that declare the `who`
capability: filter matches to items where the identity is a
participant — sender, recipient or chat member — by case-insensitive
full-name match against stored display names. It matches any sender
identity, organisations included, and applies no fuzziness: fuzzy
resolution happens before the filter, in `trawl who`. When the given
name matches more than one distinct participant, the envelope carries
`who_matched` listing them, so callers can surface the ambiguity.
Crawlers that build alias indexes declare `short_refs` (see
[short-refs.md](short-refs.md)).

A snippet is a plain text fragment: single line, whitespace
collapsed, no highlight or match markers of any kind. The full item
is what `open` is for.

## open

Takes a ref this crawler issued; returns the full item with its
natural context (a message inside its thread window, a mail with its
headers, an event with attendees). Bounded like everything else: long
threads window around the target, never dump.

## doctor

```json
{
  "checks": [
    {"id": "source_store", "state": "ok"},
    {"id": "tcc_full_disk_access", "state": "fail",
     "message": "cannot read the source database",
     "remedy": "grant Full Disk Access to Trawl in System Settings > Privacy"}
  ]
}
```

Every failing check names the exact remedy. doctor diagnoses only
what genuinely needs the world to change: permissions, expired auth,
a costly sync, a missing source store. A check whose remedy would be
safe, idempotent and local is a design bug — the crawler should have
healed that state during normal operation.

## Output rules

These apply to every command, both output modes:

- Bounded. Nothing unbounded, ever. Defaults small, maximums hard,
  truncation explicit with a hint for narrowing.
- Human shaped, down to the field. RFC 3339 timestamps in local time,
  never raw epochs. Display names alongside stable refs, never bare
  row IDs. Field names say what the value means. If a human cannot
  read a field cold, it does not ship.
- Secrets never. Auth state is booleans and expiry dates. No token,
  cookie, key or fragment thereof in any output, including errors and
  logs.
- Errors are structured and actionable: exit 1,
  `{"error": {"code", "message", "remedy"}}` on stdout in JSON mode,
  one sentence plus remedy on stderr otherwise.
- Progress (sync) is JSONL events in JSON mode
  (`{"event": "progress", "stage": "messages", "done": 120, "total": 900}`),
  human progress on stderr otherwise.
- Reads never change content. `search`, `open`, `status`, `metadata`
  never trigger a sync, never auto-import, never touch messages,
  contacts or events. They may refresh derived caches (indexes) at
  the point of use, logging one line when they do — derived state
  self-heals; there are no repair verbs.

## Conformance

The conformance harness (this repo, phase 1) verifies a binary against
this document: command grammar, JSON shapes, bounds, secret patterns,
read-only reads, and behaviour on empty and corrupt archives. A crawler
that passes is in the suite; one that does not is not. The harness is
the contract's teeth — prose drifts, tests do not.
