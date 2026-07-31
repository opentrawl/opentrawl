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

TrawlKit also provides trawler manifests and command help; globally routable
links and short references; human rendering; status, search and archive-update execution;
SQLite archive storage, read-only snapshots and archive locks; and shared run
logging and supervised long-running commands.

See [`docs/contract.md`](../docs/contract.md) for the complete public control
contract. See [`AGENTS.md`](AGENTS.md) for the TrawlKit ownership boundary.
