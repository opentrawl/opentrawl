---
written_by: ai
---

# Trawler control contract

Every registered trawler exposes a small typed control surface to `trawl` and
the Mac app. Clients depend on this contract, not on provider archive schemas
or internal trawler packages.

The Go interfaces in `trawlkit/contracts.go` and the protobuf messages under
`trawlkit/proto/trawl` define the same boundary. Go owns execution. Protobuf
owns the records that cross into the CLI, the Mac app and later API clients.

## Command surface

The examples below assume that the CLI executable is in the current directory
as `./trawl`:

```text
./trawl
./trawl --help
./trawl status [TRAWLER] [FLAGS]
./trawl update [TRAWLER ...] [FLAGS]
./trawl search [WORDS ...] [FLAGS]
./trawl who NAME [FLAGS]
./trawl conversations [FLAGS]
./trawl messages --conversation LINK [FLAGS]
./trawl open LINK [FLAGS]
./trawl TRAWLER COMMAND [ARGUMENTS] [FLAGS]
```

Root commands combine results from the relevant installed trawlers. A trawler
namespace exposes its own archive commands. Run `./trawl TRAWLER --help` to see
them.

The CLI writes human text. The Mac app and other structured clients consume
the shared protobuf messages directly.

## Registry and command declarations

`RegisteredTrawlerManifest` declares:

- the stable trawler identity, command name, display name and aliases;
- the commands shown in the bare `trawl` overview;
- the capabilities implemented by the trawler;
- the trawler privacy boundary; and
- every public trawler command, its arguments, flags and help placement.

The privacy boundary states what the trawler reads, what leaves the Mac and
which network requests it makes. The root CLI and the Mac app read one
registry. It marks each trawler as available or coming soon and keeps names and
ordering consistent.

## Shared typed records

`TrawlerCommandResponse` is a protobuf `oneof`. It carries one direct response:

| Human concept | Protobuf response | Record in the response |
| --- | --- | --- |
| messages | `MessageListResponse` | `MessageRecord` |
| conversations | `ConversationListResponse` | `ConversationRecord` |
| people | `PersonListResponse` or `PersonRecord` | `PersonRecord` |
| calendar events | `CalendarEventListResponse` | `CalendarEventRecord` |
| calendars | `CalendarListResponse` | `CalendarRecord` |
| notes | `NoteListResponse` | `NoteRecord` |
| note folders | `NoteFolderListResponse` | `NoteFolderRecord` |
| recovered note versions | `RecoveredNoteVersionListResponse` | `RecoveredNoteVersionRecord` |

These records carry typed human facts. A message has its time, people and their
roles, text or media description, and conversation context. A conversation has
its name, people, latest activity and unread count. A person has names, contact
methods and contributing trawlers. A calendar event has its name, times,
calendar, place, people and event details.

`OpenRecord` uses another protobuf `oneof` for shared records and bounded
trawler-specific presentation:

- `OpenedMessageRecordWithConversationContext`;
- `ConversationRecord`;
- `PersonRecord`;
- `CalendarEventRecord`;
- `OpenedNoteRecord`; or
- `TrawlerSpecificOpenedRecordPresentation`.

An opened message includes typed surrounding messages and the conversation
link. This lets a person move from one result to the complete conversation
without adding another trawler argument.

An opened note carries its note and version identities, metadata and an
explicitly available or unavailable body. An available body is complete.

## Typed product projections and bounded plugin presentation

Each trawler owns its provider-native archive, source semantics and private
schemas. Recognised product concepts cross the client boundary as concrete
TrawlKit protobuf records. Notes projects note lists, folders, recovered
versions and opened notes into shared note records. Calendar projects its
calendar catalogue and events into shared calendar records. The CLI and Mac app
can therefore understand these concepts directly without depending on either
provider archive.

The generic `TrawlerSpecificCommandResponse` and
`TrawlerSpecificOpenedRecordPresentation` path is reserved for a genuinely
unknown plugin record type that has no recognised TrawlKit record. These
messages carry the small typed list or detail content that every client
understands. The plugin owns the content and each client owns its layout. They
do not carry the provider record, serialised bytes or a runtime type name.

