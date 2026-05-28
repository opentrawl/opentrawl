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
