---
written_by: ai
---

# Photos v1 architecture

OpenTrawl reads Apple Photos and stores durable facts that later image
classification can trust. The normal product has one idempotent command:
`trawl update photos`.

This document describes the Milestone 2 source, media and location foundation.
It does not define the PhotoCard, OCR or Luna interaction design. Those product
questions start again from real outputs after this foundation is accepted.

Direct Josh steering is product authority. Observed real behaviour is evidence.
Implementation choices are model hypotheses until Josh accepts them.

## Dependency graph

The Photos foundation is a small explicit DAG. Every node has one job, a typed
boundary and a retained outcome. The normal update composer calls these same
operations.

```mermaid
flowchart LR
    source["Index the complete Photos source"]
    current["Acquire the current edited image"]
    original["Inspect immutable original facts"]
    known["Match a configured known place"]
    appleReverse["Acquire Apple camera-location hierarchy"]
    appleNearby["Acquire Apple nearby places"]
    geoapify["Acquire Geoapify nearby places"]
    compose["Compose typed location evidence"]

    source --> current
    source --> original
    source --> known
    source --> appleReverse
    known --> appleNearby
    known --> geoapify
    known --> compose
    appleReverse --> compose
    appleNearby --> compose
    geoapify --> compose
```

`trawl photos debug` lists this registry in dependency order and reads retained
typed state. It never changes the archive or calls a provider.

`trawl photos run NODE [PHOTO]` explicitly runs one production operation. A
missing dependency fails with its exact plain reason. The source node has no
photo argument because it indexes the library. All other nodes use a normal
`photos:…` link.

This is inspection plumbing, not a second product or workflow engine.

## Source

One complete read-only Photos library snapshot supplies assets, resources,
albums, capture facts and source state. PhotoKit does not enumerate a competing
source library.

Only a complete snapshot may mark a previously indexed asset as missing. A
failed or partial read does not invent deletions.

`photos run source` executes only this operation. It does not acquire media or
call Apple or Geoapify location services.

## Current and original media

The current image and immutable original have different jobs:

- The current rendered still contains the user's edits and has its orientation
  applied to the pixels. Future visual judgement uses this image.
- The immutable original supplies original dimensions, type, byte count, digest
  and source facts. It is not silently substituted for the edited image.

Current-media reuse is bound to the Photos asset identifier and modification
time. Original-facts reuse is also bound to the indexed original-resource
identities. A changed original resource therefore invalidates its facts without
invalidating unrelated location evidence.

The installed OpenTrawl app is the only PhotoKit client and macOS permission
identity. The direct `trawl` helper sends a typed local request to the installed
app. Users run the helper as a normal CLI. The `OpenTrawlApp` SwiftUI executable
is a GUI and must be launched as an application; executing it as a CLI causes
an AppKit registration abort.

Media bytes are short-lived working data. A checked lease has a typed identity,
byte count, dimensions, orientation and SHA-256. The consumer verifies the bytes
before storing evidence and releases the lease after durable work completes.

For human inspection only, `photos run current-media PHOTO` atomically replaces
one 0600 JPEG in the normal external archive cache and prints its path. Read-only
debug never writes this file. This is one bounded inspection image, not a
gallery, second cache or photo library.

The current 512 MiB admission limit and 2 GiB free-space floor are model
hypotheses. Real largest-media and active-lease measurements decide whether
they remain. Development media stays on the external SSD.

## Location evidence

Capture location and photographed place are different concepts. Code retains
facts about the camera position and nearby provider results. A later image model
judges what the photograph depicts.

The operations are deliberately separate:

- Known-place matching compares the capture coordinate and capture time with
  configured homes, former homes and work locations.
- Apple reverse geocoding supplies the human geographic hierarchy around the
  camera.
- Apple nearby supplies Apple MapKit place candidates.
- Geoapify supplies complementary named OpenStreetMap place candidates.
- Composition checks that all dependency inputs match and stores one typed
  location-evidence outcome.

A known-place match keeps Apple camera hierarchy but skips Apple nearby and
Geoapify before transmission. The known place does not automatically become the
photographed subject.

