---
written_by: ai
---

# TrawlKit

TrawlKit is OpenTrawl's shared Go boundary and shared mechanics for
trawlers. Each trawler owns its app access, authentication, archive schema,
import logic and record meaning that applies only to that trawler.

## Shared boundary

[`contracts.go`](contracts.go) defines the Go interfaces that trawlers
implement. These interfaces cover status, archive updates, search, people, conversations,
messages and opening a record. [`proto/trawl`](proto/trawl) defines the typed
records and command results used by `trawl`, TrawlKit and the Mac app. Go
owns execution. Protobuf owns the shared record and transport contracts.

The shared record language has four record types used across trawlers:

- `MessageRecord`;
- `ConversationRecord`;
- `PersonRecord`; and
- `CalendarEventRecord`.

Each trawler owns protobuf records for complete meaning that applies only to
that provider. Those records stay inside the trawler. Shared concepts cross the
TrawlKit contract as the first-class shared records above. A generic client
cannot statically understand record types supplied by future plugins, so an
uncommon provider-specific record crosses the generic CLI and Mac boundary only
as the small typed list or detail presentation that every client understands.

The presentation contract for a trawler-specific command is deliberately
small. It can describe a list with named columns and rows, or a detail with a
name, named fields and an optional text body. A value can be text, an unsigned
count, a time or calendar date, or a globally routable link. It never carries
an `Any`, a type URL and byte payload, JSON or a generic map.

## Shared mechanics

TrawlKit organizes provider-neutral mechanics by concern so every trawler, CLI command, and index layer imports one canonical implementation:

- **`archivetime`** — RFC3339 / fixed-nanosecond UTC formatting and parsing, and `InclusiveTimeRange` inclusive date/time bounds (in-memory, Unix SQL, and chronological RFC3339 SQL).
- **`flags`** — Shared CLI flag and user-entered date/time parsing (`--after`, `--before`, `--limit`).
- **`store`** — SQLite archive storage hygiene, WAL management, archive locks, FTS5 tokenizing/quoting/East-Asian character-pair helpers, FTS5 marked snippets, and symmetric surrounding chat context windows (`ReadSurroundingMessageContextWindow`, `SQLKeysetBeforeAfterClause`).
- **`cache`** — Safe read-only copies and snapshots of live application databases.
- **`state`** & **`config`** — Durable sync cursor/state storage and state-root configuration paths.
- **`shortref`** — Globally routable links and compact per-trawler short references.
- **`unifiedindex`** & **`semantic`** — Cross-archive unified SQLite FTS5 index, materialized monthly history summaries, and hybrid semantic search.
- **`whomatch`**, **`openrecord`**, **`mailparse`** — Person resolution matching, opened-record validation, and RFC5322 email decoding.
- **`render`** & **`output`** — Single shared human/CLI output renderer.
- **`control`**, **`log`**, **`secrets`**, **`prototransport`**, **`pluginruntime`**, **`pluginproxy`**, **`pluginsandbox`** — Manifests, structured logging, encrypted plugin secrets, protobuf framing, and sandboxed external plugin execution.

See [`docs/contract.md`](../docs/contract.md) for the complete public control
contract. See [`AGENTS.md`](AGENTS.md) for the TrawlKit ownership boundary.
