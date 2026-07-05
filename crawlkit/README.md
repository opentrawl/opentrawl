# 🧱 crawlkit

Shared Go infrastructure for local-first crawler archives.

`crawlkit` is not a universal Slack, Discord, Notion, or GitHub crawler. It is
the reusable foundation beneath those tools: SQLite hygiene, TOML config
defaults, sync state, CLI output helpers, control/status metadata, and
safe desktop-cache snapshot utilities.

See `AGENTS.md` for the ownership boundary.

## Packages

- `backup`: age-encrypted JSONL/Gzip shards, backup manifests, recipient/identity helpers, history listing, and historical-ref restore verification.
- `cache`: safe read-only local cache and SQLite DB/WAL/SHM snapshot helpers.
- `config`: standard TOML config paths, opt-in platform-native runtime dirs,
  migration-safe legacy path fallback, and token diagnostics.
- `conformance`: reusable test helpers that assert a crawler's CLI output matches the shared contract shape (human and JSON).
- `control`: crawl app metadata, command manifests, status payloads, contact-export contracts, and database inventory for launchers and automation.
- `flags`: the shared `--limit`/`--all` CLI flag contract, so every crawler resolves it the same way.
- `log`: writes crawl run logs in the shared OpenTrawl grammar, so every crawler's log is diagnosable the same way.
- `mirror`: clone/init/pull/commit/push helpers plus non-mutating fetch, immutable tags, Git-object reads, and history inspection for private snapshot repos.
- `model`: a local Ollama-compatible model client with retry, timeout, and concurrency guardrails for extraction and classification work.
- `output`: text/json/log output helpers, and the one `{"error":{code,message,remedy}}` JSON error envelope.
- `registry`: the one place that knows which crawlers are installed; probes each one's `metadata --json` into a manifest for trawl.
- `render`: shared human-output rendering — cards, tables, lists, transcripts, status and doctor pages — so every crawler's terminal output looks like one product.
- `shortref`: short, human-typeable aliases for a crawler's long internal refs, plus the SQLite index that resolves them back.
- `state`: the one sync-state store (cursors, freshness, scalar markers), plus a cursor-shaped adapter for a crawler that already had that table layout.
- `store`: SQLite open/read-only/transaction/query helpers plus safe FTS5 term and optimization helpers.
- `usage`: renders the shared `--help` text shape for crawler CLIs.
- `whomatch`: the shared resolver rules a crawler's `who` verb uses to match a person or sender identity.

## Downstream apps

- The 8 crawlers in this monorepo — `birdcrawl`, `calcrawl`, `clawdex`,
  `gogcrawl`, `imsgcrawl`, `photoscrawl`, `telecrawl`, and `wacrawl` — plus
  `trawl`, the cross-source CLI that fronts all of them, consume `crawlkit`
  through the Go workspace on `main`. There is no separate install or
  release step.
- The apps keep provider schemas, auth, desktop/API parsing, privacy filters,
  and user-facing CLI contracts. `crawlkit` owns only the reusable mechanics.

## Safety

Library tests use temporary directories. They do not touch a crawler's real
archive — for most crawlers that is `~/.opentrawl/<crawler>` (for example
`~/.opentrawl/birdcrawl` or `~/.opentrawl/imsgcrawl`); photoscrawl instead
uses the platform-native data/cache/state directories.