New Apple requests identify the exact MapKit operation in their Protobuf
request. Earlier retained reverse rows that did not store their acquisition
method remain visible as legacy Apple evidence; they are not silently relabelled
as MapKit results. Apple evidence records its observation time and attribution.

Provider evidence is keyed by the deterministic typed provider request, not by
one asset. Photos with the same exact request reuse one exact retained response.
Each asset keeps a typed link to the provider outcome it consumed. Composition
preserves provider order and does not turn proximity into a photographed-place
decision.

Every external operation retains:

- the exact typed request;
- transmission state;
- the exact response;
- observed time and provider attribution;
- a parsed typed outcome;
- a typed failure and retry time when the provider fails.

The current Apple radius and candidate limit are 500 metres and 100 candidates.
The current Geoapify experiment uses a 5 km radius, a 20-result limit and eight
provider-native category roots. These are model hypotheses, not Josh decisions.
The 5 km range retained distant towns, parks and landmarks that the 500 m Apple
operation cannot supply. The shorter category request replaced an unapproved
45-entry list. A saturated provider result is bounded evidence, not a complete
list and not a photographed-place conclusion.

The provider operations retain every returned candidate. The composed briefing
currently projects the first 12 candidates from each provider in provider
order. Twelve is a model hypothesis, not a Josh decision. It keeps the briefing
bounded while preserving the raw typed evidence for later inspection or a
different projection. The cap is part of the typed composition request, so a
change recomposes retained evidence without repeating provider calls.

Geoapify Places does not support Geoapify's batch API. One synchronous request
returning no more than 20 results costs one credit. Known-place suppression and
exact typed-request reuse are therefore the only accepted M2 savings. Spatial
reuse remains a hypothesis until it can preserve per-photo distances and useful
candidate coverage on real photos.

Geoapify's free plan currently permits 3,000 requests per day and five request
starts per second. OpenTrawl never selects more assets than the unused request
allowance and leaves the remainder pending for a later update. Request starts
are at least 200 milliseconds apart across all workers. The current rolling
24-hour allowance is a conservative model engineering decision, not a Josh
decision. It avoids depending on an assumed provider reset time and can be
changed after real operating evidence justifies a better rule.

## Concurrency and restart

The update composer owns concurrency. Components do not create worker pools or
competing schedulers.

Across assets, the composer permits a small fixed number of active workers.
Within one asset, current media, immutable original facts and location work are
independent. After known-place matching, Apple reverse, Apple nearby and
Geoapify may overlap. Composition waits for their retained typed outcomes.

An external operation progresses through durable states:

```text
request retained → transmission started → response retained → typed outcome stored
```

A retained response is parsed again rather than transmitted again. An
interrupted or failed operation remains truthful and resumable. Database writes
use short component transactions; there is no library-wide transaction.

## Observability

Each explicit node execution records the node name, acquired/reused/skipped/
deferred/failed outcome and elapsed time in the normal Photos log. Source and
foundation phases also record their elapsed time. Provider
transmission attempts and retry state are durable. Aggregate update progress
reports active work, media leases and completed outcomes without requiring user
maintenance.

Logs are supporting evidence. Acceptance still comes from the direct CLI on
real photos: inspect the exact input, run or reuse one operation, read the full
human output and see the retained result become the next node's dependency.

## M2 acceptance boundary

M2 is accepted only when the exact signed installed product proves:

- complete source indexing and an unchanged replay;
- current edited, rotated, local and iCloud-backed images;
- distinct immutable original facts;
- known-place matching and zero-call nearby suppression;
- useful, attributable Apple and Geoapify provider outcomes;
- a compact location briefing that does not bury useful evidence in provider
  directory spam;
- normal update composition, retry, resume, reuse and quiet external-disk use;
- no new crash class.

One fresh zero-context reviewer judges this major milestone against Josh's
newest steering and the real installed CLI. At most one bounded correction
follows. Then work stops for Josh. No corpus backfill, OCR, Luna or PhotoCard
work starts in M2.
