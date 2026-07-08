---
written_by: ai
status: contract — the authoritative statement of what flows from a synced photo to the classification prompt and back; code change that alters any shape here updates this doc in the same commit (TRAWL-170)
---

# Data contract: photo → location → model context

This document states the exact contract from a synced Photos asset to the
classification prompt and back into stored observations. It exists so that
provider work (TRAWL-171), sync fixes (TRAWL-172), download work (TRAWL-173)
and any future trips/backfill design change this pipeline deliberately instead
of rediscovering it. Everything here was verified against the code on
2026-07-07; file references are the proof path.

## The pipeline in one paragraph

`sync` snapshots Apple's Photos.sqlite by plain file copy and upserts assets,
resources, album memberships and GPS into the archive, fingerprinting each
asset and queueing new or changed assets for classification. `classify` drains
that queue: it loads each asset's metadata, matches known places, resolves GPS
to place context (cache → backfill → Apple, the only provider), renders one
prompt per asset with a JSON metadata sidecar plus the best local image, and
sends it to the configured vision model. The response is parsed into card
observations (summary, description, OCR, uncertainty) and place observations
(address, POI candidates, at most one venue line), all written in one
transaction per asset. The search index rebuilds from asset, resource and
membership rows plus these observations; the open card reads both.

## Stage 1 — what sync stores

Provider: `SQLiteSnapshotProvider` (`internal/photos/sqlite_snapshot.go:23`;
`provider_darwin.go` only constructs it) copies
`<library>/database/Photos.sqlite` (with its WAL and SHM sidecars) and reads
the copy. Plain file access
under Full Disk Access; PhotoKit is never touched by sync.

Per asset, sync writes:

- `asset` — identity, media kind/subtypes, creation/modification/added dates,
  timezone name as Photos recorded it, dimensions, duration, favourite,
  hidden, burst, camera fields, raw `metadata_json`.
- `asset_resource` — one row per resource: type, UTI, original filename,
  local path when the file is on disk, size, `available_locally`,
  `needs_download`.
- `album_membership` — album title and kind per membership. This feeds the
  prompt (stage 4), which is why membership truth matters beyond display;
  the join defect and its fix live in TRAWL-172.
- `location_observation` — latitude, longitude, horizontal accuracy, straight
  from the snapshot. Coordinates are stored exactly as Photos holds them
  (phone photos in mainland China are GCJ-02; see the provider seam rules).
- `crawl_seen_asset` — the asset fingerprint: sha256 over the entire
  marshalled asset JSON (`internal/archive/crawl.go:299`), including albums
  and favourite.

Change detection: an asset whose fingerprint changed is re-upserted.
`resetAssetDerivedRows` (`internal/archive/crawl_writes.go`) deletes only
the rows that sync re-imports losslessly: resources, memberships,
locations, local metadata, OCR/text, faces, edges, and `asset_fts`.
It does not delete `model_observation` or `place_observation`. Instead,
it marks their active rows with `stale_since` and `stale_reason`, keeps
their search entries, and re-enters the asset in `classification_queue`.
A queue row already in `failed_download` still keeps its state and reason
on upsert (`internal/archive/crawl_statements.go:76`), so download
failures are not silently reset by a sync.

## Stage 2 — the classification queue

`classification_queue` success states: `pending` → (`metadata_classified`)
→ `content_classified`, with `place_pending` as a side-parking state for
assets whose GPS could not be resolved (stage 3). Failure and skip states,
all set by the classify pipeline (`internal/archive/classify_pipeline.go`):
`content_failed` (model or parse failure), `failed_download` (original
export failed; preserved across syncs, see stage 1),
`content_not_in_photokit`, `content_no_content_available`, and
`content_skipped` (unsupported media). Batches load newest first, with GPS
presence breaking timestamp ties (`internal/archive/classify_inputs.go:106`). A `--refresh-model` run
additionally re-selects `content_classified` assets that lack a
`card_summary` from the requested model id at the current prompt version —
that is how a model or prompt migration re-cards the corpus without touching
the queue states.

## Stage 3 — location and place resolution

Input per asset: the first `location_observation` row (ordered by id) plus
the creation date. No location means no place phase and no location block in
the prompt; that is a normal card, not an error.

### Known places

