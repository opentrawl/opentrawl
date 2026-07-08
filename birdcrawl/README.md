---
written_by: ai
---

# birdcrawl

birdcrawl archives your own X/Twitter data into a local SQLite database
and gives you bounded command-line views over it.

Two sources, and only these two:

1. Your official X archive dump (GDPR export) seeds the history.
2. The official X API v2 (paid, pay-per-use) keeps it current:
   authored tweets, replies to you, likes, bookmarks and engagement
   counts. Sync spend is metered locally against a monthly budget cap
   and stops with a structured error when the cap is reached.

No scraping, no cookies, no browser automation.

## What it reads

From an extracted or zipped X archive dump:

- `tweets.js` for authored tweets
- `note-tweet.js` for the full text of long-form tweets
- `account.js` for the account identity (required)
- `like.js` for liked tweets

From the X API (with your own OAuth credentials in
`~/.opentrawl/birdcrawl/credentials.toml`, permissions 0600): your own
timelines and bookmarks. Replies to you come from the mentions
timeline, which X caps at roughly the 800 most recent — older
incoming replies are not obtainable from any X source.

Run:

```sh
scripts/dev-bin
trawl birdcrawl import archive /path/to/x-archive
```

The default database is `~/.opentrawl/birdcrawl/birdcrawl.db`.

## What it stores

birdcrawl stores a local SQLite archive with:

- tweets
- tweet roles, such as authored and like
- author profiles when the archive provides them
- sync state for archive import coverage
- an FTS5 search index for tweet text

Refs use this shape:

```text
birdcrawl:tweet/1800000000000000001
```

## What it never sends

birdcrawl talks to exactly one network service: `api.x.com`, using
your own credentials, to fetch your own data. It never sends tweet
text, archive files, database rows, tokens or local paths anywhere
else. Tokens never appear in output, errors or logs.

## Commands

Read your archive:

```sh
trawl birdcrawl tweets
trawl birdcrawl bookmarks
trawl birdcrawl likes
trawl birdcrawl mentions
trawl birdcrawl search "solar kettle" --limit 20
trawl birdcrawl open t7k3f
trawl birdcrawl stats --window 30d --by likes --limit 10
```

Keep it fresh:

```sh
trawl birdcrawl sync
trawl birdcrawl import archive /path/to/x-archive
```

Health:

```sh
trawl birdcrawl status
trawl birdcrawl doctor
trawl birdcrawl metadata --json
```

Human text is the default. Add `--json` for the contract envelope.

Browse and search return 20 results by default; `--limit N` is
honored exactly and `--all` returns everything. `--after`/`--before`
take RFC3339 or YYYY-MM-DD. `open` accepts a short ref from human
output or a full `birdcrawl:tweet/ID` ref, and returns one tweet, up
to 3 ancestors and up to 20 replies.

## Current status

birdcrawl is pre-1.0. Breaking changes are allowed while the OpenTrawl
contract settles.

The current build supports archive import, live X API sync with
budget metering, status, browsing, search, open, stats and doctor.

## Build

```sh
go build ./...
go test ./...
```
