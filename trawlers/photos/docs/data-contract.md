---
written_by: ai
---

# Photos pipeline data contract

This is the accepted contract from a current Apple Photos snapshot to a stored
photo card. It states the target boundary even where implementation is still
missing. The status table below prevents an accepted design from being mistaken
for shipped code.

The dependency graph lives in [architecture](architecture.md). Historical eval
results live under `docs/evals/` and do not override this contract.

## Boundary rules

Every stage has an exact input and output. Before downstream work starts, the
operator or development agent reads representative raw inputs after selection,
filtering and formatting, then reads the raw outputs before parsing or
transformation.

A count, schema, successful exit code or plausible summary is not boundary
proof. An empty or malformed output stops that asset. The stage must distinguish
a proved source absence from an extractor, provider or selection failure.

Private source identifiers, paths, image content, metadata, coordinates,
requests and responses stay in private runtime storage. Public tests and docs
use synthetic examples only.

## Current implementation status

This status was inspected at `94a2496221fc7761f5a997fafec9ae769c123e00`.
Refresh it against code whenever the implementation changes.

| boundary | status at inspected main | accepted outcome |
|---|---|---|
| Photos snapshot | implemented | read a consistent SQLite snapshot without changing Photos |
| normalised asset | partial | preserve current or upstream-deleted state and source provenance |
| camera original | implemented | resolve exact `photo` bytes through package, checked cache or signed PhotoKit |
| current rendered still | not implemented | request the largest current rendition through `PHImageManager` with version `.current` |
| full metadata and EXIF | partial | keep lossless exact-original metadata and a readable projection |
| place evidence | partial | cache configured Apple and OSM-derived map and POI evidence |
| rendered model request | not persisted | save the exact request before transmission |
| raw model response | not persisted | retain the response before parsing |
| labelled-prose parser and card rows | implemented in part | parse the declared sections and store the complete card |
| stale and superseded history | implemented | keep stale cards readable and retain replaced cards |

Partial means code exists but does not yet satisfy the full accepted outcome.

## Work tracking

Linear is the only source of truth for work state, priority, ownership and
decisions. This document records inspected code and the accepted product
boundary. It never substitutes for, mirrors or instructs changes to the board.

## Boundary 1: source snapshot

Input:

- the configured Photos library
- `database/Photos.sqlite` and its SQLite sidecars

Output:

- one internally identified source library
- one snapshot identity, completion time and explicit completeness proof
- raw current asset, resource, album and location rows selected from that
  snapshot

## Boundary 2: normalised asset

Input: all selected source rows for one Photos asset.

Accepted output:

- stable private asset identity
- explicit current or `deleted upstream` state
- the complete snapshot that established the state
- first-observed-missing time and optional source deletion time
- capture, modification and added times with their source timezone
- media kind, dimensions and duration
- favourite, hidden, burst and album state
- resource descriptors
- GPS and accuracy when the source contains them
- a fingerprint of the source projection used downstream

Only a proved-complete snapshot may transition a current asset to `deleted
upstream`. Missing assets must remain explainable; they must not silently
disappear from the archive or trigger paid reclassification. Deletion and
restoration update source state, eligibility and provenance outside the
model-generation key. A valid card remains available with its upstream-deleted
flag.

If the archive first observes an asset missing before any card has been stored,
it permanently prohibits that asset's first paid card. Restoration does not
clear the prohibition. Metadata-only processing may run once without changing
it. Stale or superseded card history means a later card is not the first.
Normal reuse and refresh rules decide whether an old card can be reused.

## Boundary 3: image roles

Apple exposes the 2 required roles through different PhotoKit contracts:

