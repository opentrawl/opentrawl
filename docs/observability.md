---
written_by: ai
---

# Observability

OpenTrawl gets one local observability system from crawlkit. Every crawler writes the same bounded log shape, every sync proves it is alive while it runs, and `trawl` can show recent failures without knowing each crawler's internals.

COMMENT: this is a good start but i don't feel like it's holistic enough for all of our services? the key problem is that agents should be able to own the entire dev lifecyle and operating loop without having to run into problems. right now this seems just for the sync idea? we should also consider the idea of - i can't rmemeber what the naame is - but software that either works or doesn't, but not one that you have to diagnose/fix/tweak or whatever -> it should be boring and all failures should be clear. zen of python idea. 

so this is directionally good but not done yet. 

the other point is that this should be for OPERATORS too, so that an agent with the code and the logs, steered by someone who does not understand the product, can easily figure out what's broken if stuff is broken. (and later on, send feedback to maintainers via github issue, same as codex/claude etc do.)

check prior art here. 

also check stuff like logging - to me JSONL feels like the wrong choice honestly. whats wrong with a plain old log file? idk.

## Problem evidence

The contract already says sync progress must exist, but today's crawlers still depend on ad hoc output.

- `gogcrawl/internal/cli/sync.go` emits progress only while ingesting backup shards. The long `gog backup gmail push` fetch runs first, and `gog.Client.run` captures stdout and stderr until the command exits. A Gmail backup can therefore run for an hour with no visible output.
- `telecrawl sync` is an alias for import. It passes an `io.Writer` to Telegram import code, but most dialog and message loading has no heartbeat. It mainly reports Telegram flood-wait sleeps and final stats, so a normal sync can appear stuck for minutes.
- Each crawler decides its own progress text and error handling. That makes `trawl` unable to answer a simple question: which source failed recently, when, and what should the user do next?
- The same evidence is what a user pastes into a bug report from the Mac app: the ring buffer is the ticket attachment.

## Design

crawlkit owns the log writer, rotation, run identity, progress helper and log reader. Crawlers call crawlkit; they do not invent their own logging package.

The store is one sqlite database per crawler, `<state-root>/<crawler-id>/logs.db`, written through crawlkit with WAL and batched commits. A sqlite ring buffer matches or beats rotated text files here: with WAL and synchronous=NORMAL, batched inserts land within the same order of magnitude as file appends, and it buys row-exact truncation (the ring is a DELETE of the oldest rows in the same transaction), indexed queries for `trawl status` and `doctor`, and one storage technology across the whole product (prior art: blacklite, which ships exactly this shape for JVM logging). Rotated text files only win when nothing ever reads the logs.

The cap is fixed per crawler: 8 MB of events, oldest rows deleted first. Per-crawler enforcement needs no cross-process coordination; the global footprint is the cap times the crawler count (about 60 MB for the current suite), which is the real bound that matters. There is no user setting for size, path or retention.

Each event is one row with a JSON payload. Required fields are timestamp, source, run id, command, level, event and message. Progress events also include stage, done, total when known, and elapsed milliseconds. Error events include code, remedy and retryable.

Timestamps are RFC 3339 with a local offset. crawlkit generates the run id at command start and puts it on every event for that command.

Logs must never include secrets, tokens, cookies, message bodies, mail subjects, note text or contact payloads. They may include source ids, counts, stage names, local state paths and actionable remedies.

Levels are `debug`, `info`, `warn` and `error`. The ring buffer persists `info`, `warn` and `error`. `debug` is a runtime switch, not persisted configuration: the Mac app's debug mode and a developer shell can turn it on for a session, and it lands in the ring buffer only while switched on.

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

Every sync must emit a first progress event within 500 ms — sync must feel alive immediately. Stages without countable work heartbeat every second on stderr with the stage name and elapsed time; the ring buffer samples those heartbeats (roughly one in ten) so a day-long sync cannot churn the ring. A long opaque call, such as `gog backup gmail push`, must be wrapped in such a heartbeat.

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

- every `sync` writes a progress event within 500 ms
- long sync stages heartbeat at least every 2 seconds
- `sync --json` emits valid JSONL progress and completion events
- human sync progress goes to stderr, not stdout
- every emitted error has code, message and remedy
- no log line contains known secret patterns or source content fields
- the per-crawler logs.db never exceeds its cap after the ring deletes
- a corrupt or malformed logs.db cannot crash `trawl status` or
  `trawl doctor`
- `trawl doctor` shows recent fixture errors grouped by source
- no crawler exposes a user flag or config key for observability
