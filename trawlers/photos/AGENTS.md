---
written_by: ai
---

# AGENTS.md

`photoscrawl` is the read-only Apple Photos source for OpenTrawl. It builds a
provenance-backed local archive; it does not make the archive or its derived
cards a second source of truth.

## Privacy boundary

- Never commit a Photos library, archive database, snapshot, thumbnail,
  original, rendered media, metadata dump, coordinates, face data, OCR,
  classifier output or a log containing user-derived values.
- Public tests and examples use small synthetic fixtures. Tests never touch the
  live Photos library.
- Runtime data belongs in the shared trawlkit data, cache and state locations.

## Product boundaries

- Product code is Go. Use `trawlkit` for provider-neutral storage, control,
  output, state, model-call and cache mechanics.
- Keep PhotoKit, CoreLocation and ImageIO bridges narrow behind Go interfaces.
  Do not add Swift, Python, Node or shell pipelines to the product path.
- Never write to Photos, albums, metadata, faces or iCloud state.
- Model-backed classification uses one configured product seam. Persist the
  exact image identity and bounded rendered context that cross it, plus the raw
  response and provenance.
- Store source facts, observations and replaceable interpretations. Do not
  create durable person, place, trip, relationship or life-event truth tables.
- Do not invent deterministic label kinds for meaning that belongs in model
  prose. Add an enum only when code must gate on it mechanically.
- Media acquisition uses a bounded cache with proved entries. Do not trade
  unbounded disk use for convenience.

## Source contract

Keep `metadata`, `status`, `sync`, `classify`, `search` and `open`
aligned with the root control contract.

[Architecture](docs/architecture.md) is the canonical Photos product contract.
Update it when a durable source or pipeline boundary changes. Do not put work
state, ticket routing, provider selection or model catalogues in public docs.

Every pipeline phase and per-item outcome logs one structured line with its
duration, including success. Output is bounded and human-readable; `open` does
not expose private evidence refs or archive-derived counts.
