---
written_by: ai
---

# trawlkit

Shared Go infrastructure for local-first crawler archives.

`trawlkit` is not a universal Slack, Discord, Notion, or GitHub crawler. It is
the reusable foundation beneath those tools: SQLite hygiene, TOML config
defaults, sync state, CLI output helpers, control/status metadata, and
safe desktop-cache snapshot utilities.

See `AGENTS.md` for the ownership boundary.

## Packages

- `cache`: safe read-only local cache and SQLite DB/WAL/SHM snapshot helpers.
- `config`: standard TOML config paths, opt-in platform-native runtime dirs,
  migration-safe legacy path fallback, and token diagnostics.
- `conformance`: reusable test helpers that assert a crawler's CLI output matches the shared contract shape (human and JSON).
- `control`: crawl app metadata, command manifests, status payloads, contact-export contracts, and database inventory for launchers and automation.
- `flags`: the shared `--limit` CLI flag contract, so every crawler resolves it the same way.
- `log`: writes crawl run logs in the shared OpenTrawl grammar, so every crawler's log is diagnosable the same way.
- `mirror`: clone/init/pull/commit/push helpers plus non-mutating fetch, immutable tags, Git-object reads, and history inspection for private snapshot repos.
- `model`: bounded model-request mechanics with retry, timeout and concurrency guardrails; callers own provider selection and configuration.
- `output`: text/json/log output helpers, and the one `{"error":{code,message,remedy}}` JSON error envelope.
- `render`: shared human-output rendering — cards, tables, lists, transcripts and status pages — so every crawler's terminal output looks like one product.
- `shortref`: short, human-typeable aliases for a crawler's long internal refs, plus the SQLite index that resolves them back.
- `state`: the one sync-state store (cursors, freshness, scalar markers), plus a cursor-shaped adapter for a crawler that already had that table layout.
- `store`: SQLite open/read-only/transaction/query helpers plus safe FTS5 term and optimization helpers.
- `usage`: renders the shared `--help` text shape for crawler CLIs.
- `whomatch`: the shared resolver rules a crawler's `who` verb uses to match a person or sender identity.

## Consumers

- The registered crawlers and `trawl` consume `trawlkit` through the monorepo's
  Go workspace. There is no separate per-source install or release step.
- The apps keep provider schemas, auth, desktop/API parsing, privacy filters,
  and user-facing CLI contracts. `trawlkit` owns only the reusable mechanics.

## Safety

Library tests use temporary directories. They do not touch a crawler's real
archive under `~/.opentrawl/<crawler>` (for example `~/.opentrawl/twitter`,
`~/.opentrawl/imessage` or `~/.opentrawl/photos`).
