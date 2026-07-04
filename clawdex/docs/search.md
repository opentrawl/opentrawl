---
written_by: ai
---

# Search

`clawdex search <query>` finds people by indexed names, aliases and handles,
and notes by text. It is local and offline. No external service is involved.

```bash
clawdex search dinner
clawdex search sally@example.com
clawdex search +1555
clawdex search "negroni recipe"
clawdex search whatsapp --json
```

## What gets searched

- Person names, IDs, and tags
- Person emails and phone numbers (normalized)
- Note bodies, kinds, sources, and topics

Hits are printed as a labelled table, best match first, with the person
and the matched fragment. 20 results come back unless `--limit` says
otherwise; zero results print a sentence, never silence:

```text
Search "dinner": showing 2 of 2, best match first.
Show a person: clawdex person show NAME (their notes: clawdex note list NAME)

date              who             text
                  Sally O'Malley  sally@example.com · +1 555 0100
2026-05-08 09:15  Sally O'Malley  ...follow up about dinner...
```

`--json` returns `{query, results, total_matches, truncated}` where
`results` is a list of `SearchHit` objects (never null).

## How matching works

Person lookup uses the derived SQLite index. Prefix queries work for names,
aliases and handles, so `mo` matches `Mohamed`.

Notes use a case-insensitive substring match against note fields.
For phone numbers the search normalizes both the query and the stored
value (strips spaces, dashes, parentheses, and a leading `+`), so any of
these find Sally:

```bash
clawdex search "+1 555 0100"
clawdex search "(555) 0100"
clawdex search 15550100
```

For emails the match is plain substring against the lowercase value, so
`gmail.com` works as a "find everyone on Gmail" query.

## Combine with timeline and grep

`search` is for finding the right thread; once you've got it, use
[`timeline`](timeline.md) for the full history of that person, or `rg` for
free-form regex on the data repo:

```bash
clawdex search "ankara"
clawdex timeline mehmet
rg -n "ankara" ~/.clawdex/contacts/people
```

## Indexes

Derived indexes live under `index/`:

```text
index/
  index.db
```

This database is rebuilt automatically as the markdown changes. It is derived,
not authoritative. Delete it and clawdex regenerates it on the next read.
Markdown is canonical; see
[Markdown Storage](markdown-storage.md).

## Related pages

- [People](people.md), [Notes](notes.md), [Timeline](timeline.md)
- [Markdown Storage](markdown-storage.md)
