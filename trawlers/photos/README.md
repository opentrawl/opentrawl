---
written_by: ai
---

# photoscrawl

Local-first Apple Photos crawler for OpenTrawl.

`photoscrawl` builds a `photos.db` archive from a user's Photos library. The
goal is not photo backup. The goal is to help users understand their own library:
where photos were taken, when they were taken, what is visible, which
documents/screenshots/receipts exist, and which assets belong together.

## Principles

- Go product code only.
- Use `github.com/opentrawl/opentrawl/trawlkit` for shared crawler mechanics.
- Local-first by default; no cloud model calls unless the user explicitly selects
  assets or derivatives to send.
- Read-only Photos access. Never write back to Photos.
- Snapshot before crawling live library state.
- Metadata for all assets, local classification for high-signal coverage.
- Store observations and internal provenance, not final people/trip/place truth.

## First Commands

```sh
trawl photos metadata --json
trawl photos status --json
trawl photos doctor --json
trawl photos sync --json
trawl photos classify --limit 100 --json
trawl photos classify --model gemma4:e4b --limit 20 --json
trawl photos search "drone beach portugal" --json
trawl photos open photos:asset/<32-hex> --json
```

Human search output shows a short ref when the archive can resolve it safely.
Use that alias with `open` for local terminal work. JSON keeps
the canonical `photos:asset/<32-hex>` ref.

Default runtime paths live under `~/.opentrawl/photos/`. The primary
database is `~/.opentrawl/photos/photos.db`; provider caches, exported
originals, logs, config and eval artifacts stay under the same crawler root.
Set `library_path` in `~/.opentrawl/photos/config.toml` only when your Photos
library is not at the default macOS path.

A lifecrawler-format `export` command is planned but does not exist
yet.

`sync` snapshots `database/Photos.sqlite` with trawlkit's SQLite snapshot helper
and reads the copy. This is the headless path: it needs Full Disk Access for the
terminal or app, not a recurring Photos TCC prompt. PhotoKit remains available
only for explicit original export flows such as
`photoscrawl-lab eval-card --allow-icloud-downloads`.

`sync` does not export originals or force iCloud downloads. It records already
local package media paths for derivatives/renders/originals when they exist, so
content classification can use local files without changing Photos or iCloud
state. Every imported asset is queued for `classify`.

`classify` drains that queue into local metadata observations. With
`--model <ollama-model>`, it also resolves place context cache-first, sends
already-local image bytes to an Ollama-API vision model, and stores one photo
card per asset: a one-line summary, rich visual description, OCR text, and
uncertainty list. Search indexes that card text and mechanical place
observations directly. It does not store rendered tag rows, object lists, or
derived term lists.

If Apple place geocoding is throttled, located photos move to `place_pending`
instead of being carded without place context. Later `classify` runs retry
parked photos and unpark them once the place cache covers their location.

If an original download fails, `classify` marks that asset as `failed_download`.
It will not try that download again until an operator resets it:
`sqlite3 ~/.opentrawl/photos/photos.db "update classification_queue set state='metadata_classified', reason='operator reset failed_download', updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') where state='failed_download';"`.

## photoscrawl-lab

`photoscrawl-lab` is a research harness for prompt/model evals and
place-context probes. It is not part of the `trawl` contract surface.

```sh
go run ./cmd/photoscrawl-lab place-context --input /tmp/photoscrawl-eval/metadata/E001.json --json
go run ./cmd/photoscrawl-lab eval-card --library /tmp/Example.photoslibrary --out /tmp/photoscrawl-evals/run-001 --cache-dir /tmp/photoscrawl-cache/originals --limit 1 --json
go run ./cmd/photoscrawl-lab known-places set --input /tmp/known-places.json --json
go run ./cmd/photoscrawl-lab known-places list --json
```

`place-context` enriches one asset's own latitude/longitude/accuracy/time into
address hierarchy and candidate nearby POIs. Apple's network-backed
CoreLocation reverse geocoder is the required step. MapKit POI search is
optional venue context: no POI found is recorded as `poi_status: "none"`,
while real provider errors still fail. Text output is a compact deterministic
place card; `--json` returns provider context. If `--input` is an existing
cached provider result, the command renders from that cache without provider
calls. Apple address areas of interest are rendered as map context, not as POIs.

`known-places` stores user-supplied home, former home, and work coordinates with
date windows. Classification uses them to replace nearby business candidates
with `At home`, `At home at the time`, or `At work` card labels while keeping
the address line.

`eval-card` is an opt-in research harness for prompt/model evaluation. It uses
the tracked prompt file in `prompts/`, prepares canonical full-resolution JPEGs
from originals, passes full metadata as a sidecar prompt input, and writes all
private images, metadata, and model responses under
`~/.opentrawl/photos/evals`. If `--allow-icloud-downloads` is set, PhotoKit
may download missing originals into `~/.opentrawl/photos/cache/originals`;
normal sync/classify commands do not force iCloud downloads.

There is no standalone place backfill command. Library-scale place caching is
handled by classify's cache-first resolver, which can read legacy backfill
artifacts.

## Current Useful Output

Today the POC sees useful source facts and optional model observations:

- asset timing, media type, dimensions, favorite/hidden state, timezone, and
  burst metadata;
- resource type, UTI, filename, local/remote availability, iCloud download need,
  and resource hash when already local;
- album membership and raw GPS observations with internal provenance;
- metadata-only observations for media type, geometry, burst membership,
  resource UTI/type, and weak screenshot/document/receipt candidates from
  filenames, albums, and metadata;
- optional photo-card observations from already-local image derivatives or
  originals, full-text searchable;
- mechanical place observations from cache-first provider context, including
  address lines and tiered venue candidates when provider geometry supports
  them;
- status coverage counts for GPS, observations, local resources, remote
  resources, classification queue state, and observation types;
- search/open JSON shaped for users and tools. Provenance stays on
  observation rows through source, model, and prompt version columns.

It does not create durable identities, trips, places, relationships, embeddings,
or global clusters yet.

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

Build `photos.db` with:

- assets and resource metadata from Apple Photos;
- local original-download queue; disk use is bounded by serial downloads and
  delete-after-classify, not a size ledger;
- GPS observations as raw coordinates only;
- album membership;
- file/resource hashes when originals are available;
- Vision/Core ML observations: labels, OCR, faces, barcodes, screenshot/document
  markers, quality/similarity signals where useful;
- internal provenance for every derived observation;
- JSON metadata/status/doctor/sync/classify/search/open commands.

Out of scope for v1:

- durable person identity;
- durable trip/place/event truth;
- relationship inference;
- global photo clustering;
- cloud classification by default;
- Photos writeback.
