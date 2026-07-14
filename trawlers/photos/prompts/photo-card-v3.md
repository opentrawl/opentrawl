---
written_by: ai
prompt_version: photo-card-v4.0
change_rationale: "Read complete CardInput ProtoJSON and submit one typed photo card."
---

# photoscrawl photo card prompt v4

Create a personal photo-library card for the supplied image.

Summary and description must describe visible image evidence only. Use metadata
to understand the image, not to add unseen place, entrance, ecology, region or
other scene claims. Mechanical facts such as time, GPS, address, provider place
candidates, camera, albums, flags, and original availability are printed by the
app outside your response.

Submit one complete card through the supplied function. Do not write a Markdown
card or repeat the field names in assistant text.

Write one sentence, 10 to 24 words. Say the main thing visible in the image. Use
visible text when it changes the meaning.

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

Place candidates are only evidence for the separate venue plausibility result.
Never turn a merely nearby candidate into a visual claim. A visible brand, menu,
label or screen is not a venue name unless it supports the same listed
candidate. For venue plausibility, choose one provider place candidate or
`none`. Use
`corroborated`, `plausible`, `inconsistent` or `none` as the verdict. Give one
short reason.

Use `corroborated` only when visible evidence supports the same venue name,
storefront, sign, logo, entrance, menu, receipt, or unmistakable venue type. Use
`plausible` when a candidate is near the GPS point and nothing visible
contradicts that place type. Use `inconsistent` when the scene contradicts the
place type, for example a private home meal near a registered business. Use
`none` when no listed candidate is a useful venue interpretation.

Do not use `corroborated` for a merely nearby provider candidate. Do not use
`plausible` when the image itself points to a private, residential, vehicle,
outdoor, office, hotel-room, or other non-matching setting.

Transcribe readable text, document fields, barcodes, QR labels, ticket numbers,
flight or train numbers, seat, gate, route, prices, totals, dates, times, menu
items, signs, labels, and screen text as completely as the image allows.

Group text by source when there are multiple objects. Preserve non-English text
where legible. Mark uncertain characters with `?`.

If there is no useful readable text, submit an empty OCR field.

Write only the uncertainties that affect interpretation. Include uncertain
venue, document, OCR, object, event, or scene claims. Do not pad this section.
Use one short clause per uncertainty. Do not restate conclusions already made
in the description. Submit an empty list when there are no meaningful
uncertainties.

Do not quote or list local paths, Photos ids, archive ids, raw GPS coordinates,
raw EXIF blocks, raw metadata JSON, hashes, or filenames.

Use this complete CardInput ProtoJSON context for reasoning only:

{{.MetadataJSON}}
