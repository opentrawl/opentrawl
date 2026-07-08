---
written_by: ai
---

# Photo card v3 proposal

## Recommendation

Build v3 as a two-layer card.

The first layer is mechanical. It prints facts that come from Apple Photos,
local metadata, or checked provider enrichment. The model never creates these
facts.

The second layer is prose. The model describes what is visible, uses metadata
as context, names uncertainty, and never becomes the source for time, GPS,
address, venue, camera, album, or file facts.

Remove evidence from `open` human output. Evidence is a separate command, not a
card section.

This is the simplest shape that fixes the current failure:

- raw GPS becomes visible and cannot be hidden by model prose
- cached place context can name a venue when the provider evidence is strong
- the model gets useful context without being asked to recite metadata
- provenance stays available without wasting the human surface

The main tradeoff is that the card becomes less literary. That is the right
tradeoff. `open` is a product surface, not a model-writing demo. If a fact is
deterministic, print it as a fact. If it is interpretive, keep it in prose.

What would change this recommendation:

- if users reject raw coordinates in default `open`, keep them in
  `open --json` and add a compact "GPS recorded" human line instead
- if POI candidates keep causing false venue claims, demote venue names to JSON
  and search only until the place resolver improves
- if Apple provider throttling makes full-library enrichment impractical, keep
  cache-only classification and move provider fills to a separate slow backfill
  command

## 1. The card

`open` human mode should render one card with 2 visibly separate blocks:

```text
photos:asset/syn-2024-0001

Captured: 14 May 2024, 7.42pm local (America/Los_Angeles)
Media: photo, 4032 x 3024
GPS: 10.12345, 20.54321, +/-8m
Address: 10 Example Street, Example City, Example Region, Example Country
Venue: Example Taqueria, candidate, 12m from GPS
Camera: Apple iPhone 15 Pro, 24mm equiv, f/1.8, 1/120s, ISO 64
Albums: City trip, Food ideas
Original: IMG_1042.HEIC, on this Mac, 2.8 MB
Flags: favourite, visible, single shot

Summary: tacos and salsa on a restaurant table.

Description: The photo shows a tray of tacos, salsa cups, lime wedges, and a
paper menu on a small restaurant table. The visible food, table setting, and
menu make this look like a casual taqueria meal rather than a home kitchen
scene. The nearby place context supports Example Taqueria as the candidate
venue, but the image itself does not show the storefront.

Uncertainty: the venue is a provider candidate, not confirmed by visible
storefront text.
```

If a value is missing, omit that line. Do not print placeholder rows such as
`Address: unknown` unless the absence itself explains the result. `GPS` should
say `GPS: not recorded` only when location is the main point of the card or the
user asked for diagnostics.

### Mechanical block

The mechanical block is deterministic. It may use Apple Photos rows, ImageIO
metadata, or cached place-context provider output. It must not use model output.

Fields shown in human mode:

| line | source | display rule |
|---|---|---|
| `Captured` | `ZASSET.ZDATECREATED`, `ZADDITIONALASSETATTRIBUTES.ZTIMEZONENAME` | precompute local time before prompting; never show raw UTC in the card |
| `Media` | `ZASSET`, resource rows | show kind, dimensions, and duration for video or live-photo-like assets |
| `GPS` | `ZASSET.ZLATITUDE`, `ZASSET.ZLONGITUDE`, `ZADDITIONALASSETATTRIBUTES.ZGPSHORIZONTALACCURACY` | show the fix rounded to 5 decimals and accuracy rounded to whole metres in text and JSON |
| `Address` | cached place-context reverse geocode | show one formatted line when cache has a valid address |
| `Known place` | user-seeded `known_place` rows | show `At home`, `At home at the time (<name>)`, or `At work (<name>)`; suppress venue display at that location |
| `Venue` | cached place-context POI candidate | show only when the candidate passes the venue threshold below |
| `Camera` | `ZEXTENDEDATTRIBUTES`, ImageIO fallback | show only the essentials listed below |
| `Albums` | `ZGENERICALBUM` membership | show up to 3 album titles, then `and N more`; JSON keeps all |
| `Original` | `ZINTERNALRESOURCE`, local media index | show original filename, availability, and size; never show local path |
| `Flags` | `ZASSET` and additional attributes | show favourite, hidden/visible, burst/single shot |

