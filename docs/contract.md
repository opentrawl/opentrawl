---
written_by: ai
---

# Control contract v1

The contract every crawler implements. A crawler is a Go package that
speaks these commands; the `trawl` CLI and the Mac app couple to this
contract, not to any crawler's internals. Adding a source means writing
a Go crawler that implements the contract and registering it in `trawl`,
then recompiling. A published, any-language plugin path is a later goal
(see vision.md), not the current build.

The Go types live in trawlkit's `control` package (manifest, status
envelope, contact export are already there); this document is the
authority for what v1 adds. Changes land in trawlkit.

All examples use synthetic data.

## Command grammar

```
<binary> <verb> [arguments] [flags]
```

Verbs first, flags last, `--json` available everywhere. (This fixes
today's drift where some crawlers use `--json metadata` and others
`metadata --json`.) Human text is the default output; it is a
first-class surface held to the same rules as JSON.

## Logging

Logging is part of the tool contract. Every crawler writes an
always-on plain text log in its state root, under `logs/`, using the
shared trawlkit log grammar. Normal stdout and stderr stay clean:
commands do not print log lines unless the user asks for them.

Every verb accepts `-v` and `--verbose`. `-v` streams the same log
lines to stderr while still writing the log file. `-vv` also writes
debug detail, such as subprocess argv, per-source elapsed times and
per-shard phase timings.

The runner owns this for crawlers registered through `trawlkit.Run`.
Those crawlers stream typed log lines to the parent over the runner
wire, and the parent renders them only when the user asks with `-v` or
`-vv`. Schema-v2 manifests do not declare a `verbose_logs` capability.
Logs are a runner contract property, not a per-crawler feature flag.
Schema-v1 crawlers that have not adopted `trawlkit.Run` still declare
`verbose_logs` when `trawl` should forward verbose flags and stderr.

Every help page ends with a diagnostics line naming the flag and log
file, for example:

```text
Diagnostics: run with -v, or read ~/.examplecrawl/logs/examplecrawl.log
```

The CLI QA gate checks that help text exposes this line and that
normal command output stays free of logs.

## Required commands

| command | purpose |
|---|---|
| `metadata --json` | identity, capabilities, contract version |
| `status --json` | archive state, freshness, declared counts |
| `sync --json` | crawl the source into the archive |
| `search <query> --json` | bounded FTS over the archive |
| `open <ref> --json` | one item, full detail |
| `doctor --json` | diagnostics with remedies |

Optional capability: `contacts export --json` (trawlkit
`ContactExport` shape). Declare it in metadata; v1 crawlers in this
repo all implement it.

## metadata

The manifest from trawlkit `control.Manifest`, plus:

```json
{
  "schema_version": 2,
  "contract_version": 1,
  "id": "examplecrawl",
  "display_name": "Example",
  "version": "0.3.0",
  "capabilities": ["status", "sync", "search", "open", "doctor", "contacts_export"],
  "commands": {
    "search": {
      "argv": ["examplecrawl", "search", "QUERY", "--json"],
      "json": true,
      "mutates": false,
      "flags": [{"name": "limit", "usage": "maximum results", "default": "20"}]
    }
  }
}
```

## status

The trawlkit `control.Status` envelope. Counts are the crawler's
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

`--limit N` is honored as given, on every verb that accepts it and
in both output modes: N results come back if the archive has them.
No hard maximum, no silent clamping — an agent that asks for 1000
rows must never discover a different number arrived. `--limit`
defaults to 20 on search; list verbs may choose their own default,
but the default is the only thing a verb may choose. When a real
constraint outside the crawler's control caps the answer (a remote
API page limit, say), the crawler returns what it can and says so:
human mode prints one line — `limit: returned 800 of 1000 requested
(reason)` — and JSON carries `"limit_capped": {"requested": 1000,
"returned": 800, "reason": "..."}`. Values below 1 are usage
errors. `--after`/`--before` narrow by time.

