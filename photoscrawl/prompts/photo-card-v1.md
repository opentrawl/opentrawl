# photoscrawl Photo Card Prompt v1

Create a personal photo-library card for the supplied image.

Use the image as primary evidence. Also use visible text, the metadata JSON, and
any resolved place context if present to understand what is happening. Better
analysis is the goal: identify the important scene, activity, document, place,
signage, food, screen, route, object, or event instead of writing a generic
caption.

Return prose with these sections:

## One-line summary

Write 10-24 words. Say the main thing happening in the image. Use visible text
or strong metadata context when it makes the summary more accurate.

## Detailed description

Write 250-450 words by default. Use 150-600 words only when the image is much
simpler or denser. Describe what is in the image in concrete visual detail. Do
not explain why the image is useful. Do not add tags, evidence bullets, or a
review-style score.

Include important visible text and document fields in the description when they
are central to the image. For tickets, boarding passes, receipts, signs, menus,
screens, labels, and forms, describe the object and the key readable fields. If
Chinese or other non-English text is visible, preserve the original text where
legible and explain its role.

If visible branding or repeated signage suggests a venue, landmark, merchant, or
event name, state it with confidence wording. Do not turn weak metadata or a
generic sign into a precise place claim.

## Location

Write a human-readable location trail when metadata or place context supports
one, from coarse to specific: country, city/region, district/area, candidate
venue/address/POI. If the specific place is not directly visible or otherwise
well supported, label it as a candidate or likely area. Do not quote raw
latitude/longitude.

## OCR and machine-readable text

Transcribe readable text, document fields, barcodes, QR labels, ticket numbers,
flight/train numbers, seat/gate/route fields, prices, and identifiers as
completely as the image allows. Group text by source when there are multiple
objects. Mark uncertain characters with `?`.

## Uncertainty

Briefly name the important uncertainties only. Do not pad this section.

Do not quote private filenames, Photos UUIDs, local filesystem paths, database
row ids, raw EXIF blocks, or raw metadata dumps. Use metadata to improve the
card; do not echo metadata as a source dump.

## Metadata JSON

Use this metadata to improve the card:

{{.MetadataJSON}}
