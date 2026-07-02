---
written_by: ai
---

# Observability

OpenTrawl gets one local observability system from crawlkit. Every crawler writes the same bounded log shape, every sync proves it is alive while it runs, and `trawl` can show recent failures without knowing each crawler's internals.

## Problem evidence

COMMENT (Josh, restored after an agent edit clobbered it): this is a good start but i don't feel like it's holistic enough for all of our services? the key problem is that agents should be able to own the entire dev lifecyle and operating loop without having to run into problems. right now this seems just for the sync idea? we should also consider the idea of - i can't rmemeber what the naame is - but software that either works or doesn't, but not one that you have to diagnose/fix/tweak or whatever -> it should be boring and all failures should be clear. zen of python idea.

The contract already says sync progress must exist, but today's crawlers still depend on ad hoc output.

- `gogcrawl/internal/cli/sync.go` emits progress only while ingesting backup shards. The long `gog backup gmail push` fetch runs first, and `gog.Client.run` captures stdout and stderr until the command exits. A Gmail backup can therefore run for an hour with no visible output.
- `telecrawl sync` is an alias for import. It passes an `io.Writer` to Telegram import code, but most dialog and message loading has no heartbeat. It mainly reports Telegram flood-wait sleeps and final stats, so a normal sync can appear stuck for minutes.
- Each crawler decides its own progress text and error handling. That makes `trawl` unable to answer a simple question: which source failed recently, when, and what should the user do next?

## Design

crawlkit owns the log writer, rotation, run identity, progress helper and log reader. Crawlers call crawlkit; they do not invent their own logging package.

The state layout is `<state-root>/<crawler-id>/logs/current.jsonl` plus `<state-root>/<crawler-id>/logs/1.jsonl` to `7.jsonl`.

The cap is fixed: 8 files, 4 MB each, 32 MB per crawler. When `current.jsonl` reaches 4 MB, crawlkit rotates the files and deletes the oldest one. There is no user setting for size, path, retention or log level.

The format is plain JSONL. Each line is one complete event. Required fields are timestamp, source, run id, command, level, event and message. Progress events also include stage, done, total when known, and elapsed milliseconds. Error events include code, remedy and retryable.

Timestamps are RFC 3339 with a local offset. crawlkit generates the run id at command start and puts it on every event for that command.

Logs must never include secrets, tokens, cookies, message bodies, mail subjects, note text or contact payloads. They may include source ids, counts, stage names, local state paths and actionable remedies.

Levels are `debug`, `info`, `warn` and `error`. The shipped ring buffer persists `info`, `warn` and `error`. `debug` exists for test sinks and local development builds, not as a user knob.

## What crawlkit exports

crawlkit exports one run context per command. The context provides:

- a logger with fixed levels and redaction checks
- a progress reporter that writes human progress to stderr
- JSONL progress events for `--json` sync output
- a durable writer for the per-crawler ring buffer
- helpers for structured errors with code, message and remedy
- readers for recent events and recent errors per crawler

Progress and logging use the same run id. A progress event shown live on stderr is also written to the ring buffer as an `info` event.

## What crawlers must call

Every crawler command starts a crawlkit run. Read-only commands may log start, completion and errors. Mutating commands, especially `sync`, must also report progress.

Every sync must emit a first progress event within 2 seconds. If a stage has no countable work, the crawler emits a heartbeat at least every 30 seconds with the stage name and elapsed time. A long opaque call, such as `gog backup gmail push`, must be wrapped in such a heartbeat.

Crawlers report these stages where they apply:

- source discovery
- authorisation check
- source fetch or source snapshot
- archive ingest
- index refresh
- media fetch
- complete

Crawlers must route user-visible errors through crawlkit's error helper. They must not print their own long-running progress directly with `fmt.Fprintf` or an untyped `io.Writer`.

## What trawl surfaces

`trawl sync` streams crawler progress while the command runs. Human progress goes to stderr, so stdout remains the command result. In JSON mode, sync emits JSONL progress and completion events.

`trawl status <source>` reads the ring buffer and shows the latest run: state, started time, finished time, duration, final stage and the most recent error if one exists.

`trawl doctor` reads all crawler ring buffers and adds a recent error section. It groups errors by source and shows time, command, code, message and remedy. It skips malformed log lines and reports the log file as a doctor warning, not as a crawler failure.

`trawl` does not get a separate logs verb in v1. The CLI has 5 verbs. Recent failures belong in `status` and `doctor`; raw logs stay local for developers and agents that need them.

## Out of scope

Metrics, SLOs, dashboards, Prometheus exporters and remote telemetry are out of scope for v1.

OpenTrawl is local-first software, not a hosted service. v1 needs bounded local evidence for humans and agents: what ran, what failed, what is still running, and what to do next. Metrics and service-level targets would add vocabulary, storage and configuration before there is a fleet to operate. Headline counts remain in `status`; logs explain operations.

## Migration path

- crawlkit: add the run context, ring writer, rotation, fixed level filter, error helper, progress helper and log readers first
- gogcrawl: wrap backup fetch with a heartbeat, then move shard ingest progress and sync errors onto crawlkit
- telecrawl: replace raw progress writers with crawlkit progress, then add heartbeats around dialog, message and media stages
- imsgcrawl: wrap archive sync and index refresh in the crawlkit run context, then map source parity failures to structured errors
- wacrawl: use crawlkit for sync progress and doctor failures, then remove any read-path progress or auto-sync noise from observability
- calcrawl: start with crawlkit observability from the first public implementation
- clawdex: log contact import runs, merge warnings and source failures, but never log contact payloads
- photoscrawl: adopt the same run context when Photos enters the suite

## Conformance checks

The conformance harness keeps the contract honest:

- every `sync` writes a progress event within 2 seconds
- long sync stages write another progress event within 30 seconds
- `sync --json` emits valid JSONL progress and completion events
- human sync progress goes to stderr, not stdout
- every emitted error has code, message and remedy
- no log line contains known secret patterns or source content fields
- the per-crawler log directory never exceeds 32 MB after rotation
- malformed JSONL in a ring buffer cannot crash `trawl status` or
  `trawl doctor`
- `trawl doctor` shows recent fixture errors grouped by source
- no crawler exposes a user flag or config key for observability
