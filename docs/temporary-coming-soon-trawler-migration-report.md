---
written_by: ai
---

# Temporary Gmail, Photos and X migration report

This report records the remaining work needed before Gmail, Photos and X can
join the accepted OpenTrawl command line interface. It describes the current
code and observed behaviour at `1bf74a31`. It does not define a second product
contract and does not start any migration.

Delete this report after Gmail, Photos and X have each passed their real
accepted journeys and the durable contracts and trawler documentation contain
the remaining truth.

## Accepted baseline

The available trawlers now establish this baseline:

- one registry controls the command line interface and Mac catalogue;
- Go interfaces and protobuf messages carry typed product contracts;
- each trawler owns its provider records and storage;
- shared rows and details contain human facts rather than flattened prose;
- status says what was archived, when it was last updated and whether it works;
- search returns a globally routable link that `open` accepts;
- every displayed next action is directly copyable;
- output remains readable at 80, 99, 200 and 500 columns;
- each archive has one live schema with no compatibility path; and
- the real update, status, search and open journey works before tests or
  protection are added.

The central registry already includes all three trawlers and marks them as
coming soon. The same registry feeds command discovery and the Mac catalogue.
This part is aligned. See
[`trawl/internal/cli/trawlers.go`](../trawl/internal/cli/trawlers.go#L45).

## Current position

| Trawler | What is already aligned | Main gap before acceptance |
| --- | --- | --- |
| Gmail | Registered once; typed status, update, search, people matching and open interfaces; canonical references receive short links | The human search row discards sender and query-match structure; the opened Gmail protobuf is reduced to a generic detail; status hides read failures; there is no useful Gmail browse entry point |
| Photos | Registered once; typed status, update, search and open interfaces; canonical references receive short links; detailed provider-owned photo protobufs exist | The public commands expose the internal classification and card pipeline; search discards people and place facts that the archive already returns; the typed photo record is reduced to a generic detail; provider data still crosses important internal boundaries as maps and JSON |
| X | Registered once; browse commands use shared typed message rows; search, open and status use the shared control interfaces; four useful browse commands appear in the overview | Search discards reply and role context; open is a generic detail; status can claim that the archive works without checking import readiness; names alternate between X, Twitter and bird; old JSON-shaped response structures remain in the product path |

## Evidence and proof limits

With `OPENTRAWL_ALL_TRAWLERS=1`, the complete bare output and help were read at
80, 99, 200 and 500 columns. All help output stayed within the selected width.
Gmail and Photos had no useful item command in the bare overview. X showed
`tweets`, `bookmarks`, `likes` and `mentions`.

Read-only commands against the default real archive root reported that the
Gmail, Photos and X archives were not available. The database files existed,
but their stored table layouts did not match the single live read path at this
commit. No archive was changed, copied or rebuilt. Therefore this audit could
not inspect real search rows, follow a returned link through `open`, or accept
status and output width for any of the three trawlers. No synthetic data was
used.

This is evidence that the current default archives cannot prove the operating
path. It is not evidence that a compatibility layer is needed. The accepted
pre-v1 rule remains one live schema and a new import from the original provider
data when migration work begins.

## Gmail

### What works in the architecture

- Gmail implements the shared trawler, update, search and people-matching Go
  interfaces. Its manifest and privacy boundary use the central registry.
  See [`trawlers/gmail/crawler.go`](../trawlers/gmail/crawler.go#L35).
- Search produces canonical message references and the shared search protobuf.
  The archive assigns short references when messages enter storage. See
  [`trawlers/gmail/search.go`](../trawlers/gmail/search.go#L13) and
  [`trawlers/gmail/internal/archive/store.go`](../trawlers/gmail/internal/archive/store.go#L82).
- Open first builds a provider-owned `OpenedGmailMessageRecord` with message,
  thread, sender, recipients, labels, attachments and body fields. This is a
  strong provider boundary. See
  [`trawlers/gmail/proto/trawl/gmail/open/open.proto`](../trawlers/gmail/proto/trawl/gmail/open/open.proto#L9).
- The update path preserves an encrypted local `gog` backup and imports pending
  shards into the Gmail archive. External JSON is confined to the `gog`
  archive and manifest boundary.

### What differs from the accepted product

- The archive search result already contains `Who`, `Where` and exact match
  runs. The protobuf projection uses the subject as the display name and emits
  one unmarked `Message` text field. It drops the sender, email thread context,
  record kind and the exact subject/body match evidence. See
  [`trawlers/gmail/internal/archive/types.go`](../trawlers/gmail/internal/archive/types.go#L73)
  and [`trawlers/gmail/search.go`](../trawlers/gmail/search.go#L13).
- The provider-owned Gmail record does not cross the shared open boundary.
  `OpenRecord` immediately converts it to
  `TrawlerSpecificOpenedRecordPresentation`. The command line interface and Mac
  app therefore receive presentation fields, not the Gmail message contract.
  See [`trawlers/gmail/open_record.go`](../trawlers/gmail/open_record.go#L21).
- Status converts every archive-open or status-read error into an empty status.
  A broken archive is indistinguishable from a missing archive and no useful
  failure reaches the shared status operation. See
  [`trawlers/gmail/crawler.go`](../trawlers/gmail/crawler.go#L80).
- Gmail declares no item-list command. Its trawler help contains only the
  shared search instruction, and its bare overview has an empty right-hand
  side. A person cannot browse recent mail before choosing search words.
- `update gmail --help` exposes `--backup-repo`, `--query` and `--max`. These
  describe backup and partial-import mechanics rather than the ordinary human
  update path. See
  [`trawlers/gmail/crawler.go`](../trawlers/gmail/crawler.go#L62).
- The README still teaches nested `gmail status`, `gmail search` and
  `gmail open` commands and omits `./`. This conflicts with the accepted root
  status, search and open journey. See
  [`trawlers/gmail/README.md`](../trawlers/gmail/README.md#L27).

### Boundary for the future Gmail slice

The first slice ends when one real Gmail update completes, status reports the
message archive truthfully, a scoped search row shows the message time, sender,
mail context, exact match and link, and `open LINK` presents the selected mail
through a typed contract. The same journey must be inspected at 80, 99, 200
and 500 columns and in the Mac consumer. A browse command belongs in this slice
only if the real journey proves that recent mail needs one.

Search-row semantics and shared mail or message behaviour are shared-contract
questions. `gog` access, Gmail fields, backup storage and email-thread meaning
remain Gmail-owned.

## Photos

### What works in the architecture

- Photos implements the shared trawler, update, search and open Go interfaces
  and uses the central registry. See
  [`trawlers/photos/crawler.go`](../trawlers/photos/crawler.go#L51).
- Update reads Apple Photos without changing the source library. Search and
  open use canonical photo references and short-link assignment.
- The provider-owned open protobuf distinguishes source facts, capture time,
  media, place, camera, albums, original-asset facts and model-derived details.
  See
  [`trawlers/photos/proto/trawl/photos/open/open.proto`](../trawlers/photos/proto/trawl/photos/open/open.proto#L9).
- The archive keeps source observations separate from model-derived
  observations and records model provenance. This direction matches the
  durable-substrate contract.

### What differs from the accepted product

- The visible command surface starts with `classify` and exposes
  `select-card-input-ready`, `acquire-current-still`, `prepare-card` and
  `create-card`. Their flags require asset identifiers, source-library
  identifiers and approval values. These are pipeline operations, not an
  understandable Photos journey. See
  [`trawlers/photos/crawler.go`](../trawlers/photos/crawler.go#L86).
- There is no photo or album browse entry point. The bare overview has no
  useful action for Photos.
- The archive search result already contains recognised people, place and
  exact field match runs. The shared projection drops all three, omits the
  record kind and emits one unmarked `Photo` text field. See
  [`trawlers/photos/internal/archive/query.go`](../trawlers/photos/internal/archive/query.go#L30)
  and [`trawlers/photos/crawler.go`](../trawlers/photos/crawler.go#L365).
- The provider-owned `OpenedPhotoRecord` does not cross the shared open
  boundary. It is rebuilt as a generic detail with many fields. This prevents
  the Mac app and later interfaces from using typed media behaviour. See
  [`trawlers/photos/open_record.go`](../trawlers/photos/open_record.go#L22).
- Important provider facts cross internal archive and media boundaries as
  `map[string]any`, generic SQL-row maps and JSON values. These erase the
  meaning expressed by the Photos protobuf and make agent changes hard to
  verify. The densest path is
  [`trawlers/photos/internal/archive/open_result.go`](../trawlers/photos/internal/archive/open_result.go#L135).
- The archive schema stores several domain payloads as JSON text. Some are
  legitimate source or model evidence, but several are also the active
  application model. The future slice must keep external JSON at its source
  boundary and use named types inside the product. See
  [`trawlers/photos/internal/archive/schema.go`](../trawlers/photos/internal/archive/schema.go#L9).
- `FallbackProvider` is a second snapshot route which records a primary error
  inside generic metadata. The production constructor currently chooses one
  SQLite snapshot provider, so this appears to be unused residue rather than a
  proved operating path. See
  [`trawlers/photos/internal/photos/fallback.go`](../trawlers/photos/internal/photos/fallback.go#L8)
  and
  [`trawlers/photos/internal/photos/provider_darwin.go`](../trawlers/photos/internal/photos/provider_darwin.go#L5).
- The README teaches nested status, search and open commands, exposes
  `metadata` although the current help does not, and omits `./`. See
  [`trawlers/photos/README.md`](../trawlers/photos/README.md#L25).

### Boundary for the future Photos slice

The first slice is only the ordinary path: update a real Photos library, show
truthful status, search for a known photo fact, follow the returned link and
open a bounded typed photo record. Search must preserve the matched field,
people, albums and place when present. The result and opened record must work
at 80, 99, 200 and 500 columns and through the Mac consumer.

Classification and card creation do not belong in the first acceptance path.
They should remain outside the normal help until the basic Photos journey
works. A shared typed media record is a likely shared-contract gap because
Photos and message attachments need common media behaviour. PhotoKit access,
photo facts, observations and model provenance remain Photos-owned.

## X

### What works in the architecture

- X implements the shared trawler, update, search and open Go interfaces and
  uses the central registry. See
  [`trawlers/twitter/crawler.go`](../trawlers/twitter/crawler.go#L41).
- `tweets`, `bookmarks`, `likes` and `mentions` are useful browse entry points
  and appear in the bare overview. They return the shared typed message-list
  protobuf, including time, author, canonical reference and text. See
  [`trawlers/twitter/output.go`](../trawlers/twitter/output.go#L15).
- `stats`, `spend` and archive import return typed shared list or detail
  presentations rather than JSON output.
- Imported and live posts receive canonical references and short links. Open
  can include bounded ancestor and reply context in the provider-owned Twitter
  record.

### What differs from the accepted product

- The store search result contains author, reply target and archive roles. The
  shared projection emits only author as the title and one unmarked `Post`
  field. It drops reply context, roles, record kind and exact query-match
  evidence. See
  [`trawlers/twitter/internal/store/search.go`](../trawlers/twitter/internal/store/search.go#L27)
  and [`trawlers/twitter/search.go`](../trawlers/twitter/search.go#L15).
- Search does not accept the shared resolved-person filter even though author
  identity is a first-class archive fact.
- The provider-owned Twitter post protobuf does not cross the shared open
  boundary. Open converts it to a generic detail, so the Mac app cannot use
  typed post, reply or engagement fields. See
  [`trawlers/twitter/crawler.go`](../trawlers/twitter/crawler.go#L168).
- Shared status sets `trawler_archive_can_answer_current_commands` to true after
  any readable store status. It does not apply the archive-readiness rule used
  by the older internal status envelope. See
  [`trawlers/twitter/crawler.go`](../trawlers/twitter/crawler.go#L128) and
  [`trawlers/twitter/contract.go`](../trawlers/twitter/contract.go#L101).
- `statusEnvelope`, `freshnessEnvelope`, `updateEvent` and related JSON-tagged
  structures remain even though the current response boundary is protobuf.
  They duplicate product language and contain banned vague terms. See
  [`trawlers/twitter/contract.go`](../trawlers/twitter/contract.go#L17) and
  [`trawlers/twitter/update.go`](../trawlers/twitter/update.go#L35).
- One concept has several names: the registered identity and package are
  `twitter`, the command is `x`, the display name is `Twitter (X)`, the
  configuration type is `birdConfig`, and the README teaches `twitter`.
  Ordinary text search therefore does not find one complete implementation.
- The update report returns no added, changed or removed counts even after the
  update runner records its totals. See
  [`trawlers/twitter/update.go`](../trawlers/twitter/update.go#L399).
- Browse commands use the shared `MessageRecord`. That gives the correct row
  mechanics now, but the contract does not express post roles, reply context
  or engagement facts. These facts must not be concatenated into the text
  field.
- The README teaches nested status, search and open commands and omits `./`.
  See [`trawlers/twitter/README.md`](../trawlers/twitter/README.md#L32).

JSON parsing at the official X archive and X API boundaries is required by
those providers. Raw source responses may be retained as source evidence. This
is separate from the obsolete JSON-shaped internal product structures above.

### Boundary for the future X slice

The first slice starts from a real official X archive import. It ends when
status reports that archive truthfully; each browse command returns useful,
bounded rows; a scoped search preserves author, post role, reply context and
exact match evidence; and `open LINK` presents the selected post through a
typed contract. The complete journey must be inspected at 80, 99, 200 and 500
columns and through the Mac consumer. Live API update and spend behaviour can
follow only after the local import journey works.

Post role and reply meaning remain X-owned. Shared row, person, link and open
mechanics belong in TrawlKit only when another trawler uses the same real
concept. A provider-owned post detail can use the small generic detail fallback
until a real cross-trawler media or post feature earns a shared type.

## Shared work that should happen once

The audit found three gaps that should not be repaired independently in every
trawler:

1. Search projections need one enforced way to carry record kind, associated
   time, people, nearest digital containers, physical places, exact matched
   fields and a globally routable link. The shared protobuf already contains
   these fields; Gmail, Photos and X do not populate them.
2. A first-class shared media contract is not present. Photos needs one before
   the Mac app can receive a typed photo, but it should contain only behaviour
   also used by another trawler, such as message attachments.
3. Provider-owned opened records are currently converted into the generic
   detail fallback before crossing the shared boundary. Gmail and Photos
   already have strong provider protobufs, and X has one, but none is carried
   through the central open contract. The future design must retain dynamic
   trawler extension without hard-coding every provider type into one central
   union.

Everything else in this report is local to the named trawler. Do not create a
new renderer, compatibility framework, archive version system or universal
record ontology while closing these gaps.

## Acceptance order

Migrate one trawler at a time. For each trawler, prove this complete journey on
real data before adding protection:

1. register and orient;
2. update or import once through the ordinary path;
3. read truthful status;
4. browse the primary human items when the product has a browse entry point;
5. search for a known fact;
6. copy the returned link into `open`;
7. inspect the command line result at 80, 99, 200 and 500 columns; and
8. inspect the same typed record in the Mac app.

Only then remove this report section for that trawler and move durable facts to
its contract and documentation.
