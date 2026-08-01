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
trawl x import archive PATH
```

The default database is `~/.opentrawl/twitter/twitter.db`. API credentials are
read from `~/.opentrawl/twitter/credentials.toml` with file mode `0600`. API
spend is metered locally against a configured monthly cap.

The archive stores posts, roles such as authored or liked, available author
profiles, import coverage, update state and a search index.

## Commands

```sh
trawl x import archive PATH
trawl update x
trawl x tweets --limit 20
trawl x bookmarks --limit 20
trawl x likes --limit 20
trawl x mentions --limit 20
trawl x stats --window 30d --by likes --limit 10
trawl x spend
trawl search "words" --trawler x
trawl open LINK
```

The CLI uses normal text output. Root `open` returns one post with bounded
ancestor and reply context. The X mentions endpoint limits how much older
incoming-reply history the crawler can recover.

## Network and privacy boundary

The only network service used by this crawler is `api.x.com`, for the explicit
update above. It never sends archive files, local database rows or paths to any
other service. Tokens never appear in output, errors or logs.