EXIF fields that earn a human line:

| field | reason |
|---|---|
| camera make and model | tells the user which device captured the image |
| lens model | useful for camera, drone, and interchangeable-lens photos |
| focal length, with 35mm equivalent preferred | useful and compact |
| aperture | useful for technical photo reads |
| shutter speed or exposure time | useful for motion, night, drone, and blur cases |
| ISO | useful for quality and low-light context |
| flash fired | show only when true |

EXIF fields that stay JSON-only:

| field family | reason |
|---|---|
| metering mode, white balance, exposure bias | useful for diagnostics but too noisy for default `open` |
| digital zoom, orientation, timezone offset | useful internally but rarely helpful as prose |
| raw ImageIO dictionaries | too large and not stable as a human contract |
| camera processing state, HDR, depth, spatial, capture reason | useful for later filters, not default card facts |
| codec, bitrate, sample rate, frame rate | show only for video when it explains the asset |

The SQLite provider extracts time, kind, dimensions, favourite, hidden, burst
id, raw GPS, GPS accuracy, UTI, filenames, resources, availability, albums, and
the camera and lens fields above.

Photos.sqlite exposes more candidate tables than the provider currently uses.
The first useful additions are:

- `ZEXTENDEDATTRIBUTES` for camera make, camera model, lens model, focal length,
  aperture, shutter speed, and ISO
- `ZMEDIAANALYSISASSETATTRIBUTES` for face count and analysis versions
- `ZDETECTEDFACE` and `ZPERSON` for source face and person evidence
- `ZKEYWORD` and keyword joins for user-applied keywords
- `ZSCENECLASSIFICATION` for Apple scene labels and confidence
- `ZASSETDESCRIPTION` for user-entered long descriptions

Do not ingest all of these at once. v3 needs camera fields first, then
place-context, then faces/OCR/scene work behind separate eval gates.

### Model block

The model block is prose:

- `Summary`: one compact sentence
- `Description`: rich visual description, including important visible text
- `Uncertainty`: only the uncertainties that affect interpretation

The model may mention place context inside the description. It must not replace
or contradict the mechanical GPS, address, or venue lines.

Do not show a separate model `Location` line. That is how model prose starts to
look like a fact table.

Keep OCR as a model output section for JSON, search, and eval. In human mode,
the description should include important readable text when text is central to
the image. A separate `OCR` block belongs in a later verbose mode, not the
default card.

### JSON mirror

`open --json` should mirror the 2-layer card. It may include JSON-only detail,
but it must not reintroduce raw provenance as the main shape.

