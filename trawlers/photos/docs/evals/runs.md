---
written_by: ai
---

# Historical eval runs

This is a qualitative record of experiments run on 3 July 2026. It contains no
real-library counts, model scores, image details, identifiers or provider
payloads. Those runs predate the current original, current-rendition, metadata,
place, request-persistence and representative-sampling gates. They do not choose
the production model or prompt.

Some experiments called a paid Gemini endpoint directly. Do not repeat that
path. Photos image classification and image-model evals now use Ollama Cloud.

## What happened

- The first product-shaped run stopped before model invocation because no
  eligible image reached the research harness.
- A later comparison substituted package-local rendered derivatives. It was
  useful for debugging the harness but did not test the required image role.
- The first parallel PhotoKit original-export attempt crashed before model
  invocation. Sequential attempts then reused a populated private original
  cache and completed, but the run did not retain enough source and cache
  evidence to establish how each original became available.
- Prompt variants returned labelled prose, but used the old metadata sidecar,
  run wrapper and weak place input. Their relative scores cannot choose today's
  request, prompt or model.
- The old corpus was small and selected by recency. It did not represent the
  sparse metadata, CJK text, difficult place, edited-image and failure cases
  required before a library backfill.

## What survives

- Read the exact prepared image and rendered request before spending a model
  call. A successful response cannot repair the wrong input.
- Product and eval must share acquisition, metadata, place, request and
  persistence boundaries. A parallel lab path proves only itself.
- Model catalogues and behaviour drift. Re-evaluate every current image-capable
  Ollama Cloud candidate or record a hard incompatibility before exclusion.
- Keep raw private requests and responses for the current run. Aggregate scores
  without those boundaries are not decision evidence.

New method belongs in the [photo card eval protocol](photo-card-protocol.md).
Work state, ownership and decisions belong only in live Linear tickets.
