---
written_by: ai
---

# Place evidence evaluation

This protocol tests the deterministic location-evidence boundary for photo
cards. It does not choose a provider and it does not run the card model.

The only current-main implementation inventory lives in the
[data contract](../data-contract.md). Place implementation and decisions are
tracked in live [TRAWL-171](https://linear.app/joshpalmer/issue/TRAWL-171).
This protocol does not mirror its work state.

## Accepted evaluation boundary

The target location tool accepts one asset's source coordinate, accuracy and
capture time. The evaluation expects checked provider evidence only:

- address and administrative areas
- mapped trails, parks, landmarks, roads and natural features
- nearby venues and other POIs
- source, relation, distance and coordinate-system provenance
- cache and completion state

The tool does not classify the image or decide which candidate is depicted.
That judgement belongs to the later card model, which can compare visual
evidence with all useful candidates. The camera coordinate says where the
photographer stood; a cathedral across water or a distant trail is not
necessarily centred on that point.

Provider endpoints and credentials come from explicit application
configuration. No library package chooses or hardcodes an external provider.

## Cache and restart contract

The accepted provider path is cache-first and resumable. A cache key includes
every input that can change the result, including coordinate datum, accuracy,
radius, query shape and provider version. A cache hit must round-trip through
the same validation as a live result.

Different coordinate keys may resolve in parallel within provider limits.
Classification for other ready assets may continue while place evidence fills.
The card call for a located asset waits until its own place boundary is complete.

## A missing result is an investigation

During development, an empty provider response does not count as a successful
place input. Before recording a genuine absence, inspect:

- the exact source coordinate, datum, accuracy and capture time
- any mainland-China coordinate conversion
- the radius, category filters and query shape
- the raw reverse-geocode, map-feature and POI responses
- at least one suitable alternative OSM-backed source

If those checks show a pipeline or provider gap, fix or retry it. Do not card the
asset without the evidence and do not ask the card model to hide the missing
stage. A proved source absence can later be stored explicitly with its checks.

## Decision sample

Provider evaluation uses both targeted hard cases and naïve random sampling.
The targeted set must include:

- a venue whose correct candidate competes with nearby businesses
- a park or trail where generic infrastructure competes with the landmark
- a depicted place offset from the camera coordinate
- mainland-China coordinates
- dense urban and sparse rural coordinates
- an apparent no-result case

For every case, read the exact input and raw provider output. Record cache and
restart behaviour. Do not accept a score, candidate count or plausible rendered
summary instead of reading the candidates themselves.

## Provider decision

No provider is selected by this document. Apple, Geoapify and other free or
low-cost OSM-backed sources are candidates. A provider wins only when current
raw outputs cover the representative corpus well enough for the card input and
its terms allow the required caching and backfill.

Mainland China does not automatically require a separate provider. Test the OSM
path with the correct coordinate handling first. Add another provider only if
that evidence shows a real independent gap.

## Privacy

Raw coordinates, provider payloads and location names from a real library stay
in private runtime or eval storage. Public docs contain only the protocol and
synthetic examples.