```json
{
  "schema_version": 3,
  "ref": "photos:asset/syn-2024-0001",
  "mechanical": {
    "captured": {
      "local": "2024-05-14T19:42:18-07:00",
      "timezone": "America/Los_Angeles"
    },
    "media": {
      "kind": "photo",
      "width": 4032,
      "height": 3024,
      "duration_seconds": 0
    },
    "gps": {
      "latitude": 10.12345,
      "longitude": 20.54321,
      "horizontal_accuracy_meters": 8
    },
    "address": "10 Example Street, Example City, Example Region, Example Country",
    "venue": {
      "name": "Example Taqueria",
      "category": "restaurant",
      "tier": "venue_candidate",
      "distance_meters": 12
    },
    "venue_candidates": [
      {
        "name": "Example Taqueria",
        "category": "restaurant",
        "tier": "venue_candidate",
        "distance_meters": 12,
        "verdict": "plausible"
      }
    ],
    "camera": {
      "display": "Apple iPhone 15 Pro, 24mm equiv, f/1.8, 1/120s, ISO 64",
      "make": "Apple",
      "model": "iPhone 15 Pro",
      "lens_model": "back camera",
      "focal_length_mm": 6.86,
      "focal_length_35mm": 24,
      "aperture": 1.8,
      "shutter_speed": "1/120s",
      "iso": 64,
      "json_only": {
        "metering_mode": "pattern",
        "white_balance": "auto",
        "exposure_bias": 0
      }
    },
    "albums": [
      {"title": "City trip", "kind": "user"},
      {"title": "Food ideas", "kind": "user"}
    ],
    "original": {
      "filename": "IMG_1042.HEIC",
      "availability": "local",
      "bytes": 2936012
    },
    "flags": {
      "favorite": true,
      "hidden": false,
      "burst": false
    }
  },
  "model": {
    "prompt_version": "photo-card-v3.1",
    "model_id": "gemini-flash-latest",
    "summary": "tacos and salsa on a restaurant table.",
    "description": "The photo shows a tray of tacos, salsa cups, lime wedges, and a paper menu on a small restaurant table.",
    "ocr_text": "menu text visible but not fully legible",
    "uncertainties": [
      "the venue is a provider candidate, not confirmed by visible storefront text"
    ]
  }
}
```

### Evidence

Evidence is gone from human `open`.

Do not print:

```text
Evidence: 12 records - photoscrawl evidence photos:asset/...
```

That line is not useful. A human cannot act on "12 records" without opening a
second command anyway.

Recommendation:

- `open` human mode shows the card only
- `open --json` mirrors the card and omits source and evidence provenance
- `evidence <ref>` owns provenance, raw source pointers, provider payloads, and
  model request/response refs
- a future `open --json --depth evidence` may embed evidence refs if agents
  prove they need one round trip, but v3 should not start there

This keeps the default output readable and keeps provenance available for users
who ask why a field exists.

## 2. Orthogonality table

Rule: deterministic facts never come from the model. Model prose never
masquerades as fact.

| fact family | source | stored where | card | search | JSON | model role |
|---|---|---|---|---|---|---|
| time | Apple mechanical | `asset.creation_date`, timezone fields | show local capture time | filter and result time | local time and zone name, no source field | context only |
| GPS | Apple mechanical | `location_observation` | show rounded fix and accuracy | fallback `where` only | rounded coordinate and whole-metre accuracy, no source field | context only |
| address | provider enrichment | place-context cache and derived `place_observation` | show formatted address if cached | searchable area/address fields | full address components | context only |
| known place | user-seeded archive row | `known_place` and derived `place_observation` | show the known-place label and suppress venue display | `home`, `<name> (home at the time)`, or `work — <name>` | `{kind, name}` under `mechanical.known_place` | hint only |
| POI/venue | provider enrichment | place-context cache and `place_observation` candidate rows | show only tiered candidate or confirmed venue | searchable only when tier passes | up to 5 candidates with plain category, tier, whole-metre distance, and verdict | may mention with calibrated wording |
| camera | Apple mechanical and ImageIO fallback | asset metadata JSON plus typed capture fields | show compact camera line | searchable device/lens only | full capture metadata | context only |
| faces/people | Apple mechanical or local Vision | `face_observation` | not in v3 card by default | `who` and person search when labels exist | face count, labels, boxes if present | must not identify people |
| albums | Apple mechanical | `album_membership` | show capped title list | searchable | full list | context only |
| filenames | Apple mechanical | `asset_resource`, `asset.metadata_json` | show original filename only | searchable | filename, UTI, availability | withheld from prompt by default |
| OCR text | local OCR or model OCR | `text_observation` or model OCR field | description includes important text | searchable text | full OCR text and source | extract text, mark uncertainty |
| objects/scene | local Vision, Apple scene labels, model prose | observation rows and card terms | prose only | searchable terms | observation rows | describe visible evidence |
| uncertainty | model | model observation rows | show as `Uncertainty` | not searchable by default | uncertainty list | required output |

