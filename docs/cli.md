---
written_by: ai
---

# The trawl CLI

The single entry point. Five verbs, no more. Follows
[clig.dev](https://clig.dev); adding a verb or flag is a design
decision recorded in this file, not a response to a feature request.

All examples use synthetic data.

## Verbs

```
trawl status [source]
trawl sync [source ...]
trawl search <query> [--source a,b] [--limit n] [--after date] [--before date]
trawl open <ref>
trawl doctor [source]
```

Global flags: `--json`, `--help`, `--version`. Nothing else.

## status

Health of every installed crawler at a glance. One line per source:
state, freshness, and the headline counts the crawler itself declares.

```
$ trawl status
SOURCE     STATE  FRESH    HEADLINE
imessage   ok     2m ago   12,345 messages · 87 chats · since 2014
telegram   ok     1h ago   23,456 messages · 145 chats · since 2016
whatsapp   stale  3d ago   3,456 messages · 42 chats · since 2019
gmail      error  —        auth expired — run: trawl doctor gmail
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

Diagnoses one crawler or all of them: binary found, contract version,
auth state, source store reachable, permissions (TCC), archive
integrity. Every failing check comes with the exact remedy command.

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

Crawlers are found by probing a built-in registry of known binary
names on PATH (which includes `.dev/bin` inside the dev shell), plus
drop-in manifests in `~/.trawl/apps/*.json` for third-party crawlers.
A binary is a crawler if `<binary> metadata --json` returns a valid
manifest. No configuration, no registration step.
