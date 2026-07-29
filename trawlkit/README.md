---
written_by: ai
---

# TrawlKit

TrawlKit is OpenTrawl's shared Go boundary and shared mechanics for
trawlers. Each trawler owns its app access, authentication, archive schema,
import logic and record meaning that applies only to that trawler.

## Shared boundary

[`contracts.go`](contracts.go) defines the Go interfaces that trawlers
implement. These interfaces cover status, sync, search, people, conversations,
messages and opening a record. [`proto/trawl`](proto/trawl) defines the typed
records and command results used by `trawl`, TrawlKit and the Mac app. Go
owns execution. Protobuf owns the shared record and transport contracts.

The shared record language has four record types used across trawlers:

- `MessageRecord`;
- `ConversationRecord`;
- `PersonRecord`; and
- `CalendarEventRecord`.

A trawler puts any record that applies only to that trawler in its own protobuf and packs it in
`google.protobuf.Any`. TrawlKit transports that value as a trawler-specific
command response or opened record. It does not turn trawler-specific meaning
into shared fields.

The presentation contract for a trawler-specific command is deliberately
small. It can describe a list with named columns and rows, or a detail with a
name, named fields and an optional text body. A value can be text, an unsigned
count, a time or calendar date, or a globally routable link. The packed
protobuf remains the record contract.

## Shared mechanics

TrawlKit also provides trawler manifests and command help; globally routable
links and short references; human rendering; status, search and sync execution;
SQLite archive storage, read-only snapshots and archive locks; and shared run
logging and supervised long-running commands.

See [`docs/contract.md`](../docs/contract.md) for the complete public control
contract. See [`AGENTS.md`](AGENTS.md) for the TrawlKit ownership boundary.