`where` in search should come from this order:

1. known place label
2. confirmed or high-tier provider place
3. formatted address or area
4. raw GPS label
5. empty

It should not come from model prose.

## 3. POI and place pipeline

Classify should use place-context enrichment through a cache-first sidecar. It
should not run one provider request per model worker.

Recommended pipeline:

1. `sync` stores raw GPS and accuracy from Photos.
2. A place preflight reads GPS rows and computes cache keys from latitude,
   longitude, horizontal accuracy, and radius.
3. Existing cache hits are attached to the classify sidecar.
4. Missing keys are filled by a single resumable place-context worker or
   backfill command, not by model workers.
5. If the cache is still missing, classify continues with raw GPS only.
6. The model sees `place_context.cache_status: "missing"` and gets no venue
   candidates.

The existing place-context cache already dedupes exact provider keys. That is
the right primitive. Add a preflight report that prints private local counts for:

- GPS rows
- exact provider keys
- rounded coordinate keys at 5, 4, and 3 decimal places
- cache hits
- cache misses

Do not copy those private counts into tracked docs. A read-only local check on
the current archive confirmed that rounded-coordinate dedupe materially reduces
the number of provider calls. The exact counts stay private because this repo is
public.

### Apple geocoder limits

Apple documents `CLGeocoder` as rate-limited per app and advises that typical
use should not send more than one geocoding request per minute. Apple does not
publish stable numeric limits. Apple staff have also said MapKit reverse
geocoding is rate-limited, details can change, and clients should defer after
throttle errors such as `MKError.loadingThrottled`.

Design consequence: never make 44k classification depend on uncached Apple
requests in the hot path.

Use this policy:

- cache hits are free and can feed classify immediately
- cache misses are normal, not model failures
- provider fills run through one throttled queue with retry and resume state
- throttle errors pause provider work and do not fail already classifiable
  assets
- no POI is a result, not an error

References:

