---
written_by: ai
---

# imsgcrawl

`imsgcrawl` is a local-first iMessage crawler. It reads Apple Messages through
a temporary read-only SQLite snapshot, syncs a source-native archive, and gives
humans or agents a small command surface for status, chat listing, transcript
reading, person resolution, search, and contact export.

The default output is for humans first and agents second: bounded, readable,
and explicit about follow-up commands. Use `--json` for machines, scripts,
tests, CrawlBar, or other workflows that need stable fields and local IDs.

## Quick Start

```bash
trawl imessage status
trawl imessage sync
trawl imessage chats
trawl imessage messages --chat 42
trawl imessage who elon
trawl imessage search "candles budget"
trawl imessage open imessage:msg/8831
trawl imessage contacts export
```

List commands are bounded by default. They show how many rows were returned and
how to ask for more. Use `--all` for chats or one chat's messages only when you
really want complete local output.

## What You See

Examples below use fake Trump cinematic universe fixture data, not real
Messages output.

### Metadata

```text
iMessage Crawl (imsgcrawl)
Local-first iMessage archive crawler.

Capabilities: metadata, status, doctor, sync, search, open, who, contacts_export, short_refs, chats, messages

Agent-facing commands:
  metadata        trawl imessage metadata --json
  status          trawl imessage status --json
  sync            trawl imessage sync --json
  doctor          trawl imessage doctor --json
  chats           trawl imessage chats --json
  messages        trawl imessage messages --json
  who             trawl imessage who NAME --json
  search          trawl imessage search QUERY --json
  open            trawl imessage open REF --json
  contacts export trawl imessage contacts export --json

Machine output: add --json to print the structured manifest.
```

### Status And Sync

`status` is a safe readiness check. It reports counts, not message contents.

```text
Status: ok
Archive is fresh.

Local archive:
  Database: /Users/example/.opentrawl/imessage/imessage.db
  Last sync: 7 Jun 09:15
  Messages: 12
  Chats: 4
  Named contacts: 6
  Since: 2026
```

`sync` refreshes the local archive and prints what it imported.

### Chats

`chats` shows the chat ID needed for `messages --chat`, plus enough context for
a human or agent to choose the right conversation.

```text
Chats: showing 3 of 4, newest first.
More: trawl imessage chats --limit 4
All: trawl imessage chats --all
Open: trawl imessage messages --chat CHAT_ID

chat  kind    msgs  latest            conversation
42    group   6     2026-06-07 09:10  Cabinet Group (Elon, JD, Xi, +1 more)
17    direct  3     2026-06-07 08:55  Failing Elon
9     direct  2     2026-06-06 22:03  JD Vance
```

### Messages

`messages` is a wrapped table for scanning. Message bodies stay in the `text`
column and are not truncated; row limits control size.

```text
Messages in Cabinet Group (chat 42): showing 3 of 6, newest-first.
More: trawl imessage messages --chat 42 --limit 6
All: trawl imessage messages --chat 42 --all
Search: trawl imessage search QUERY

date              from        text
2026-06-07 09:10  me          The candles budget is CORRECT.
2026-06-07 09:09  JD Vance    Sir, I have prepared bullet points:
                              - The hum is louder
                              - The couch remains loyal
2026-06-07 09:08  Failing Elon  (attachment)
```

### Search

`who` resolves a name, alias or identifier fragment to archived participants.
Use it before a person-filtered search when a name may match more than one
person.

```text
who            last seen         items  identifiers
Failing Elon   2026-06-07 09:11      3  +15550102
```

`search` shows which conversation a hit came from and keeps the matched text in
the table. `--who` resolves first, then filters by exact identifiers. A search
query is optional when `--who`, `--after` or `--before` is present. Use JSON
when an agent or script needs refs for follow-up commands.

```text
Search "candles budget": showing 1 of 1.
Open: trawl imessage open REF

date              who  where                         ref    text
2026-06-07 09:10  me   Cabinet Group (Elon, JD,      8x7m2  The candles budget is CORRECT.
                        Xi, +1 more)
```

```text
Failing Elon → Failing Elon
Search filters: showing 2 of 3.
More: trawl imessage search --limit 3 --who "Failing Elon"
Open: trawl imessage open REF
```

### Open

`open` takes a ref from `search --json` and prints the full message with a
bounded window from the same chat.

```text
Transcript: Cabinet Group, 7 Jun 2026
Ref: imessage:msg/8831
Participants: Elon, JD, Xi

Time: 2026-06-07 09:10
From: me
Text: The candles budget is CORRECT.

Context: 3 messages around this one.

   date              from        text
— Sun 7 Jun 2026 —
   2026-06-07 09:09  JD Vance    Sir, I have prepared bullet points.
>  2026-06-07 09:10  me          The candles budget is CORRECT.
   2026-06-07 09:11  Failing Elon  We need more candles.
```

### Contacts

`contacts export` is intentionally narrow: display name plus phone numbers.

```text
Contacts: showing 3 of 3.

name          phone
Donald        +15550100
JD Vance      +15550101
Failing Elon  +15550102
```

## JSON Mode

Text is the agent/human reading surface. JSON is the machine/workflow surface.
It includes local IDs and stable field names, so code can pipe it through `jq`
or feed it to crawlkit/CrawlBar-style consumers.

```bash
trawl --json imsgcrawl status
trawl --json imsgcrawl chats --limit 20
trawl --json imsgcrawl messages --chat 42 --limit 20
trawl --json imsgcrawl who elon
trawl --json imsgcrawl search --limit 20 "candles budget"
trawl --json imsgcrawl search --who "Failing Elon" --limit 20
trawl --json imsgcrawl open imessage:msg/8831
trawl --json imsgcrawl contacts export
```

Search JSON keeps the envelope small and parseable:

```json
{
  "query": "candles budget",
  "results": [
    {
      "ref": "imessage:msg/8831",
      "time": "2026-06-07T09:10:00+02:00",
      "who": "me",
      "where": "Cabinet Group",
      "snippet": "The candles budget is CORRECT."
    }
  ],
  "total_matches": 1,
  "truncated": false
}
```

A resolved person search includes the exact identifiers used by the filter:

```json
{
  "query": "",
  "results": [],
  "total_matches": 0,
  "truncated": false,
  "who_resolved": {
    "who": "Failing Elon",
    "identifiers": ["+15550102"]
  }
}
```

## Privacy

Messages data contains private names, phone numbers, emails, and conversation
contents. Do not publish raw output from a real Messages database. Tests and
public examples must use fake fixture data.

## Development

The repo is designed to be tested through `trawl imessage`.

With Nix/devenv:

```bash
direnv allow
scripts/dev-bin
trawl imessage status
```

Without Nix, install Go `1.26.4` or newer, then use the normal Go workflow:

```bash
go test ./...
scripts/dev-bin
trawl imessage status
```

Install `jq` if you want to run the smoke transcript or inspect JSON examples.

## Agent Smoke Transcript

Use the smoke transcript when reviewing whether the CLI actually works for an
agent. It runs `trawl imessage` with a synthetic `HOME`, then
writes exact stdout/stderr for progressive text and JSON commands to `/tmp`.

```bash
scripts/agent-smoke-transcript.sh --query "candles budget"
```

The script prints paths to `review.txt`, `manifest.jsonl`, `commands.tsv`, and
the raw stdout/stderr directory. These artifacts contain local Messages-derived
output. Keep them local unless the user explicitly asks to share them.
