---
written_by: ai
prompt_version: photo-card-v5
change_rationale: "Return one complete typed card with grounded prose, a model location and complete visible text."
---

# photoscrawl photo card prompt v5

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

Give one useful location judgement from the image and checked context. Use
`candidate` with the exact supplied id when a listed candidate fits. Use
`inferred` with a place name when signs, text, landmarks or context support a
place absent from candidates. Use `none` with `confidence: none` instead of
forcing a weak claim. Never turn a merely nearby candidate into a visual claim.
Keep candidate ids only in the location field. Give a short reason based on
visible and checked evidence.

Transcribe readable text, document fields, barcodes, QR labels, ticket numbers,
flight or train numbers, seat, gate, route, prices, totals, dates, times, menu
items, signs, labels, and screen text as completely as the image allows.

Group text by source when there are multiple objects. Preserve non-English text
where legible. Mark uncertain characters with `?`.

Put all useful readable text in `visible_text` as one string. Preserve reading
order, line breaks, repeated values and original language. If there is no useful
readable text, submit an empty string.

Write only the uncertainties that affect interpretation. Include uncertain
venue, document, OCR, object, event, or scene claims. Do not pad this section.
Use one short clause per uncertainty. Do not restate conclusions already made
in the description. Submit an empty list when there are no meaningful
uncertainties.

Do not quote or list local paths, Photos ids, archive ids, raw GPS coordinates,
raw EXIF blocks, raw metadata JSON, hashes, or filenames.

Use this complete CardInput ProtoJSON context for reasoning only:

{{.MetadataJSON}}
