---
written_by: ai
---

# Twitter (X)

The Twitter crawler archives a person's own X data in local SQLite. It has two
source paths:

1. an official X archive export seeds authored posts and likes;
2. the official X API v2 refreshes authored posts, replies, likes, bookmarks and
   engagement counts with the user's OAuth credentials.

It does not scrape, automate a browser or use cookies.

## Import and storage

Import an extracted or zipped X archive:

```sh
trawl twitter import archive /path/to/x-archive
```

The default database is `~/.opentrawl/twitter/twitter.db`. API credentials are
read from `~/.opentrawl/twitter/credentials.toml` with file mode `0600`. API
spend is metered locally against a configured monthly cap.

The archive stores posts, roles such as authored or liked, available author
profiles, import coverage, sync state and a search index. Canonical refs look
like `twitter:tweet/1800000000000000001`.

## Commands

```sh
trawl twitter doctor
trawl twitter import archive /path/to/x-archive
trawl twitter sync
trawl twitter status
trawl twitter tweets
trawl twitter bookmarks
trawl twitter likes
trawl twitter mentions
trawl twitter search "solar kettle" --limit 20
trawl twitter open twitter:tweet/1800000000000000001
trawl twitter stats --window 30d --by likes --limit 10
```

Add `--json` for structured output. `open` returns one post with bounded
ancestor and reply context. The X mentions endpoint limits how much older
incoming-reply history the crawler can recover.

## Network and privacy boundary

The only network service used by this crawler is `api.x.com`, for the explicit
sync above. It never sends archive files, local database rows or paths to any
other service. Tokens never appear in output, errors or logs.