- [Apple CLGeocoder documentation](https://developer.apple.com/documentation/corelocation/clgeocoder)
- [Apple developer forum answer on rate limits](https://developer.apple.com/forums/thread/786947)
- [Apple developer forum answer on MapKit reverse geocoding throttle behaviour](https://developer.apple.com/forums/thread/795522)

### Venue threshold

The card may name a venue only when provider evidence earns it.

Use these tiers:

| tier | rule | card wording |
|---|---|---|
| `confirmed_venue` | provider POI is inside the GPS accuracy circle and visible text or scene evidence supports the same name or type | `Venue: Example Taqueria` |
| `venue_candidate` | provider POI is within `max(25m, accuracy)` and there is no equally close competing candidate of a different type | `Venue: Example Taqueria, candidate, 12m from GPS` |
| `nearby_poi` | provider POI is within the search radius but outside the candidate threshold | JSON and prompt context only; do not show as venue |
| `area_context` | address or area only | show address or area, no venue |

The model may use the same tiers:

- `confirmed_venue`: may say the photo is at the venue
- `venue_candidate`: may say at or near the venue, or candidate venue
- `nearby_poi`: may say nearby only if it helps interpret the image
- `area_context`: may name the area, not a venue

The fridge-brand incident becomes impossible under this rule. A visible brand,
label, appliance logo, menu supplier, or product package is not a venue. It can
be described as visible text. It cannot become a place claim unless provider
place context independently supplies a matching venue candidate that passes the
threshold.

## 4. Model input spec

The classifier should send one structured sidecar per photo.

The sidecar should include:

```json
{
  "schema_version": 3,
  "capture": {
    "local_time": "2024-05-14T19:42:18-07:00",
    "timezone": "America/Los_Angeles",
    "source": "apple_photos"
  },
  "media": {
    "kind": "photo",
    "subtypes": ["still"],
    "width": 4032,
    "height": 3024,
    "duration_seconds": 0
  },
  "location": {
    "gps": {
      "latitude": 10.12345,
      "longitude": 20.54321,
      "horizontal_accuracy_meters": 8
    },
    "place_context": {
      "cache_status": "hit",
      "address_line": "10 Example Street, Example City, Example Region, Example Country",
      "area": ["Example Country", "Example Region", "Example City"],
      "poi_status": "found",
      "venue_candidates": [
        {
          "name": "Example Taqueria",
          "category": "restaurant",
          "distance_meters": 12,
          "tier": "venue_candidate"
        }
      ]
    }
  },
  "camera": {
    "display": "Apple iPhone 15 Pro, 24mm equiv, f/1.8, 1/120s, ISO 64",
    "make": "Apple",
    "model": "iPhone 15 Pro",
    "lens_model": "back camera",
    "focal_length_mm": 6.86,
    "focal_length_35mm": 24,
    "aperture": 1.8,
    "shutter_speed": "1/120s",
    "iso": 64
  },
  "library_context": {
    "albums": ["City trip", "Food ideas"],
    "favorite": true,
    "hidden": false,
    "burst_member": false,
    "original": {
      "uti": "public.heic",
      "availability": "local",
      "bytes": 2936012
    }
  }
}
```

When a known place matches, the sidecar adds
`location.known_place: {"kind": "home", "name": "Example Residence"}`. Provider
POI candidates are not sent to the model for that asset. The address line stays.

The sidecar should deliberately withhold:

- local filesystem paths
- Photos UUIDs, archive row ids, source primary keys, stable hashes
- original filename, unless a later eval proves filenames help more than they
  leak or get echoed
- raw provider payloads and provider provenance arrays
- raw EXIF dictionaries
- prior model output
- evidence refs

Album names stay in the sidecar because they often carry user intent. They are
private, so remote model use must remain explicit opt-in.

Use local capture time, not raw UTC. The May evals already showed that raw UTC
causes bad reasoning and metadata echo.

### Model calls

Use one prompt and one model call per photo for v3.

Do not add a second call yet. The v3 prompt should make the first call more
complete by giving it better metadata and stronger rules.

A second OCR-focused call is worth testing only if both conditions hold:

- local Vision OCR or model section output says the image is text-dense,
  document-like, a receipt, a ticket, a menu, or a screenshot
- v3 eval shows the main call misses important readable text on that class

If added later, the second call should return only OCR text and field structure.
It should not write another summary or place description.

## 5. Prompt v3 draft

Do not apply this in `prompts/photo-card-v2.md` yet. Use it as the v3 candidate
after the sidecar contract exists.

```markdown
---
written_by: ai
prompt_version: photo-card-v3.1
change_rationale: "Expose rounded camera context and require one-clause uncertainty bullets."
---

# photoscrawl photo card prompt v3

Create a personal photo-library card for the supplied image.

Use the image as primary evidence. Use the metadata context to understand the
image, not to echo a data dump. Mechanical facts such as time, GPS, address,
venue candidates, camera, albums, and original availability are printed by the
app outside your response.

Return only these sections:

## Summary

Write one sentence, 10 to 24 words. Say the main thing visible in the image.
Use visible text when it changes the meaning.

## Description

Write 250 to 450 words by default. Use less only for simple images and more
only for dense documents, screenshots, technical scenes, food, interiors,
travel scenes, or objects with important details.

Describe the visible evidence completely enough that a person can recognise why
the photo matters. Include people only as visible roles or counts unless the
metadata gives an explicit source label. Do not identify a person from the
image alone.

Use place context carefully:

- if `confirmed_venue` is present, you may say the photo is at that venue
- if `venue_candidate` is present, say candidate, likely, or at or near
- if only `nearby_poi` is present, do not turn it into the photo location
- if only address or area exists, name the area, street, city, region, or
  country as context
- if the image contradicts the place context, trust the image and say why

Do not treat visible brands, appliance labels, product packaging, menu supplier
names, or screen text as venue names unless the place context independently
supports the same venue.

Err toward complete visible detail. Mention important objects, documents,
screens, signs, food, tools, vehicles, route markers, camera/drone context,
weather, indoor/outdoor setting, and interactions. Do not write generic captions
such as "a photo of food" when the image contains more.

## OCR and machine-readable text

Transcribe readable text, document fields, barcodes, QR labels, ticket numbers,
flight or train numbers, seat, gate, route, prices, totals, dates, times, menu
items, signs, labels, and screen text as completely as the image allows.

Group text by source when there are multiple objects. Preserve non-English text
where legible. Mark uncertain characters with `?`.

If there is no useful readable text, write `none`.

## Uncertainty

Write only the uncertainties that affect interpretation. Include uncertain
venue, document, OCR, object, event, or scene claims. Do not pad this section.
Use one bullet per uncertainty. Each bullet must be one short clause, not a
sentence pair. Do not restate conclusions already made in the description.
If there are no meaningful uncertainties, write `none`.

Do not quote or list local paths, Photos ids, archive ids, raw GPS coordinates,
raw EXIF blocks, raw metadata JSON, hashes, or filenames.

## Metadata context

Use this context for reasoning only:

{{.MetadataJSON}}
```

The parser should treat `Summary`, `Description`, `OCR and machine-readable
text`, and `Uncertainty` as the v3 sections. There is no model `Location`
section in v3.

## 6. Migration and eval plan

Implement v3 as a break, not a shim. This repo is pre-1.0 and the archive can
re-derive.

### Re-derive

Re-derive these rows:

- asset metadata after the SQLite provider adds `ZEXTENDEDATTRIBUTES`
- place observations after place-context cache/backfill is wired to classify
- model observations for `photo-card-v3.1`
- hidden search terms from the v3 card
- `open` JSON and text snapshots in tests

Do not migrate v2 model rows into v3. Delete or ignore old prompt-version rows
when refreshing the model.

### Measure v3 against v2

Use the existing rubric in `docs/evals/photo-card-protocol.md`, with the same
private sample shape:

- 15 latest original-input assets for continuity
- the top 2 current models first
- no private images, filenames, asset ids, locations, OCR, or model responses in
  tracked docs
- aggregate scores only in `docs/evals/runs.md`

Add targeted checks for this proposal:

| check | pass condition |
|---|---|
| evidence removed | `open` human mode has no evidence line |
| GPS mechanical | GPS and accuracy appear when present |
| address mechanical | cached address appears without model involvement |
| venue threshold | high-tier provider venue appears; weak POIs do not |
| model contradiction | model prose never contradicts GPS/address/venue lines |
| metadata echo | model does not quote raw EXIF, filenames, ids, paths, or JSON |
| OCR completeness | document-like images keep important readable text |
| search orthogonality | `where` comes from provider/address/GPS, not model prose |

The v3 run wins only if it beats v2 on location and format without losing visual
detail or OCR. A smaller mean score gain is not enough if venue hallucinations
remain.

### What the 44k run waits on

Do not start the 44k model run until these gates pass:

- SQLite provider imports the EXIF essentials needed for the mechanical camera
  line
- place-context cache lookup is available to classify sidecars
- provider fills are resumable, cache-first, and throttled outside model workers
- `open` human and JSON fixtures prove the 2-layer shape
- v3 parser and prompt pass synthetic format tests
- a 15-asset original-input eval beats or matches v2 on the new checks
- the run can continue when place context is missing

If place cache coverage is low, the run may still proceed, but cards without
cache hits must not name venues. They should show raw GPS and model prose only.
