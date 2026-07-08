---
written_by: ai
---

# Rendering

Every crawler's human output is built from four shared crawlkit
components. Crawlers hand over data; the library owns layout, labels,
wrapping, truncation, empty states and time formatting. Hand-rolled
list or table rendering in a crawler is a defect.

The bar is the blind-person test (AGENTS.md): someone who has never
seen the tool must understand every line cold — what each column is,
what each value means, and what to do next.

## The components (crawlkit/render)

- `WriteList` — record lists: search hits, messages, tweets, emails,
  events. Heading first (`Bookmarks: showing 20 of 398, newest
  first.`), then copy-pasteable hint lines (`Open: trawl birdcrawl
  open REF`, `More: ...`), then a labelled table: `date  source  who  where
  ref  text`. Columns whose values are all empty are omitted (the
  source column only appears in the federated trawl view). A row can
  carry a date-only value (an all-day event): the date column shows
  the date alone, never a fake `00:00`. The text column takes all
  remaining width and wraps; search snippets clamp at 2 lines,
  browse views show full text. At narrow widths who and where
  shrink first, then refs shed to a per-row `open:` line — date,
  source and ref cells are never truncated. Zero rows print only the
  empty sentence (`No matches for "boat".`) — never a bare header,
  never silence.
- `WriteTable` — entity tables: chats, contacts, who candidates.
  Lowercase headers, per-column wrap or truncate, right-aligned
  counts, columns fitted to the terminal.
- `WriteCard` — one record in detail (an email, an event): title,
  wrapped `Label: value` fields, prose body, next-step hints.
- `WriteTranscript` — ordered conversation context around one message,
  with day separators and a `>` marker on the target.

Help text comes from `crawlkit/usage`: commands declared in groups
(read your archive / keep it fresh / health), rendered identically
for every crawler.

## Times

Human mode: short local, `2026-07-02 21:40`, everywhere — tables,
status, log lines. JSON: full RFC3339 with local offset. The two
surfaces never share a formatter.

## Refs

Human rows show the short alias (`t7k3f`) in a labelled `ref` column;
the `Open:` hint says what to do with it. Full refs stay in JSON
(docs/short-refs.md). The archive owner renders as `me` in both
surfaces.

## Guard rails

- Human mode must never emit JSON: each crawler's print path errors
  on an envelope with no human renderer, and contract checks probe
  status, doctor and search without `--json` and fail on JSON or empty
  output.
- A mistyped command finishes its log run as `rejected` — user
  feedback, never a recorded crawler error.

Adopted by birdcrawl and telecrawl first; remaining crawlers move on
next touch. imsgcrawl's private text table and the gogcrawl/clawdex
copies are superseded by these components.
