# Handover Prompt

You are working in `/Users/josh/code/crawlers/photoscrawl`.

Build the first real implementation slice for `photoscrawl`: a Go-only
OpenClaw/crawlkit Apple Photos crawler that initializes `photos.sqlite`, then
reads Josh's Photos library read-only through the safest Apple-supported path.

## Product Goal

Create a local-first personal-media archive that helps a user understand their
own Photos library while staying user-controlled and privacy-first:

- user-owned local SQLite archive;
- no cloud calls by default;
- no Photos writeback;
- provenance on every derived claim;
- observations first, clusters/trips/people later.

## Current Seed

- `cmd/photoscrawl`: CLI skeleton with `init` and `status`.
- `internal/archive`: schema and status helpers using crawlkit.
- `docs/architecture.md`: source, classification, location, and identity policy.

## First Implementation Slice

Implement `crawl --library <Photos Library.photoslibrary> --json`.

Requirements:

1. Read the Photos library without mutating it.
2. Prefer PhotoKit for supported metadata.
3. Create a snapshot/cursor record so repeated runs can detect changes.
4. Populate at least:
   - `source_library`
   - `asset`
   - `asset_resource`
   - `album_membership` if accessible without major detour
   - `location_observation`
   - `evidence_ref`
   - `asset_fts`
5. Do not download all originals in `crawl`. Mark local/remote resource state
   and queue downloads/classification for `classify`.
6. Add Darwin-specific bridge code only behind a narrow Go interface. A tiny
   cgo bridge is allowed. Do not add Swift/Python/Node product code.
7. Keep tests away from the live Photos library. Test schema, ID stability, and
   importer behavior with temp fixtures or a fake provider.

## Classification Slice After Crawl

Implement `classify` after metadata ingestion works:

- bounded local cache/ringbuffer for originals needed from iCloud;
- local Vision/Core ML labels;
- OCR for text-heavy assets;
- face boxes/counts and Apple People labels where extractable;
- barcodes/QRs;
- screenshot/document/receipt markers;
- quality/similarity signals;
- evidence refs for every observation.

Signal quality matters more than avoiding CPU. Disk blowups and privacy leaks
are unacceptable.

## Commands To Preserve

```sh
photoscrawl init --json
photoscrawl status --json
photoscrawl crawl --library "$HOME/Pictures/Photos Library.photoslibrary" --json
photoscrawl classify --all --json
photoscrawl search --query "drone beach portugal" --json
photoscrawl open --id asset:<id> --json
photoscrawl neighbors --id asset:<id> --json
photoscrawl evidence --row-id asset:<id> --json
```

## Non-goals

- No durable person table.
- No durable trip/place/event truth table.
- No relationship inference.
- No global clustering.
- No cloud model calls by default.
- No writeback to Apple Photos.
