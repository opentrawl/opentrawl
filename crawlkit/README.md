# 🧱 crawlkit

![crawlkit banner](docs/assets/readme-banner.jpg)

Shared Go infrastructure for local-first crawler archives.

`crawlkit` is not a universal Slack, Discord, Notion, or GitHub crawler. It is
the reusable foundation beneath those tools: SQLite hygiene, TOML config
defaults, sync state, CLI output helpers, control/status metadata, and
safe desktop-cache snapshot utilities.

## Install

```bash
go get github.com/openclaw/crawlkit@latest
```

Go packages are published by tagging this repository. There is no separate
package registry step. See `docs/publishing.md` for the release commands.
See `docs/boundary.md` for the crawlkit-versus-app ownership boundary.

## Packages

- `config`: standard TOML config paths, opt-in platform-native runtime dirs,
  migration-safe legacy path fallback, and token diagnostics.
- `store`: SQLite open/read-only/transaction/query helpers plus safe FTS5 term and optimization helpers.
- `backup`: age-encrypted JSONL/Gzip shards, backup manifests, recipient/identity helpers, history listing, and historical-ref restore verification.
- `mirror`: clone/init/pull/commit/push helpers plus non-mutating fetch, immutable tags, Git-object reads, and history inspection for private snapshot repos.
- `state`: generic crawler cursor and freshness records, including mapped adapters for existing app table layouts.
- `output`: text/json/log output helpers.
- `control`: crawl app metadata, command manifests, status payloads, contact-export contracts, and database inventory for launchers and automation.
- `cache`: safe read-only local cache and SQLite DB/WAL/SHM snapshot helpers.

## Downstream apps

- `gitcrawl`, `discrawl`, `notcrawl`, `wacrawl`, `telecrawl`, and `slacrawl`
  consume `crawlkit` on `main`.
- The apps keep provider schemas, auth, desktop/API parsing, privacy filters,
  and user-facing CLI contracts. `crawlkit` owns only the reusable mechanics.

## Safety

Library tests use temporary directories. They do not touch app runtime stores
such as `~/.config/gitcrawl`, `~/.slacrawl`, `~/.discrawl`, or `~/.notcrawl`.