`known_place` rows carry kind (`home`, `former_home`, `work`), display name,
coordinate, radius (default 75 m), and an optional validity window.
`matchKnownPlace` (`internal/archive/known_places.go:214`) picks the best
in-radius match: photos inside the validity window match normally; photos
taken after the window match with `After` set — "former home" — because that
relationship explains the visit better than whichever business is registered
nearby. Photos before the window never match. Selection prefers an in-window
match over an after-window match regardless of distance; within the same
phase, ties break by distance, then kind rank (home, former home, work),
then name.

A known-place match suppresses venue machinery on purpose: the prompt gets
`known_place` and no `venue_candidates` (address and area context still go
in), and storage writes a `known_place` place observation instead of POI
candidates (stage 5). Nobody needs "Sushi Bar, 40 m" on a photo taken at
home.

### The resolver

`place.NewResolver` (`internal/place/resolver.go`), driven by
`internal/archive/classify_place_phase.go` with radius 150 m. Resolution
order per coordinate key:

1. **Cache** — `~/.opentrawl/photoscrawl/cache/place-context/<key>.json`.
   The key is the coordinate rounded with the radius; nearby photos share
   one lookup. Lookup also accepts the legacy hashed key format, so old
   cache entries stay valid.
2. **Backfill** — a manifest-indexed directory of prior bulk runs
   (`Paths.PlaceBackfillDir`), read-only legacy artifacts.
3. **Provider** — Apple CoreLocation reverse geocode plus MapKit POI search
   (`internal/place/apple_darwin.go`), the only provider wired today.
   Provider starts are spaced 250 ms. Throttling retries with 2 s then 10 s
   backoffs; a third consecutive throttle stops live geocoding for the whole
   run, and so does a single timeout (Apple tarpits rather than
   fast-rejects) — in both cases the remaining assets park. "No placemark
   for this coordinate" is a cached empty result, not a failure — the photo
   cards with GPS but no address, and the coordinate is never re-geocoded.

   Seam obligations for any new provider (TRAWL-171). First, the provider
   plug-in point does not exist yet: `ResolveProvider` hardcodes the Apple
   call (`internal/place/resolver.go:106`), dispatched by build tags — there
   is no Provider interface. TRAWL-171's first deliverable is designing that
   seam, not implementing against one. Second, map throttle and timeout
   conditions onto the `place.ErrProviderThrottled` and
   `place.ErrProviderTimeout` sentinels (`internal/place/types.go:102`); the
   parking and whole-run-stop logic is `errors.Is` on exactly those values,
   so a provider returning bare errors silently breaks invariant 6. Third,
   provider output must round-trip the cache: a stored `place.Result` is
   re-validated on load (`loadResolvedResult` → `validateComplete`) and
   candidate distances are recomputed (`calibrateCandidates`,
   `internal/place/resolver.go:248`) — a result that only works in memory
   is not a passing result.

Assets whose key could not be resolved park as `place_pending` and unpark
automatically on a later run once their key resolves — from cache or from a
successful live lookup (up to 200 pending keys re-checked per run). Parking is the designed behaviour under provider
failure: never block the batch, never fabricate place context.

### The result shape and tiers

Providers produce `place.Result` (`internal/place/types.go`): `area` levels,
`address`, `map_features` (no wired provider fills this today — the OSM gap
that motivates TRAWL-171), `poi_status`/`poi_reason`/`poi_total`,
`poi_candidates`, plus provider/source/cache provenance.

The tiers, and where each is assigned (four candidate/context tiers plus
one storage-only tier):

- `venue_candidate` — assigned by geometry (`internal/place/tier.go`):
  within `max(accuracy, 25 m)` of the photo and no same-distance candidate
  of a different category competes.
- `nearby_poi` — assigned by geometry: every other candidate, including
  candidates with no usable distance.
- `area_context` — not a candidate tier; assigned at storage time to the
  address/area observation (stage 5).
- `known_place` — storage-only tier on the known-place observation row
  (stage 5), never a provider candidate tier.
- `confirmed_venue` — **never assigned by geometry.** Only the model can
  promote a candidate to confirmed, by corroborating it against the image,
  and the promotion happens at storage time (stage 5). Distance is not
  confidence; this is the rule that keeps "Versace, 80 m" off a temple
  photo.

## Stage 4 — exactly what the model receives

One request per asset: the prompt template `prompts/photo-card-v3.md`
(version `photo-card-v3.2`) rendered with a single `MetadataJSON` value, plus
one image and temperature 0.1. Image selection: the first classifiable
local resource path (jpg/jpeg/png/heic) in stored resource order — no
quality preference between derivative, render and original. When no local
path exists and the original is in iCloud, classify exports it through
PhotoKit into a per-run scratch cache and sends that file, deleting it after
use (`internal/archive/classify_originals.go`); TRAWL-173 owns hardening
this export path.