The CLI currently invokes trawler-specific commands and renders their list or
detail response. The Mac app currently invokes only `status`, `update`,
`search` and `open`. It can render a trawler-specific detail returned by
`open`, but it does not currently invoke trawler-specific commands.

The generic plugin presentation contract is closed. Its values are limited to:

- text;
- an unsigned count;
- a time or calendar date; and
- an internal canonical record reference that TrawlKit converts to a link.

A list has named columns and rows. A detail has a name, named fields and an
optional text body. That is the entire shared presentation contract for
uncommon command output. It presents bounded lists and details; it is not an
arbitrary document language. Provider-native meaning stays with the provider.
The contract contains no `Any`, JSON, generic map, type URL, byte payload,
compatibility alias or second transport path.

## Status

`TrawlerArchiveStatus` carries three facts:

- archive content counts after the last successful update;
- the exact time of the last successful update; and
- whether the archive can answer its current commands.

The CLI presents these as `trawler`, `archived`, `last update` and `works`.
Failures are separate typed operation failures, not extra status text.

## Search and people

Search remains query-led. It returns evidence matching the query and its
filters rather than a catalogue of every record.

Each `TrawlerSearchMatch` carries:

- the matching record time;
- the trawler name and record kind;
- the human name of the matching record;
- related people and their roles;
- the nearest digital containers and physical places;
- typed text fields split into matching and non-matching fragments; and
- the internal record identity used to assign the public link.

The root search operation assigns the link and combines the matches. The CLI
renders comparable rows ordered by `when`, `match`, useful `who`, `where` and
`what` context, `trawler`, then a complete `open` command. Matched content gets
the flexible width. Narrow output keeps context and the open command attached
to its result. The CLI omits empty or exactly repeated context.

Search returns the total match count when the trawler knows it, whether that
count is a lower bound and whether more matches exist. `--limit` controls the
number shown. `--after` and `--before` accept dates or times. `--who` resolves a
person to the exact identifiers known by each relevant trawler before search.

Person matches carry the display name, useful alternative names, the value that
matched the query, contributing trawlers, message count and link. The CLI uses
message count first when it orders several people.

## Links and open

Every link shown by a root command is globally routable. It contains the
trawler identity and a stable local alias. A person or agent can copy it into
`./trawl open LINK` without selecting the trawler again.

Trawlers keep their canonical record references inside the typed and storage
contracts. TrawlKit assigns and resolves links at the shared boundary. Human
tables show links, not provider database identifiers, and never truncate them.

`open` returns the requested typed record and any bounded context around the
matching item. The response also keeps the requested link and optional anchor
identifier so each client can show the same target in its own layout.

`./trawl calendar calendars` returns a typed catalogue with each calendar's
exact globally routable link. `./trawl calendar events LINK` accepts one of
these links and lists events from that calendar; it does not identify a
calendar by its display name or account name. Calendar links are navigation
links for this command rather than records accepted by root `open`.

Calendar records carry the account and any human-entered owner or purpose
description from the source. When present, that description also accompanies
the calendar's events in event lists and opened event details, so similarly
named calendars retain their intended context.

## Output and failure rules

- Output is bounded and states when more records exist.
- Human text and protobuf output carry the same facts but use layouts suited to
  each client.
- Protobuf uses typed timestamps and calendar dates. Human output uses readable
  local time.
- Secrets, tokens, cookies and credential fragments never appear in output,
  errors or logs.
- A typed failure contains a code and a plain message. Partial operations keep
  successful records and identify the trawlers that did not complete.
- Detailed causes belong in run logs. Human errors do not give repair work to
  the user.
- Read commands use existing local archives and do not change the original
  apps. `update` is the explicit command that gets new items from apps.
- Public commands do not prompt for interactive input.
- Update progress uses typed protobuf events for the Mac app and stderr for the
  CLI.
- `-v` shows detailed progress on stderr. `-vv` adds debug detail.

Process exit codes are stable: `0` means complete, `1` means failed, `2` means
the command was used incorrectly and `3` means a partial result with usable
stdout.
