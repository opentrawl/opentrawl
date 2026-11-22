---
written_by: ai
---

# Twitter (X)

The Twitter crawler archives a person's own X posts, bookmarks, likes, mentions
and direct messages in local SQLite (`~/.opentrawl/twitter/twitter.db`).

It supports three explicit source paths:

1. **Web session (`source = "web_session"`):** strictly read-only HTTP sync
   using your X browser session cookies (`auth_token` and `ct0`) stored at
   `~/.opentrawl/twitter/session.toml` (`0600`). Syncs bookmarks, authored
   posts, mentions, likes and direct messages (both accepted and message
   requests) with incremental stop-at-known-ID pagination, per-request pacing,
   a 15-minute cooldown, per-endpoint rate-limit tracking and a session-expiry
   circuit breaker.
2. **Official X API v2 (`source = "official_api"`):** refreshes authored posts,
   mentions, likes, bookmarks and engagement counts with OAuth credentials in
   `~/.opentrawl/twitter/credentials.toml` (`0600`), metered locally against a
   configured monthly USD cap.
3. **Official X archive export:** seeds historical authored posts and likes from
   a downloaded X archive zip or folder.

OpenTrawl never falls back silently between free web-session cookies and paid
official X API calls.

## Commands

```sh
trawl twitter connect --from-env
trawl twitter disconnect
trawl x import archive PATH
trawl update x [--force] [--full] [--max-pages N]
trawl x tweets --limit 20
trawl x bookmarks --limit 20
trawl x likes --limit 20
trawl x mentions --limit 20
trawl conversations --trawler x
trawl messages --trawler x --limit 20
trawl x stats --window 30d --by likes --limit 10
trawl x spend
trawl search "words" --trawler x
trawl open LINK
```

## Network and privacy boundary

- Web-session updates talk only to `x.com` (and `abs.twimg.com` JS bundles to
  discover current GraphQL query IDs).
- Official API updates talk only to `api.x.com`.
- Import, search, browse, conversations, messages and open are strictly local.
- Cookies and OAuth tokens never appear in output, errors or logs.
