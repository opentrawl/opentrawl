# AGENTS.md

`trawlkit` is the shared Go stdlib for the crawler family: provider-neutral
mechanics that make each crawler smaller and more uniform.

The boundary rule: code moves into `trawlkit` only when it is provider
neutral and at least two crawlers use it. It owns config paths, SQLite
hygiene, snapshot helpers, sync state, output and render helpers,
control/status metadata, and safe read-only cache
snapshots. It does not own provider API clients, auth flows, app database
schemas, provider cache parsing, app FTS bodies or ranking, or app CLI
command contracts — those stay in the crawlers.

Tests use temp dirs and temp SQLite files only; never touch live archives
under `~/.opentrawl/` or app runtime stores.
