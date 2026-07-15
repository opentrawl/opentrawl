---
written_by: ai
---

# Crawler control contract

Every registered crawler exposes a small, manifest-driven control surface to
`trawl` and the Mac app. Clients depend on the declared contract, not on source
database schemas or internal crawler packages.

The executable schemas live in `trawlkit/control` and the protobuf packages
under `trawlkit/proto/trawl`. This document explains their public meanings.
Examples use synthetic data.

## Command shape

The public source namespace is:

```text
trawl <source> <verb> [arguments] [flags]
```

Human text is the default. `--json` requests the structured equivalent. Source
names are stable product names such as `imessage`, `gmail` and `photos`; legacy
binary names are not part of the public interface.

The common control vocabulary is:

| Verb | Meaning | Changes archive content |
| --- | --- | --- |
| `metadata` | identity, capabilities and command manifest | no |
| `status` | archive state, freshness and declared counts | no |
| `sync` | refresh the archive from its source, when declared | yes |
| `search` | bounded search over the archive | no |
| `open` | one source-owned record with bounded context | no |
| `doctor` | diagnostics for conditions that need action | no |

The source manifest is authoritative about which capabilities exist. Most
archive crawlers declare `sync`; a source may instead expose reviewed imports or
source-specific refresh commands. Sources may also declare capabilities such as
`who`, short refs, contact export or source-specific list commands. Clients
discover these from the manifest rather than assuming them.

## Manifest

Metadata identifies the source, its display name, public aliases, capabilities,
headlines and commands. Each command declaration includes the argument vector,
flags, output mode and whether it mutates archive content.

```json
{
  "schema_version": 2,
  "contract_version": 1,
  "id": "example",
  "display_name": "Example",
  "capabilities": ["status", "sync", "search", "open", "doctor"],
  "commands": {
    "search": {
      "argv": ["example", "search", "QUERY", "--json"],
      "json": true,
      "mutates": false
    }
  }
}
```

## Status and diagnostics

Status reports one of `ok`, `stale`, `empty`, `error` or `missing`, with a
short summary, freshness, source-declared counts and setup requirements. Auth
is represented by state and expiry, never credentials.

```json
{
  "app_id": "example",
  "state": "ok",
  "summary": "Archive is fresh.",
  "freshness": {"last_sync": "2026-07-02T14:03:11+02:00"},
  "counts": [
    {"id": "messages", "label": "messages", "value": 12345}
  ]
}
```

`doctor` checks only conditions that require action outside an ordinary read:
for example, a missing source store, expired authentication or a macOS privacy
permission. Every failed check includes a plain-language remedy.

## Search matches

Search returns matches, not generic records. Each hit contains:

- the source and canonical ref to open;
- a stable anchor within that record;
- source-owned summary and archive context;
- labelled typed evidence explaining what matched; and
- a timestamp when the source has one.

```json
{
  "query": "boat trip",
  "results": [
    {
      "ref": "example:msg/8842",
      "time": "2026-05-14T09:12:00+02:00",
      "anchor_id": "message",
      "summary": {"title": "Family chat", "subtitle": "Alice Example"},
      "archive_context": [{"kind": "messages", "label": "In Family chat"}],
      "evidence": [
        {
          "label": "Message from Alice Example",
          "text": {"runs": [{"text": "the boat trip is on Saturday", "matched": true}]}
        }
      ]
    }
  ],
  "total_matches": 1,
  "truncated": false
}
```

Evidence is one of text, a structured field, bounded local media or a source
relation. It is exact source provenance, not a fabricated explanation of a
ranking score.

Search defaults to 20 results. If a command accepts `--limit N`, it returns N
items when the source can provide them. An external limit must be reported with
the number requested, the number returned and the reason. `--after` and
`--before` use RFC 3339 timestamps or dates.

Sources that declare `who` resolve a name or exact identifier before filtering.
One match yields the exact identifiers used. Several matches return an
`ambiguous_who` error with candidates; no match returns `unknown_who` with
suggestions. A close spelling is a suggestion, never an automatic choice.

## Refs and open

Canonical refs have the form `source:kind/id`. They remain the machine-facing
identity across syncs. Sources that declare short refs may also show a stable
human-typable alias. `trawl open` accepts a canonical ref or resolves a short
ref across capable sources without guessing.

Once assigned, a short ref is not moved or deleted. If a new ref collides with
an existing alias, only the new alias grows beyond the collision; existing
aliases do not change or shrink.

Open returns two views of the same source record:

- a typed source-owned value for machine consumers; and
- a required bounded presentation document for people and generic clients.

The presentation grammar supports prose, fields, timelines, tables, local
media, attachments, actions and notices. An opened conversation or history is
centred on the requested anchor and states when more context exists.

## Output and failure rules

- Output is bounded and says when it is incomplete.
- Human text and structured output carry the same meaning.
- Timestamps are RFC 3339 in structured output. Human output uses readable
  local time.
- Secrets, tokens, cookies and credential fragments never appear in output,
  errors or logs.
- Structured failures include a code, message and remedy. Partial federation
  keeps successful source results and names failed or skipped sources.
- Read commands never sync or change source content. They may rebuild a safe
  derived index at the point of use.
- Public `trawl` commands do not prompt or read interactive input. Setup and
  network effects use explicit commands or configuration.
- Sync progress uses structured events in JSON mode and stderr in human mode.
- `-v` streams ordinary diagnostics to stderr; `-vv` adds debug detail.

Process exit codes are stable: `0` means success, `1` failure, `2` invalid
usage and `3` a partial result in which some sources failed. Person resolution
uses `4` for `ambiguous_who` and `5` for `unknown_who`.

Conformance tests verify grammar, shapes, bounds, privacy tripwires, read-only
behaviour and empty or corrupt archive handling. Source tests remain the proof
for source-specific meanings.
