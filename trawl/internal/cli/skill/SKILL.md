---
name: opentrawl
description: Search and open the user's local digital archives across messages, email, calendar, notes, contacts, photos, health, finances, and X history using OpenTrawl MCP tools or the trawl CLI. Use whenever a task needs ground truth from the user's own history.
---

# OpenTrawl (`trawl`)

OpenTrawl gives you strictly read-only, repeatable access to the user's local archives in `~/.opentrawl` so you can find ground truth directly instead of guessing or scraping raw app databases.

## Rules

- OpenTrawl is read-only. Never assume a lookup sends messages, edits emails, or modifies source apps.
- Always open the full record before answering from a search snippet. Copy links verbatim (for example `gmail:abc123`, `imessage:def456`, or `health:ghi789`).
- Respect source scope. When connected through a scoped MCP token, `trawl_status` lists the exact sources this session can read. Never imply "not found" for a source that is unavailable, stale, or outside your scope.
- Do not run `trawl update`, install plugins (`trawl plugin install`), enable semantic search (`trawl semantic enable`), or write agent configs (`trawl connect --write`) unless the user asks.
- Process exit codes: `0` complete, `1` failed, `2` bad usage, `3` partial result with usable stdout.

## 12 built-in sources

- `imessage`: Apple Messages conversations and messages
- `whatsapp`: WhatsApp Desktop chats, groups, and messages
- `telegram`: Telegram for macOS or direct zero-FDA MTProto login (`trawl telegram connect`)
- `mail`: Apple Mail and iCloud Mail accounts
- `gmail`: Gmail mailbox via direct read-only Google OAuth (`trawl gmail connect`)
- `notes`: Apple Notes folders, notes, attachments, and recovered versions
- `contacts`: Apple Contacts and cross-source people resolution
- `calendar`: Apple Calendar events, calendars, and attendees
- `photos`: Apple Photos dates, places, albums, cameras, people tags, and favourites
- `health`: Apple Health exports (`export.zip` / `export.xml`), HealthFit `.fit` workouts, sleep, vitals, and incremental HealthKit batches
- `money`: Bank accounts, balances, and transactions
- `twitter`: X posts, bookmarks, likes, mentions, and direct messages

## MCP tools

When the `opentrawl` MCP server is connected, prefer its read-only tools:

- `trawl_status`: Check which sources are archived, when each was last updated, and whether it works.
- `trawl_search`: Search across allowed sources (`query`, `trawler`, `who`, `after`, `before`, `limit`). Each match returns a link.
- `trawl_open`: Open one record in full by its link from `trawl_search`.
- `trawl_around`: Find related records across allowed sources near a link, date (`YYYY-MM-DD`), or `trip` window.
- `trawl_who`: Resolve a person by name, phone number, or email address across allowed sources.

## CLI commands

Bundled macOS CLI path: `/Applications/OpenTrawl.app/Contents/Helpers/trawl`.

Core read-only lookup:

- `trawl status [TRAWLER]`
- `trawl search [WORDS...] [--trawler TRAWLERS] [--who PERSON] [--after DATE] [--before DATE] [--limit N]`
- `trawl open LINK [--participants]`
- `trawl around [TARGET] [--trip NAME] [--window 3d] [--trawler TRAWLERS] [--limit N]`
- `trawl who NAME... [--limit N]`
- `trawl conversations [--with PERSON] [--unread] [--all] [--limit N]`
- `trawl messages --conversation=LINK [--after DATE] [--before DATE] [--limit N]`

Source-specific highlights:

- `trawl health workouts|sleep|metrics|summary` and `trawl health import PATH`
- `trawl photos [--on DATE] [--from DATE] [--to DATE] [--place NAME] [--person NAME]` and `trawl photos days`
- `trawl notes [FOLDER]`, `trawl notes folders`, `trawl notes versions LINK`
- `trawl money accounts`, `trawl money transactions`
- `trawl calendar events [LINK]`, `trawl calendar calendars`
- `trawl twitter bookmarks|tweets|likes|mentions|conversations|messages`

Maintenance, semantic search, plugins, and agent setup (only when asked):

- `trawl update [TRAWLER...]` — refresh local archives from source apps or APIs
- `trawl index status|rebuild` — inspect or rebuild unified `index.db`
- `trawl semantic status|enable|build|disable` — manage optional Apple Silicon local hybrid semantic search (`semantic.db`)
- `trawl plugin list|install|remove` — list, sandbox-install (SHA-256 pinned), or remove standalone plugins
- `trawl connect [AGENT] [--sources TRAWLERS] [--transport stdio|http] [--skill] [--write]` — preview exact MCP and `SKILL.md` config diffs and write scoped setup
- `trawl mcp [--sources TRAWLERS] [--http] [--listen HOST:PORT]` — serve read-only MCP tools over stdio or Streamable HTTP
- `trawl token create|list|revoke` — manage per-agent source-scoped MCP bearer tokens
- `trawl skill` — print these canonical instructions

Run `trawl --help`, `trawl COMMAND --help`, or `trawl TRAWLER --help` for exact subcommands and flags.
