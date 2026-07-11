---
written_by: ai
---

# Photos architecture

photoscrawl is a read-only Apple Photos crawler. It owns its source schema,
archive, classification pipeline and query surface. `trawlkit` owns shared
crawler, storage, model-call and output mechanics.

This document states the stable structure and accepted direction. It does not
inventory current-main behaviour. The [data contract](data-contract.md) is the
only Photos document that records implementation status.

## Model boundary

Local first means local archives, caches, source access and user control. It
does not mean local image models are preferred.

Photos image classification and image-model evals use frontier vision models
through Ollama Cloud. Direct paid Gemini API calls are not part of the product
path. No current document chooses the final card model.

Ollama Cloud is not used for code review or general agent reasoning. Those jobs
use a capable Sol/OpenAI agent or a human. Terra may be an agent-runtime choice;
it is not a Photos provider.

## Asset-parallel dependency graph

The accepted pipeline is a resumable dependency graph, not one sequential loop:

```mermaid
flowchart LR
    source["Current Photos snapshot"] --> asset["Normalised asset and deletion state"]
    asset --> media["Camera original and current rendered still"]
    media --> metadata["Full metadata and EXIF"]
    asset --> place["Cached map and POI evidence"]
    media --> ready["Complete card input"]
    metadata --> ready
    place --> ready
    ready --> request["Persisted rendered request"]
    request --> model["Ollama Cloud card call"]
    model --> response["Retained raw response"]
    response --> parse["Labelled-prose parse"]
    parse --> card["Stored card and provenance"]
```

Different assets may occupy different stages at the same time. Place evidence
for one asset may be filling while another waits for PhotoKit and a third is
sent to the model. Dependencies remain strict within one asset: the expensive
card call starts only after every required upstream boundary is complete.

## Source and asset boundary

The crawler reads a consistent snapshot of the configured Photos library. It
does not change Photos, its albums, metadata, media or iCloud state.

One normalised asset carries the source identity and facts consumed downstream.
Only a proved-complete snapshot may establish that a previously current asset
is missing. The archive records the explicit source state, the snapshot that
established it, when absence was first observed and any deletion time supplied
by Photos. A verified source absence is distinct from an extractor, provider or
selection failure. Deletion and restoration update source state and provenance;
they do not by themselves change the model-generation key or spend another card
call. A valid existing card remains readable with its upstream-deleted flag.

An asset first observed missing before it has a stored card can never receive
its first paid card. The archive records that fact permanently, including after
restoration. Metadata-only processing may still run once. Existing cards stay
outside this first-card prohibition, including stale or superseded history.
Normal reuse and refresh rules decide whether an old card can be reused.

## Image roles and cache

Apple exposes 2 different image roles:

- `PHAssetResourceType.photo` is the camera original. Its exact bytes, size,
  hash and full metadata are provenance
- [`PHImageManager.requestImageDataAndOrientation`](https://developer.apple.com/documentation/photos/phimagemanager/requestimagedataandorientation%28for%3Aoptions%3Aresulthandler%3A%29)
  with request version [`.current`](https://developer.apple.com/documentation/photos/phimagerequestoptionsversion/current)
  supplies the most recent rendered still, including edits, at the largest
  available size

`PHAssetResourceType.fullSizePhoto` is a modified resource, not the canonical
way to obtain that current rendition and not a better original. The pipeline
acquires and identifies both roles. Classification always sees the current
rendered still; metadata and provenance come from the camera original. For an
unedited asset the 2 representations may be visually equivalent, but the
pipeline does not assume their bytes are identical. The rendered request
records exactly which bytes it sent.

Downloaded media uses a bounded local least-recently-used cache. A valid cache
hit is tied to the complete source version and proves its stored size and
SHA-256 after restart. Partial exports never become cache entries. A request
holding a cached file prevents eviction until it has copied the bytes.

## Metadata boundary

The product extracts the full metadata and EXIF record from the exact camera
original. Private runtime storage keeps the lossless source record. The card
request receives a field-aware, human-readable projection: sensible dates,
standard shutter-speed notation, useful units and no meaningless float
precision.

Unknown, malformed or contradictory values remain explicit. They do not become
plausible-looking prose or silently disappear.

## Place evidence boundary

One configured provider seam combines cached, resumable address, map-feature
and POI evidence from explicit sources. The evidence can cover venues,
landmarks, trails, parks and difficult coordinates, including mainland China.
Provider endpoints and credentials come only from explicit application
configuration.

The camera coordinate is where the photographer stood. It is not necessarily
the depicted place. Provider code returns evidence; the card model may compare
the image with useful candidates to infer what is depicted. Provider distance
alone never confirms a venue or landmark.

An empty provider response is not automatically a completed stage. During
development it blocks carding until the input, coordinate handling, query and
provider coverage have been investigated. A genuine, proved absence may then be
stored explicitly.

## Card boundary

The complete card input joins the selected image with readable mechanical
context. The exact rendered request is persisted before transmission. The exact
raw response is retained before parsing.

The Photos response contract uses labelled prose sections. Deterministic code
checks declared structure without making a semantic judgement or treating valid
syntax as true content. The stored card includes a useful summary, a long visual
description, important visible text and explicit uncertainty.

One private provenance record links the source asset, camera-original hash,
classified-image hash, metadata projection, place evidence, model request, raw
response, parser version and stored card.

## Missing evidence and restart behaviour

An unexamined missing field is a pipeline failure, not permission to card. Each
stage distinguishes:

- verified source absence
- unsupported source shape
- transient failure
- permanent failure with evidence
- complete output

Only a complete output or proved source absence satisfies a dependency. A
provider or extractor returning an empty payload does not prove absence.

Every completed stage is idempotent. It records enough input identity, output
identity and private provenance to reuse valid work after restart. A retry does
not repeat a valid download, provider call or model call. Drift in an input
actually consumed by card generation marks the card stale; successful
reclassification retains the old card as history rather than deleting it.
Source deletion or restoration alone changes eligibility and provenance, not
the generation key.

## Query and research boundaries

`search` is a projection of stored source facts and cards. It does not perform a
second semantic inference pass. `open` presents the current card and readable
mechanical facts without exposing private evidence identifiers.

Research tooling may test providers, prompts and models, but it does not become
a second product pipeline. Reusable acquisition, metadata, place and request
logic belongs in the product path. A lab result proves the product only when it
uses those same boundaries.

## Unresolved decisions

Current documentation must not settle these before evidence exists:

- the final OSM-backed provider and ranking policy
- the production Ollama Cloud vision model
- whether auxiliary OCR evidence improves the single expensive card call
- the composition output shape
- later use of Live Photo motion frames
- a large developer-only external cache and prefetch policy

Historical research may inform these decisions. It never decides them by
incumbency.