- `PHAssetResourceType.photo` is the camera original
- [`PHImageManager.requestImageDataAndOrientation`](https://developer.apple.com/documentation/photos/phimagemanager/requestimagedataandorientation%28for%3Aoptions%3Aresulthandler%3A%29)
  with request version [`.current`](https://developer.apple.com/documentation/photos/phimagerequestoptionsversion/current)
  returns the most recent rendered still, including all edits, at the largest
  available size

`PHAssetResourceType.fullSizePhoto` is a modified resource. It is neither a
higher-quality original nor the canonical selector for the current rendition.

Accepted output:

- exact camera-original bytes, byte count, media type and SHA-256
- current-rendered bytes, byte count, media type, orientation and SHA-256 for
  every image asset
- the exact request version, quality and network policy used to obtain that
  rendition
- resolution source and cache provenance for each role

The camera original supplies provenance and metadata. Classification always
uses the current rendition. An unedited asset may render identically to its
camera original, but byte equality is proved rather than assumed. The model
request records the exact image hash it sent.

The camera-original resolver follows one order:

1. one unambiguous, non-empty file in the Photos package
2. one persistent cache entry tied to the asset version and proved by stored
   size and SHA-256 after restart
3. the signed, authorised PhotoKit app

Media resolution never uses the network except for an explicit PhotoKit request
for an uncached role. Exports install atomically and leave no partial cache
entry after failure. Concurrent requests for the same key share one completed
export. The fixed product cache never evicts a file while a request is reading
it.

The shared resolver work is tracked in
[TRAWL-173](https://linear.app/joshpalmer/issue/TRAWL-173). Read the live ticket
before acting; the implementation table above is the docs-side status inventory.

## Boundary 4: full metadata and EXIF

Input: the exact camera-original bytes from boundary 3 plus source asset facts
from boundary 2.

Accepted output has 2 forms:

- a lossless private record of all metadata and EXIF the extractor can read
- a field-aware human projection for the card request

The projection converts machine values into useful evidence. It uses real local
capture times, standard shutter-speed notation, sensible units and precision,
and descriptive names instead of raw enums. It preserves every source value in
the private record, even when that value does not earn a line in the prompt.

Missing, malformed or contradictory metadata is a stopped boundary until the
agent checks the source bytes and extractor output. A proved source absence may
be recorded explicitly. Silence is not proof of absence.

## Boundary 5: place evidence

Input:

- source coordinate, datum, horizontal accuracy and capture time
- known private place context when configured
- valid cached evidence from the same normalised input

Accepted output:

- address and administrative areas
- OSM-derived trails, parks, landmarks and other map features
- nearby venue and POI candidates with source, relation and distance
- provider, query, cache and coordinate-conversion provenance
- an explicit complete or failed status

Provider code returns candidates, not semantic truth. The photo coordinate says
where the photographer stood; a depicted cathedral, trail or landmark may be
elsewhere. The classification model may infer the depicted place from image and
provider evidence. Geometry alone does not confirm it.

Provider choice and China coordinate handling remain eval-gated.

An empty provider response does not complete this boundary during development.
The agent checks the input, datum conversion, query, radius, categories and an
alternative source before deciding whether the source genuinely has no useful
mapped evidence.

## Boundary 6: complete card input

The model step is ready only when these inputs are complete or explicitly proved
absent:

- normalised asset facts consumed by the card prompt
- camera-original identity and hash
- current rendered still
- full metadata record and human projection
- place evidence for an asset with source location
- album, favourite, hidden and other selected library context

Unknown or unexamined missing evidence blocks carding. It enters an explicit
failure state for investigation rather than becoming an empty prompt field.
Upstream deletion state is stored alongside the card for eligibility and
provenance. It is not rendered into the prompt and does not create a new model
generation.

## Boundary 7: rendered model request

Input: the complete card input and the selected prompt version.

Accepted output: the exact, fully rendered request that will be transmitted,
including:

- Ollama Cloud model identity
- prompt version
- classified image role, media type, size and hash
- readable labelled metadata and place context
- all selection and truncation decisions

The request is persisted in private runtime storage before the call. A person or
agent can read it exactly as sent. The product does not call paid Gemini APIs
directly. Ollama Cloud serves Photos image classification and classification
evals only; it is not the engineering reviewer.

The final model remains eval-gated.

## Boundary 8: raw model response

Input: the persisted rendered request.

Output: the exact Ollama Cloud response text, provider telemetry, timing and
request relation. The raw response is retained before parsing. A retry cannot
overwrite or masquerade as the first attempt.

## Boundary 9: labelled prose and stored card

Input: the retained raw response.

The response uses labelled prose sections rather than a JSON object. The parser
checks declared headings and required content mechanically. It does not judge
meaning, repair unsupported claims or treat syntactic validity as truth.

The card requires a useful summary and long visual description. Important text
must be captured for OCR and search. The exact OCR and composition strategy
remains eval-gated; this contract does not assume a second model call.

Accepted stored output:

- parsed card fields
- retained raw response relation
- model and prompt identity
- source, original and classified-image hashes
- metadata and place-evidence relations
- parser version and write time
- stale and superseded state

Search indexes the stored card and checked mechanical facts. It does not infer a
second answer at query time.

## Provenance and privacy

One private provenance chain must reconstruct a card from:

1. source snapshot and asset selection
2. camera-original and current-rendered-still selection
3. exact byte sizes, types and hashes
4. metadata extraction and human projection
5. coordinate and cached provider evidence
6. fully rendered model request
7. raw model response
8. parser version and stored card rows

Private identifiers, paths, coordinates, metadata, image content and model
output never enter commits, public docs or public test fixtures.

## Failure, restart and staleness

Accepted restart behaviour: every stage records enough input and output identity
to resume without repeating valid work. A restart may reuse a proved cache
entry, provider result, rendered request, raw response or stored card only when
its complete input identity still matches.

Transient failures retry with a bounded policy. Permanent failures retain a
safe reason. No partial output becomes complete state.

When any input consumed by card generation changes, the active card becomes
visibly stale and re-enters the dependency graph. It remains readable. Source
deletion or restoration alone does not stale or regenerate the card. A
successful replacement supersedes rather than deletes the previous card.
Reclassification is batched and deliberate because model calls are expensive.

## Backfill gate

The full-library card run remains parked. It starts only after one real asset
reaches a stored card through this contract, each boundary has raw proof,
focused regressions pass, representative sampling finds no unresolved upstream
integrity defect, and the measured model cost is justified.
