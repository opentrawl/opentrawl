# photoscrawl

Local-first Apple Photos crawler for the OpenClaw crawl-family ecosystem.

`photoscrawl` builds a `photos.sqlite` archive from a user's Photos library. The
goal is not photo backup. The goal is to help users understand their own library:
where photos were taken, when they were taken, what is visible, which
documents/screenshots/receipts exist, which assets belong together, and what
evidence supports each result.

## Principles

- Go product code only.
- Use `github.com/openclaw/crawlkit` for shared crawler mechanics.
- Local-first by default; no cloud model calls unless the user explicitly selects
  assets or derivatives to send.
- Read-only Photos access. Never write back to Photos.
- Snapshot before crawling live library state.
- Metadata for all assets, local classification for high-signal coverage.
- Store observations and evidence, not final people/trip/place truth.

## First Commands

```sh
go run ./cmd/photoscrawl init --json
go run ./cmd/photoscrawl status --json
go run ./cmd/photoscrawl crawl --library "$HOME/Pictures/Photos Library.photoslibrary" --json
go run ./cmd/photoscrawl classify --limit 100 --json
go run ./cmd/photoscrawl search --query "drone beach portugal" --json
go run ./cmd/photoscrawl open --id asset:<id> --json
go run ./cmd/photoscrawl evidence --row-id asset:<id> --json
```

Planned crawl-family commands:

```sh
photoscrawl neighbors --id asset:<id> --json
```

`crawl` tries PhotoKit first for metadata. PhotoKit enumerates the active system
Photos library; the `--library` path is validated and recorded as the requested
source. If PhotoKit is unavailable or denied, the POC falls back to a read-only
`database/Photos.sqlite` transaction and labels that evidence as
`photos_sqlite_snapshot`.

`crawl` does not export originals or force iCloud downloads. Resource rows carry
local/remote availability when the source exposes it, and every imported asset is
queued for `classify`.

`classify` currently drains that queue into evidence-backed local metadata
observations only. It does not open originals, thumbnails, or iCloud content.
Vision/OCR/barcode/face classification belongs behind the same queue and evidence
shape once bounded content access lands.

## Why This Shape

This is a local-first personal media index:

- typed local objects;
- provenance on every derived claim;
- entity and link resolution as explainable pipelines;
- graph traversal and timelines as first-class query shapes;
- clusters and trips as later hypotheses, not v1 truth;
- user-owned local archive with no sharing or hidden scoring by default.

Photos are useful because a saved image usually records something the user cared
about: a place, person, document, trip, purchase, home, event, hobby, meal,
screenshot, or drone flight. The crawler's job is to preserve that context
without pretending GPS, face labels, or classifier labels are perfect facts.

## v1 Scope

Build `photos.sqlite` with:

- assets and resource metadata from Apple Photos;
- local original-download queue with bounded cache/ringbuffer;
- GPS observations as raw coordinates only;
- album membership;
- file/resource hashes when originals are available;
- Vision/Core ML observations: labels, OCR, faces, barcodes, screenshot/document
  markers, quality/similarity signals where useful;
- evidence refs for every observation;
- JSON status/search/open/neighbors/evidence commands.

Out of scope for v1:

- durable person identity;
- durable trip/place/event truth;
- relationship inference;
- global photo clustering;
- cloud classification by default;
- Photos writeback.
