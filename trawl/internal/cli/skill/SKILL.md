---
name: opentrawl
description: Search and open the user's local digital archives across messages, email, calendar, notes, contacts, money, photos, and social sources using OpenTrawl MCP tools or the trawl CLI. Use whenever a task needs ground truth from the user's own history.
---

# OpenTrawl (`trawl`)

OpenTrawl gives you strictly read-only, repeatable access to the user's local archives so you can find ground truth directly instead of guessing or scraping raw app databases.

## Rules

- OpenTrawl is read-only. Never assume a lookup sends messages, edits emails, or modifies source apps.
- Always open the full record before answering from a search snippet. Copy links verbatim (for example `gmail:abc123` or `imessage:def456`).
- Respect source scope. When connected through a scoped MCP token, `trawl_status` lists the exact sources this session can read. Never imply "not found" for a source that is unavailable, stale, or outside your scope.

## MCP tools

When the `opentrawl` MCP server is connected, prefer its read-only tools:

- `trawl_status`: Check which sources are archived, when each was last updated, and whether it works.
- `trawl_search`: Search across allowed sources (`query`, `trawler`, `who`, `after`, `before`, `limit`). Each match returns a link.
- `trawl_open`: Open one record in full by its link from `trawl_search`.
- `trawl_around`: Find related records across allowed sources near a link, date (`YYYY-MM-DD`), or `--trip` window.
- `trawl_who`: Resolve a person by name, phone number, or email address across allowed sources.

## CLI equivalents

When running `trawl` directly in a shell:

- `trawl status [TRAWLER]`
- `trawl search [WORDS...] [--trawler NAME] [--who PERSON] [--after YYYY-MM-DD] [--before YYYY-MM-DD] [--limit N]`
- `trawl open LINK`
- `trawl around TARGET [--window 3d] [--trawler NAME] [--limit N]`
- `trawl who NAME`

Run `trawl --help` or `trawl COMMAND --help` for exact flags and source-specific subcommands.
