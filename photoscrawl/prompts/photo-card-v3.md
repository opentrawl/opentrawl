---
written_by: ai
prompt_version: photo-card-v3
change_rationale: "Separate deterministic context from model prose and require structured venue plausibility."
---

# photoscrawl photo card prompt v3

Create a personal photo-library card for the supplied image.

Use the image as primary evidence. Use the metadata context to reason about the
image, not to echo a data dump. Mechanical facts such as time, GPS, address,
venue candidates, camera, albums, flags, and original availability are printed
by the app outside your response.

Return only these sections:

## Summary

Write one sentence, 10 to 24 words. Say the main thing visible in the image. Use
visible text when it changes the meaning.

## Description

Write 220 to 420 words by default. Use 120 to 550 words only when the image is
much simpler or denser. Err toward completeness of visible evidence. Richness is
required, especially for documents, screenshots, technical scenes, food,
interiors, travel scenes, or objects with important details.

Describe the visible evidence completely enough that a person can recognise why
the photo matters. Mention important objects, documents, screens, signs, food,
tools, vehicles, route markers, camera or drone context, weather,
indoor/outdoor setting, and interactions. Include people only as visible roles
or counts unless the metadata gives an explicit source label. Do not identify a
person from the image alone.

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

## Venue plausibility

Answer for the top provider venue candidate only. Use exactly these fields:

- `candidate`: the provider venue candidate name, or `none`
- `verdict`: `corroborated`, `plausible`, or `inconsistent`
- `reason`: one short sentence

Use `corroborated` only when visible evidence supports the same venue name,
storefront, sign, logo, entrance, menu, receipt, or unmistakable venue type. Use
`plausible` when the candidate is near the GPS point and nothing visible
contradicts that venue type. Use `inconsistent` when the scene contradicts the
venue type, for example a private home meal near a registered business.

Do not use `corroborated` for a merely nearby provider candidate. Do not use
`plausible` when the image itself points to a private, residential, vehicle,
outdoor, office, hotel-room, or other non-matching setting.

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

Do not quote or list local paths, Photos ids, archive ids, raw GPS coordinates,
raw EXIF blocks, raw metadata JSON, hashes, or filenames.

## Metadata context

Use this context for reasoning only:

{{.MetadataJSON}}
