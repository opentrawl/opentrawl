# trawl-beeper

Standalone MIT external OpenTrawl plugin that archives full message history from Beeper Desktop's local read-only HTTP API (`http://127.0.0.1:23373`).

## What it archives

- Connected Beeper accounts and network names (for example Instagram, LinkedIn, WeChat, Slack, Discord, Messenger)
- Conversations, participants, and full paginated message history (`GET /v1/chats` and `GET /v1/chats/{chatID}/messages`)
- Incremental high-water-mark sync per chat on subsequent updates, with resumable historical backfill on first sync
- Unified `index.db` export so Beeper messages join cross-source `trawl search`, `trawl around`, `trawl open`, `trawl who`, and semantic search

By default, networks that OpenTrawl already crawls natively (`WhatsApp`, `Telegram`, `iMessage`, `SMS`) are skipped so messages are not duplicated. Configure `include_networks` or `exclude_networks` in `<stateRoot>/beeper/config.toml` to change which networks are archived.

All requests to Beeper Desktop are strictly read-only (`GET` only). No Full Disk Access is required.

## Build and install

Build with SQLite FTS5 enabled:

```sh
CGO_ENABLED=1 go build -tags=sqlite_fts5 -o ./trawl-beeper ./cmd/trawl-beeper
```

Stage the plugin with explicit capability approvals (self-hosted loopback access to Beeper Desktop's local API and isolated Keychain secret storage for the Bearer token):

```sh
trawl plugin install ./trawl-beeper \
  --allow-self-hosted 127.0.0.1:23373=127.0.0.1/32 \
  --allow-read-secret beeper_access_token \
  --allow-write-secret beeper_access_token
```

Connect your Beeper Desktop read-only Bearer token (stored in OpenTrawl's isolated per-plugin Keychain namespace):

```sh
trawl beeper connect --file ~/.config/beeper-readonly/token
```

Run an update and search:

```sh
trawl update beeper
trawl status beeper
trawl beeper networks
trawl search --trawler beeper "<query>"
```

## Configuration

Optional `<stateRoot>/beeper/config.toml`:

```toml
api_base_url = "http://127.0.0.1:23373"
include_networks = ["Instagram", "LinkedIn"]
exclude_networks = ["WhatsApp", "Telegram", "iMessage", "SMS"]
request_timeout_seconds = 15
max_message_pages_per_chat = 0
```

When you cannot use the macOS Data Protection Keychain (for example in an unsigned local development build), you may pass `--allow-read-path <path>` at install time and set `token_file = "<path>"` in `config.toml` or pass `--file <path>` to `trawl update beeper`.
