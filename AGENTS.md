# AGENTS.md

## Purpose

`photoscrawl` is a local-first OpenClaw/crawlkit crawler for Apple Photos. It
builds an evidence-backed `photos.sqlite` archive from a user's Photos library
without uploading private media by default.

## Stack

- Product code is Go.
- Use `github.com/openclaw/crawlkit` for SQLite hygiene, JSON output, status
  shape, snapshots, state cursors, vector/embedding primitives when needed, and
  future TUI pieces.
- Darwin-only cgo bridges to Apple frameworks are allowed when PhotoKit, Vision,
  CoreLocation, or Core ML require them. Keep the bridge narrow and expose a Go
  interface.
- Do not add Swift, Python, Node, shell pipelines, or ad-hoc scripts to the
  product path.
- Tests must not touch the live Photos library. Use temp SQLite files and small
  synthetic fixtures only.

## Product Boundaries

- NO PRIVATE DATA IN THE REPO. Do not commit, stage, copy, or write private
  Photos data into this checkout: Photos libraries, `photos.sqlite`, snapshots,
  thumbnails, originals, exported media, extracted metadata dumps, GPS dumps,
  face data, OCR text, classifier output, logs containing asset metadata, or
  any other user-derived archive material.
- Keep private crawl artifacts outside the repo, preferably under
  `~/.photoscrawl`, XDG cache/data dirs, or `/tmp/` for short-lived fixtures.
- If verification needs real Photos access, run it read-only and report counts
  or synthetic examples only. Do not paste or save private asset identifiers,
  filenames, locations, OCR text, people labels, or media-derived content into
  tracked files.
- Keep public repo language user-helping and privacy-first. Do not add framing
  that makes the project sound like coercive profiling, public-sector targeting,
  data-broker tooling, dossier building, investigations, or unrelated casework.
  This is open source software for users to understand their own Photos data.
- Read from Apple Photos only through explicit read-only/snapshot flows.
- Never mutate Photos, albums, metadata, faces, or iCloud state.
- Cloud model calls are opt-in only and must identify exactly which assets or
  derived thumbnails leave the machine.
- Store observations, evidence, and candidate signals. Do not create durable
  person, place, trip, relationship, or life-event truth tables in v1.
- CPU is acceptable when it buys signal quality. Disk pressure is not; classify
  originals through a bounded local cache/ringbuffer when downloads are needed.

## Query Surface

Keep crawl-family JSON commands:

- `status`
- `init`
- `crawl`
- `classify`
- `search`
- `open`
- `neighbors`
- `evidence`

Add higher-level commands only after the underlying tables and evidence explain
the result.
