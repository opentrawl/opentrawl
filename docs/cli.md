---
written_by: ai
---

# The trawl CLI

The single entry point. Follows [clig.dev](https://clig.dev); adding a
cross-source verb or flag is a design decision recorded in this file, not
a response to a feature request.

There are two kinds of command. The cross-source verbs below run over
every registered source at once — the one door. Each source is also its
own namespace: `trawl <source>` lists that crawler's verbs and
`trawl <source> <verb>` runs one, served from the source's manifest so
the source package is never named to the reader (see Namespaces).

All examples use synthetic data.

## Verbs

```
trawl status [source]
trawl sync [source ...]
trawl search <query> [--source a,b] [--limit n] [--after date] [--before date]
trawl open <ref>
trawl doctor [source]
trawl <source> [verb ...]
```

Global flags: `--json`, `--help`, `--version`. Nothing else.

## status

Health of every registered source at a glance. One line per source:
state, freshness, and the headline counts the crawler itself declares.

```
$ trawl status
SOURCE     STATE  FRESH    HEADLINE
imessage   ok     2m ago   12,345 messages · 87 chats · since 2014
telegram   ok     1h ago   23,456 messages · 145 chats · since 2016
whatsapp   stale  3d ago   3,456 messages · 42 chats · since 2019
gmail      error  —        auth expired — run trawl doctor gmail
```

`trawl status <source>` shows one crawler in detail: databases, counts,
last sync outcome, auth state (booleans and expiry only).

## sync

Runs crawls. No arguments syncs everything; names sync those sources.
Progress goes to stderr so stdout stays clean; the result is one
summary line per source.

```
$ trawl sync imessage telegram
imessage   ok    1,204 new messages in 14s
telegram   ok    89 new messages in 6s
```

## search

Federated search across per-source archives. Every row carries
provenance: source, timestamp, who, snippet, and a stable ref.

```
$ trawl search "boat trip" --source imessage,telegram
imessage  2026-05-14  Alice → Family      …the boat trip is on Saturday…   imessage:msg/8842
telegram  2026-05-12  Bob                 …book the boat before June…      telegram:msg/1930
```

Default limit is 20 rows; `--limit` is honored as given, with no
hidden maximum. Truncation is always explicit
(`…and 312 more; narrow with --after or --source`).

## open

Expands a ref returned by search into the full item — the whole
message with its thread context, the mail with its headers, the event
with its attendees. Refs are `<source>:<path>`; the path is
crawler-owned and stable across syncs.

```
$ trawl open imessage:msg/8842
```

## doctor

Diagnoses one crawler or all of them: source registered, contract version,
auth state, source store reachable, permissions (TCC), archive
integrity. Every failing check comes with the exact remedy command.

## Namespaces

`trawl <source>` opens one crawler's own verbs. The list is served from
the source's manifest, so it teaches itself without naming package internals:

```
$ trawl imessage
iMessage — Local-first iMessage archive crawler.

Verbs:
  chats            Chats
  contacts export  Export contacts
  messages         Messages
  open REF         Open
  search QUERY     Search
  status           Status
  sync             Sync
  who NAME         Who

Run a verb: trawl imessage <verb>
```

`trawl <source> <verb> [args]` runs that verb through the registered
crawler and streams its output; `--json` on a source that emits JSON
flows through. `trawl <source> --json` returns the verb list as JSON for
agents. An unknown or incomplete verb gets a trawl-owned error, never the
crawler's — source internals stay internal.

## Behaviour rules

- `--json` on any verb emits the structured equivalent; `sync --json`
  emits JSONL progress events. Human output is the default and is a
  first-class surface, not a formatting afterthought.
- Exit codes: 0 success, 1 failure, 3 partial (some sources failed —
  the failures are on stderr, the successes are real).
- Errors are one human sentence plus the exact remedy, never a stack
  trace. In JSON: `{"error": {"code", "message", "remedy"}}`.
- Colour only when stdout is a TTY; never semantic-only (states are
  words, colour is decoration).
- No prompts, no interactivity. Every command is safe to run from an
  agent.

## Discovery

Crawlers are registered explicitly in `trawl`: one hand-written list of
source constructors. The manifest still comes from each crawler's
contract metadata. Registration is code — no config files, no drop-in
manifests, no per-source install step.
