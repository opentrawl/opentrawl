---
written_by: ai
---

# photoscrawl Architecture

Date: 2026-05-28

## Decision

Build `photoscrawl` as a standalone OpenClaw/crawlkit Go crawler. It owns the
Apple Photos schema, local classification policy, privacy policy, and query
surface. `crawlkit` owns reusable mechanics only.

## Source Strategy

Use the safest source available for each job:

1. A crawlkit SQLite snapshot of `database/Photos.sqlite` for headless asset,
   resource, album, and location enumeration.
2. PhotoKit only for explicit media export flows, such as original export when
   the user allows iCloud downloads.
3. Apple's existing Photos analysis as evidence when it is extractable and good.
4. Local Vision/Core ML classification to fill gaps or improve signal.
5. Local multimodal models for higher-signal image understanding when the user
   opts into local content classification.

Treat private Photos SQLite tables as source evidence with schema-version checks
and evidence labels. Do not promote them into durable truth tables.

## Ingestion Model

The crawler has two stages:

- `sync`: enumerate assets and cheap metadata for all assets.
- `classify`: process image/video content through a resumable queue.

`sync` may record paths to files that already exist inside the Photos library
package, such as derivatives, renders, or originals. It must not export media,
write to Photos, or trigger iCloud downloads.

Originals may be downloaded for classification, but downloads must be bounded:

- keep a local cache budget;
- process batches;
- evict originals/thumbnails after observations and hashes are recorded;
- resume from cursor state;
- record `needs_download` when the original is not local.

CPU is allowed. Disk blowups are not.

## Classification Policy

Classify for signal, not uniform checklist compliance.

Always consider:

- scene/object labels;
- OCR;
- face count and boxes;
- barcode/QR detection;
- screenshot/document/receipt markers;
- image quality and visual similarity.
- local photo cards with summaries, visual descriptions, calibrated place
  phrases, and uncertainty notes.

But store observations only when they have useful provenance. Carry confidence
only when the source or model actually emitted a calibrated value. A cat photo
does not need barcode output; a receipt/screenshot/document probably does need
OCR; a drone-looking burst probably needs camera/device/resource metadata and
location precision.

Photo-card output is candidate observation data. It belongs in generic model
observation rows with the model id, prompt version, and internal provenance;
search indexes the raw card text directly. Promotion into trips, places,
people, relationships, or durable events belongs in a later user-reviewed
layer.

## Location Policy

Store raw GPS observations first. Reverse geocoding is a separate derived layer.

Reason: GPS can be off by enough to imply the wrong business or home. A raw
coordinate is source provenance; "barber shop" versus "pizza place" is a
fallible derived claim and must carry an internal method and provenance record.
It carries confidence only when the provider or model actually emits one.

## Identity Policy

Use Apple's People/faces data if available, but label it as source provenance.
Also run local face detection/embedding where useful because user annotations are
sparse and biased toward important people.

Do not create canonical people in v1. Store anonymous face observations, Apple
person labels, and candidate links. Promotion to people belongs in a later
user-reviewed identity layer.

## Query Model

The first query layer is asset traversal:

- `metadata`: crawler contract and capabilities.
- `status`: archive health and counts.
- `doctor`: local readiness checks.
- `sync`: initialise or refresh the archive.
- `classify`: write local metadata, place, and model observations.
- `search`: FTS over asset metadata, albums, place observations, and photo-card
  terms.
- `open`: card-first asset detail with mechanical metadata, place context,
  summary, description, and uncertainty. It does not expose evidence refs.

Human search output may show a derived short ref for local copy and paste.
JSON keeps the canonical `photos:asset/<32-hex>` ref. `open` accepts
either form when the alias resolves to exactly one asset.

Higher concepts like trips, recurring places, drone flights, or named places are
later hypotheses built from archive facts such as album ids, burst ids,
timestamps, and GPS.
