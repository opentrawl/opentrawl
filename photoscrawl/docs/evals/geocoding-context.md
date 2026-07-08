---
written_by: ai
---

# Geocoding context for photo cards

Raw GPS alone is not enough for good photo cards. The model needs a small,
human-readable place context that is useful without pretending the exact POI is
known.

## Contract

Before prompting, run one asset at a time through `place-context`:

```sh
photoscrawl-lab place-context --input <private-eval-run>/metadata/E001.json --json
```

To render already-cached evidence without another provider call:

```sh
photoscrawl-lab place-context --input ~/.opentrawl/photos/cache/place-context/<key>.json
```

The command reads the eval metadata JSON shape, uses the asset's own
latitude/longitude/accuracy/time, and emits provider evidence plus a compact
deterministic Markdown card. Reverse-geocoded address evidence is required. POI
search is optional evidence: no nearby POI is a normal result, not a provider
failure.

For library-scale cache coverage, use classify's cache-first resolver rather
than a separate backfill command. It fills missing place keys as classification
needs them and can read legacy backfill artifacts.

Provider evidence includes:

- `area`: coarse trail, such as `Country -> City -> district/area`;
- `map_features`: mapped trails, roads, natural features, landmarks, areas, or
  other deterministic map context when supplied by a checked provider;
- `poi_status`: `found`, `none`, or `provider_error`;
- `poi_candidates`: ranked candidate venues, landmarks, terminals,
  addresses, or POIs, each with source, relation, distance, and provenance;

A separate place-resolution layer should turn provider records plus known
private context into a tiny prompt shape. That layer can suppress residential
POI noise near known homes, prefer a time-bounded hotel stay, and correlate work
or travel context. Provider responses and evidence refs are internal
provenance, not a public query surface.

The prompt should use this context to understand the image, not to overwrite
visual evidence. A nearby boarding pass can make an airport-lounge snack image
understandable. A no-drone sign mentioning an airport control zone should not
move a city temple photo to the airport.

## Precision

Cards should expose both generic and specific context:

```text
Area: Country -> City -> airport/rail hub area
Specific candidate: likely terminal complex, exact terminal unresolved
```

Do not put raw coordinates in prose. Store them as private evidence in the local
archive. Future place resolution can use Apple/CoreLocation, Amap/Gaode, Google
Places, Foursquare, or another checked provider, but each candidate needs
source, distance/accuracy, and deterministic status. Do not invent confidence
labels from distance.

## Provider notes

The first repo implementation uses Apple's native CoreLocation reverse geocoder
plus MapKit nearby point-of-interest search. It is network-backed, cached under
`~/.opentrawl/photos/cache/place-context`, and good enough to test the
command boundary without API keys. `MKErrorPlacemarkNotFound` from MapKit POI
search is stored as `poi_status: "none"` because Apple found an address but no
named POI inside the requested radius.

Provider choice should be evidence-driven:

- Apple is the default first slice for macOS Photos because it is native and
  returns addresses plus nearby POIs. It does not provide a reliable
  coordinate-to-map-feature API on macOS; `CLPlacemark.areasOfInterest` is
  useful area context, not nearby POI evidence.
- Geoapify is the first OSM-derived map-context candidate outside China because
  it has HTTP reverse-geocoding and Places APIs, allows cached/stored results,
  and publishes a free plan with 3,000 credits/day, no credit card, and 5
  requests/second as of 2026-06-01. A private golden-sample probe over 53
  photo-derived inputs returned successful reverse geocodes for all 53 records,
  but Places returned zero candidates for 40 records, including 26 of 31 China
  records. Use Geoapify for OSM reverse/map context, not as the sole POI
  provider. Public Nominatim is for probes only, not product bulk backfills.
- Amap/Gaode is the China path because its reverse geocoder can return address
  components, nearby POIs, AOIs, roads, and building/community context with
  `extensions=all`. It is not keyless/free-assumed: official docs require a Web
  Service API key, QPS is console-managed, and base-service usage is
  quota/billing governed after monthly allowance. A live browser attempt on
  2026-06-01 reached the developer registration form and could not create a key:
  registration requires a phone number, slider challenge, SMS code, and
  password before the console exposes application/key creation.
- Google Places is the likely global quality ceiling, but it is billable and
  field-mask pricing matters.
- Mapbox is another cheap/free-tier HTTP comparator; use Search Box for POIs
  because Mapbox Geocoding v6 no longer returns POI data and storage terms need
  care.

Do not add a provider selector to the classifier prompt path. Add providers only
behind this same JSON output contract after real provider outputs show the need.

## China provider status

Amap/Gaode is parked. It is blocked on human-gated developer registration and is
queued for maintainer action. When a key exists, the provider should sit behind
the same place-evidence seam as Apple. Until then, China-relevant work belongs
in rural and non-POI prompt phrasing, not provider integration.
