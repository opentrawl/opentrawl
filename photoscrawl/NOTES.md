# photoscrawl coordination notes

## 2026-07-05 — crawlkit adoption wave (TRAWL-93), lane photoscrawl-wave

Landed the deferred adoption-wave items in one pass. **Rebuild the
photoscrawl binary** before your next run — output shapes and the manifest
changed.

What changed that affects you:

- **Manifest placeholders are UPPERCASE now** (`QUERY`/`REF`/`PATH`, not
  `<query>`/`<ref>`/`<path>`). `trawl photos <verb>` dispatches them as args;
  federated search/open now work end to end.
- **Human output rebuilt on crawlkit/render + crawlkit/usage.** search is a
  labelled table (no pipe rows, no raw RFC3339 — capture dates render in the
  photo's own zone); open is a card (summary title, labelled fields, short-ref
  field, description body) with the full 32-hex ref removed from human output
  (it stays in `--json`). Help is grouped usage.Doc.
- **Error envelope** now goes through crawlkit/output.WriteError; `--json`
  errors are `{"error":{code,message,remedy}}` (unchanged shape).
- **`--limit` contract**: silent caps gone (search 200, classify 1000).
  `--limit 0` is a usage error on both search and classify. classify default
  stays 100.
- **place/backfill_store.go** now uses the mattn sqlite3 driver (matches
  crawlkit/store); modernc dropped from go.mod. Read-verified on the live
  archive (24,325 backfill keys). Backpop progress is unchanged (output files
  on disk); nothing about the 44k run was touched.

### Action needed: run one `sync` after you pick up the new binary

The sync-state table was renamed `sync_state` → canonical `sync_cursor_state`
(TRAWL-82; lets crawlkit drop its *Mapped adapter). Pre-1.0, no data migration:
the old table is abandoned and **one `sync` re-derives** the new one. Until you
run that sync, read verbs on the existing archive still work but **search
degrades to long refs** (open still shows the short ref, because its lookup
isn't freshness-gated). One sync restores short refs everywhere. This cannot
re-process classified photos or the backpop — the cursor gates neither
(classify uses the queue+cards tables, backpop uses on-disk output files).