v1.2 defines `--who <person>` for crawlers that declare the `who`
capability: filter matches to one person's items — sender, recipient
or chat member. The value may be a name or an exact identifier
(email, phone, handle). A name resolves against the archive's
participants before any filtering:

- exactly one person: filter, and include a `who_resolved` object in
  the envelope (`{"who": ..., "identifiers": [...]}`); human mode
  prints one resolution line, for example `alex → Alex Jones`.
- more than one person: no search runs. Error `ambiguous_who`
  carries a `candidates` array — each with `who`, `identifiers`,
  `last_seen` and message volume — so one retry with an identifier
  cannot miss. Exit 4.
- no person: error `unknown_who` carries `did_you_mean` candidates
  from generous matching (prefix, substring, close spellings); when
  nothing is close, a hint to search without `--who`. Exit 5.

Close-spelling matches never count toward "exactly one": a name that
only matches by spelling distance (a lone Dana for `--who dan`) is
`unknown_who` with the suggestion, never an auto-resolve — explicit
beats implicit. Exact, prefix and substring matches resolve.

Resolution is generous; filtering is exact. An ambiguous name is
never filtered across all its matches (v1.1's blend-and-report
`who_matched` behaviour is removed). The same rule as short refs:
zero, one or many, never pick.

Crawlers with the `who` capability also expose the resolver
directly: `who <name>` returns the candidates as JSON
(`{"query": ..., "candidates": [{"who": ..., "identifiers": [...],
"last_seen": ..., "messages": ...}]}`) and as a plain table in human
mode. Trawl uses this surface to resolve federated queries — joining
same-named candidates across sources, upgraded by the person layer
(clawdex) where it knows the person — instead of duplicating
identity logic.

The `<query>` argument to `search` is optional when at least one
filter (`--who`, `--after`, `--before`) is present: a filter-only
search lists the newest matching items. `search` with no query and
no filters is an error.

Trawlkit owns the alias index and manifests always include
`short_refs`. Aliases are assigned once and never deleted. On
collision, new refs extend past every stored alias instead of moving
an existing alias. The accepted costs are explicit: rows for deleted
source items persist (opening one reports not found), and aliases
never re-shorten after a collision has made them longer.

That guarantee holds once every binary writing the archive runs this code.
Older binaries still clear aliases during sync (kind-scoped in the immediately
prior generation, whole-table before that) and can delete or reassign aliases.
A later sync preserves whatever state the old binary left; it does not recover
deleted aliases for refs no longer emitted.

Crawlers include the computed alias as `"short_ref"` on every result in their
own `--json` output. The full `ref` stays the canonical identity; `short_ref` is
display sugar so a federating reader (trawl) can render the short form without
inventing its own alias length. Trawl's agent-facing `--json` still keeps short
refs out by default.

A snippet is a plain text fragment: single line, whitespace
collapsed, no highlight or match markers of any kind. The full item
is what `open` is for.

A result whose `time` is a calendar date with no meaningful time of
day (an all-day calendar event) carries `"all_day": true`. The field
is optional; absent means false, and it is only meaningful alongside
`time`. `time` stays RFC 3339 as always — `all_day` tells a reader
the clock part is padding, so renderers show the bare date instead
of a fake `00:00`.

## open

Takes a ref this crawler issued; returns the full item with its
natural context (a message inside its thread window, a mail with its
headers, an event with attendees). Bounded like everything else: long
threads window around the target, never dump.

`trawl open <ref>` passes through the crawler's human rendering because
open is single-source. The crawler's human mode is authoritative for
its own items; search remains trawl-rendered so multiple sources can be
merged consistently.

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

## Contract checks

The test suite and `trawlkit/conformance` helpers verify command grammar,
JSON shapes, bounds, secret patterns, read-only reads, and behaviour on
empty and corrupt archives. These checks are the contract's tripwires —
prose drifts, tests do not. They remember known defect classes; they are
not the quality gate. Output changes additionally pass an adversarial
model review of raw transcripts (see vision.md engineering principles).