The sidecar (`photoCardMetadataJSON`, `internal/archive/model_classifier.go:109`):

```json
{
  "capture":  { "local_time": "...", "timezone": "..." },
  "media":    { "kind": "...", "width": 0, "height": 0,
                "subtypes": ["screenshot", "..."], "duration_seconds": 0 },
  "library_context": {
    "original": { "availability": "local|in_icloud", "bytes": 0, "filename": "..." },
    "albums":   [ { "title": "...", "kind": "generic_album:2:0" } ],
    "favorite": true, "hidden": true, "burst_member": true
  },
  "location": {
    "gps": { "latitude": 0, "longitude": 0, "horizontal_accuracy_meters": 0 },
    "place_context": {
      "address_line": "...",
      "area": [ { "level": "country|...", "name": "..." } ],
      "venue_candidates": [ { "candidate_id": "venue_candidate_1", "name": "...",
                              "distance_meters": 0, "tier": "venue_candidate|nearby_poi",
                              "category": "..." } ],
      "poi_status": "found|none|provider_error"
    },
    "known_place": { "name": "...", "relationship": "the user's home" }
  },
  "camera": { "display": "...", "make": "...", "model": "...", "...": "..." }
}
```

Rules the sidecar enforces:

- Empty blocks are omitted rather than sent as empty strings — with one
  known exception: `library_context.original` is always present when the
  asset has resources, and its `availability` is `""` when the selected
  resource is neither local nor marked for download
  (`internal/archive/model_classifier.go:297`). A place resolved to nothing
  sends no `place_context` at all.
- At most 5 venue candidates (`topPOICandidates`), nearest first, confirmed
  and candidate tiers always included, deduplicated by name+tier, each with
  a stable `candidate_id` the model must echo back.
- `venue_candidates` and `known_place` are mutually exclusive (known place
  wins).
- `known_place.relationship` is plain words ("the user's former workplace"),
  never a kind enum.
- Capture time renders in the asset's own timezone; when Photos recorded no
  usable zone it renders UTC — the machine's timezone is a fact about the
  reviewer, not the photo.
- Coordinates and meters pass through `cardformat` rounding; the raw stored
  values stay in the archive.

