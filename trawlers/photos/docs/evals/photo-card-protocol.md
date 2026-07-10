---
written_by: ai
---

# Photo card eval protocol

This protocol chooses a Photos card model, prompt and evidence shape without
paying to classify the library more than once. It evaluates the real product
boundary, not a parallel lab pipeline.

Photos image-model calls use frontier models through Ollama Cloud. Gemini served
through Ollama Cloud is eligible. Direct Gemini API calls are not. Terra is an
agent-runtime choice, not a Photos model provider.

## Entry gate

Do not run card-model comparisons until representative raw checks have proved:

- current asset and upstream-deleted state
- exact camera original and its hash
- current rendered still requested through PhotoKit version `.current`
- full camera-original metadata and readable EXIF projection
- complete cached place evidence for located assets
- exact rendered request persistence
- raw response retention

An eval that bypasses any of these boundaries cannot select the production
model. If the research harness and product do not use the same resolver,
metadata projection, place evidence and request renderer, the run is a harness
test only.

## Image input

The accepted card input uses the largest current rendition returned by
`PHImageManager.requestImageDataAndOrientation` with request version `.current`.
This includes all edits. The camera original separately supplies the exact
provenance hash and full metadata. Even for an unedited asset, do not assume the
2 byte streams are identical. The saved request records the role, type, size
and hash of the image actually sent.

Do not silently canonicalise, resize or substitute a derivative. If a model
requires another representation, evaluate that change explicitly against the
same assets and record the transform in the request.

## Representative corpus

Build the corpus dynamically from the current library. Use both targeted hard
cases and naïve random sampling so repeated testing does not overfit a frozen
set. Keep a small group of continuity examples for regression, including photos
the user has already selected as meaningful.

Sparse and failure-prone boundaries get extra weight. The corpus should cover:

- screenshots and dense interfaces
- receipts, tickets and documents
- menus, signs, handwriting and CJK text
- travel photos, landmarks, parks and trails
- a depicted place offset from the camera coordinate
- food, people, interiors, technical objects and low light
- edited and unedited assets
- package-local and iCloud-backed originals
- assets with sparse or apparently missing metadata

Start with a small canary. Expand only after reading every input and output.
Roughly 400 inspected assets is a useful guide before a full backfill, not a
fixed target or permission to skip sparse categories.

## One expensive card call

The default hypothesis is one card-model call per asset. It must produce a rich,
long description, useful visible-text extraction and clear uncertainty from the
complete evidence supplied.

OCR, composition and the final output shape remain eval-gated. Do not add a
second OCR or composition call by default. A bounded comparison may test extra
OCR evidence, but it wins only if the complete card improves without adding
confident transcription errors, unnecessary exposure or another carding pass.

The bounded 10 July 2026 comparison found no default OCR pre-pass worth adding.
The card model reading the image itself was the most reliable overall. Apple
Vision did not improve a card, while a separate Ollama Cloud OCR model helped a
dense CJK case but introduced confident errors and exposed more private text in
other cases. Keep one card call as the default. Repeat the comparison on the
chosen successor card model and representative corpus before the backfill.

## Required private evidence

For each call, retain outside the repository:

- corpus category and selection reason
- exact image role, size, type and hash
- complete rendered request after all formatting and truncation
- exact raw model response and telemetry
- parsed stored card
- elapsed time, retries and model-call count
- every upstream cache hit, miss and restart result that affected the request

Read the rendered request before accepting the response. Read the raw response
before accepting the parse. Confirm that the stored card corresponds to both.

Nothing from a real image, metadata record, location, request or response is
commit-safe.

## Review questions

Reviewers inspect the private evidence and ask:

- Does the request contain the right image and complete readable context?
- Does the summary identify the main point of the image?
- Is the description specific and long enough to preserve what matters?
- Does OCR capture important visible text without inventing characters?
- Does place reasoning distinguish the camera position from what is depicted?
- Does the model avoid inventing a venue, route, person or event?
- Does uncertainty identify real ambiguity without padding?
- Can the stored card be reconstructed from the retained provenance?

Use a capable Sol/OpenAI agent or a human for adversarial review. Ollama Cloud
models are the classification candidates, not the code, protocol or evidence
reviewers.

## Decision rule

No aggregate mean overrides a serious raw failure. A candidate loses if it
hallucinates places, drops important text, ignores supplied context, leaks
machine metadata into prose or produces output that the labelled parser cannot
store safely.

Test every current image-capable Ollama Cloud model against the same corpus, or
record a hard technical incompatibility that prevents a candidate from taking
the product request. Choose a model only after the representative corpus proves
quality and measured Ollama Cloud usage fits the planned backfill with
headroom. Model eval work and decisions are tracked in live
[TRAWL-107](https://linear.app/joshpalmer/issue/TRAWL-107). This protocol does
not mirror its work state. Historical runs in [eval runs](runs.md) are context,
not the answer.