What the model must return (the prompt's own contract): Summary,
Description, Venue plausibility (`candidate_id`, `verdict` ∈ `corroborated`
| `plausible` | `inconsistent`, one-sentence `reason` — judged for the top
candidate only, and required by the parser only when venue candidates were
actually sent), OCR, Uncertainty. The prompt forbids echoing metadata,
paths, ids, or coordinates into prose, and instructs the model to trust the
image over place context when they conflict.

## Stage 5 — what gets stored back

Per asset, in one transaction (`internal/archive/model_write.go`,
`internal/archive/classify_place.go`):

- `model_run` — one row per batch: model id, prompt version, input count,
  and run metadata (no per-call timings; `started_at` and `completed_at`
  carry the same commit timestamp today).
- `model_observation` — `card_summary`, `card_description`, `card_ocr`
  (when text exists), one `card_uncertainty` per bullet. Each row carries
  source `photo_card`, the model id and prompt version; `evidence_id` is
  written empty today, so the raw model response is not addressable from
  the observation row.
- `place_observation` — cleared and rewritten per classify:
  - `address` (tier `area_context`) — the formatted address line.
  - `known_place` — the plain-words label, when matched (and then nothing
    else venue-shaped is written).
  - `poi_candidate` — candidates in nearest-first order, up to and
    including the first venue-eligible one: the write loop stops once it
    writes the venue line, so a corroborated top candidate persists only
    itself and the later candidates are dropped. Only when no venue line is
    written does every sent candidate persist. These rows are selection
    provenance, deliberately excluded from the search index so a nearby
    "Meadow Grill" cannot outrank a card that is actually about grilling.
  - `venue` — at most one row, the product's "Venue:" line. Written only
    when the model's verdict allows it: `corroborated` promotes the
    candidate to `confirmed_venue`; `plausible` keeps a geometric
    `venue_candidate` as the venue line; `inconsistent` or no verdict means
    no venue line, whatever the geometry said. This is the code gate that
    makes venue claims image-corroborated end to end.

## Contract invariants

These are the rules a change must not break, each enforced by the shapes
above:

1. **No provider selector in the prompt path.** New providers (Geoapify,
   Amap) produce `place.Result` behind the resolver — TRAWL-171 designs the
   plug-in seam, which does not exist yet (stage 3). The classify pipeline
   and prompt shape do not know provider names
   (`docs/evals/geocoding-context.md`, ratified).
2. **Coordinate datum is a provider concern.** The archive stores
   coordinates exactly as Photos holds them (GCJ-02 in mainland China from
   phones, WGS-84 from DSLRs). Any provider that consumes WGS-84 converts
   behind the seam; nothing upstream shifts stored data. (TRAWL-171
   productizes the conversion.)
3. **Geometry caps at `venue_candidate`; only image corroboration confirms.**
4. **Unselected candidates never reach the search index.**
5. **Known place suppresses venue machinery**, in prompt and in storage.
6. **Deficient resolution parks; it never blocks a batch and never
   fabricates.** An empty Apple result is a cached fact, not an error.
7. **No machine facts in prose.** Raw coordinates, paths, ids and EXIF stay
   in the archive as evidence; the prompt bans them from card text and the
   renderer owns coordinate display.
8. **Unknown timezone renders UTC**, never the reviewing machine's zone.
9. **Album titles and favourite flags are model inputs**, so their truth is
   part of this contract — membership pollution (TRAWL-172) is a card
   quality bug, not just a display bug.

## What this gates

- **TRAWL-171 (one lookup seam)** implements invariants 1–2, honours the
  error-sentinel seam obligation (stage 3), and fills `map_features` and
  better `poi_candidates`. Two requirements: candidate ranking must prefer
  venues, parks and landmarks over accommodation and sanitation
  infrastructure at comparable distances (evidence, 2026-07-07: Sanyue
  Teahouse lost to a hotel at 126 m; Big Trees Trail lost to "Toilets");
  and the ranking work should consider letting the model judge plausibility
  across all sent candidates instead of only the top one — the sidecar
  already ships ids for all five, so that is a prompt-version change gated
  on an eval run, not a schema change.
- **TRAWL-172 (album join)** repairs invariant 9's inputs. Fixing
  memberships changes fingerprints at scale, so its archive-level
  acceptance sync runs as measured, cleared spend (see "Invalidation").
- **TRAWL-173 (downloads)** feeds stage 4's image selection: its output is
  the guarantee that a classifiable local image path exists (or a truthful
  `in_icloud` availability), never a silent 360 px thumbnail.
- **Trips (TRAWL-174, parked)** starts from the known-place semantics in
  stage 3, which this doc now states; the spike designs the
  known-places-versus-trips split from here.

## Invalidation — ruled (TRAWL-176)

Cards are **context-maximal by design**. Josh's ruling (2026-07-08): "the
key product feature here imo is that we feed ALL the metadata (including
enriched location stuff, and trip info later on) → into the LLM so that it
knows this when it is classifying the image. thats the whole point."

Josh's rework ruling on 2026-07-08 supersedes deletion semantics for
model and place products: changed inputs mark cards stale, and successful
re-card runs retain history.

Consequences:

- The fingerprint stays whole-asset. A metadata edit (album, favourite —
  and later, enriched context such as trips) genuinely stales the card's
  inputs, so it legitimately marks stale and re-cards. This is invariant 9
  extended: everything the model consumes is change-tracked.
- Sync deletes only Apple-derived rows it can rebuild from the same
  snapshot. It marks active `model_observation` and `place_observation`
  rows stale with `stale_since` and `stale_reason`, for example
  `asset metadata changed in sync (fingerprint drift)`.
- Stale active cards stay usable. Search keeps them findable and marks
  hits with `[STALE]`. `open` renders the full card with a debug banner:
  `DEBUG INFO: THIS CARD IS STALE - generated before the latest metadata
  sync (...) PLEASE FIX.`
- A successful re-card does not delete the previous card. It sets
  `superseded_at` on prior active model and place rows, writes fresh rows,
  and removes superseded rows from default search/open results. The old
  rows stay in SQLite as history.
- The sync summary, `--json` output, and log line report
  `marked_stale_model_assets`, `marked_stale_model_rows`,
  `marked_stale_place_assets`, `marked_stale_place_rows`, and
  `classification_queue_pending`.
- The rejected alternative (content-only cards, metadata joined at display
  time) is recorded here so it is not re-proposed: it was rejected because
  it guts the product's core idea, not on cost grounds.

Sync against an enriched archive now reports the stale count and pending
queue depth in the run output. The operator can see the cost before deciding
whether to drain the queue with `classify`.
