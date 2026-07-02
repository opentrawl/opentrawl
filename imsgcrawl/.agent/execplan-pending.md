---
written_by: ai
---

# Slice 1: expose iMessage contact-export through crawlkit control

This ExecPlan is a living document. The sections `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` must be kept up to date as work proceeds.

This plan follows `.agent/PLANS.md` in this repository.

## Purpose / Big Picture

After this slice, clawdex can ask a local iMessage crawler for phone-based contact rows through the same small `contact-export` contract used by the Telegram and WhatsApp crawlers. This matters because the Messages database contains contact-ish handles that are not all present in Apple Contacts or clawdex today. The user-visible proof is a local command:

    imsgcrawl --json metadata
    imsgcrawl --json status
    imsgcrawl --json contacts export

`metadata` advertises a read-only crawlkit control command, `status` reports aggregate database counts without leaking handles, and `contacts export` emits only:

    {
      "contacts": [
        {
          "display_name": "+15550100",
          "phone_numbers": ["+15550100"]
        }
      ]
    }

The design is intentionally small. The crawler owns the Messages SQLite read and contact extraction policy; clawdex owns canonical people, matching, and human cleanup. This avoids making crawlkit carry iMessage-specific schema knowledge and avoids expanding the contact-export v0 contract before all producers and consumers change together.

## Progress

- [x] (2026-06-06 00:58+02:00) Created `imsgcrawl` as the normal checkout from `git@github.com:openclaw/imsgcrawl.git`.
- [x] (2026-06-06 01:05+02:00) Added repo rules, README, Go module, crawlkit control metadata, read-only Messages snapshotting, status, contact export, and fixture tests.
- [x] (2026-06-06 01:12+02:00) Ran `nix shell nixpkgs#go -c go mod tidy`.
- [x] (2026-06-06 01:13+02:00) Ran `nix shell nixpkgs#go -c go test ./...`.
- [x] (2026-06-06 01:14+02:00) Built `imsgcrawl` into `/tmp` and smoked `metadata`, `status`, and `contacts export` against the maintainer's real local Messages database.
- [x] (2026-06-06 01:22+02:00) Reviewed the diff for simplicity, privacy, and v0 contract drift; tightened phone-handle filtering and removed public-doc personal path/name leaks.

## Surprises & Discoveries

- Observation: the live Messages database could be read through an immutable SQLite URI in earlier probing, but a normal sqlite read failed once. Evidence: the implementation snapshots `chat.db`, `chat.db-wal`, and `chat.db-shm` into a temporary directory before opening read-only.

- Observation: the real local contact export produced 386 deduped phone contacts while status reported 447 phone-syntax handle rows. Evidence: the export groups by clawdex-style normalized phone digits, so duplicate source spellings collapse into one contact row.

- Observation: an adversarial review found that "any handle containing a digit" was too broad for export because opaque alphanumeric handles could look numeric enough to pass. Evidence: `LooksPhoneLike` now accepts short codes and normal phone punctuation but rejects values with letters such as fixture `opaque123`.

## Decision Log

- Decision: Slice 1 exports phone handles only and does not add `email_addresses`.
  Rationale: clawdex, telecrawl, and wacrawl currently share a v0 contract with only `display_name` and `phone_numbers`. iMessage has real email handles, but expanding the contract belongs in a later coordinated change.
  Date/Author: 2026-06-06 / Codex.

- Decision: Use phone fallback display names only for the v0 contact-export contract, and revisit this before clawdex treats crawler names as canonical human names.
  Rationale: named-only export would mostly re-export Apple Contacts and miss the point of surfacing iMessage-only contacts, but a phone number is not a human name. The safer later shape may be an unnamed phone-only contact, once the shared contract and clawdex importer can represent that.
  Date/Author: 2026-06-06 / Codex.

- Decision: Deduplicate by clawdex-style normalized phone digits and choose the most recent Messages handle row.
  Rationale: this matches clawdex's comparison shape without adding country assumptions or a phone-number library. Most recent source evidence is the simplest tie-breaker for duplicate source handles.
  Date/Author: 2026-06-06 / Codex.

- Decision: Accept short codes but reject opaque alphanumeric handles in Slice 1.
  Rationale: the user explicitly warned against naive length-based phone falsehoods, so short numeric service contacts remain valid. But strings containing letters are not safe phone values for a phone-only export contract.
  Date/Author: 2026-06-06 / Codex.

## Outcomes & Retrospective

The first slice is implemented and validated locally. The review found and fixed one over-broad phone-handle filter plus public-doc wording that named a local user/path. No known blocker remains for the Slice 1 scaffold.

## Context and Orientation

This repository is a new Go crawler. The relevant files are:

- `cmd/imsgcrawl/main.go`: process entrypoint.
- `internal/cli/cli.go`: command parsing and output.
- `internal/cli/control.go`: crawlkit control manifest.
- `internal/messages/snapshot.go`: safe temporary copy of the live Messages SQLite files.
- `internal/messages/read.go`: status and contact extraction queries.
- `internal/cli/cli_test.go`: fixture-backed CLI and contract tests.

The Messages database is Apple's local SQLite file at `~/Library/Messages/chat.db`. A handle is a source identifier in that database, usually a phone number, email address, or opaque service value. `contact-export` is the small machine contract clawdex already consumes from other crawlers: root key `contacts`, with each contact carrying `display_name` and `phone_numbers` only.

## Plan of Work

Keep all Messages-specific knowledge inside `internal/messages`. The CLI should only know that it can ask for a status report or exported contacts. The snapshot module hides the live SQLite sequencing: callers do not need to know about `-wal` and `-shm` sidecars. The contact exporter hides dedupe, phone normalization, and name fallback so clawdex and the control manifest see one simple command.

Do not add sync, archive storage, search, TUI, or email export in this slice. Those are Slice 2.

## Concrete Steps

From the repository root, run:

    go mod tidy
    go test ./...
    go run ./cmd/imsgcrawl --json metadata
    go run ./cmd/imsgcrawl --json status
    go run ./cmd/imsgcrawl --json contacts export

For public proof, use only fixture outputs or aggregate counts. If the user asks for raw local output in chat, show the command and output in chat, but do not commit that output.

## Validation and Acceptance

Acceptance requires:

- `go test ./...` passes.
- `metadata` JSON has `schema_version: crawlkit.control.v1`.
- `metadata.commands.contact-export` has `json: true`, no `mutates`, and argv `["imsgcrawl","--json","contacts","export"]`.
- `status` reports counts for handles, chats, messages, phone handles, email handles, and other handles without listing private values.
- `contacts export` emits only root `contacts` and per-contact `display_name` plus `phone_numbers`.
- Real local smoke proves that the command can read the user's Messages DB through the snapshot path.

## Idempotence and Recovery

The commands are read-only. Snapshot files are created under a temporary directory and removed after each command. Re-running tests recreates fixture SQLite databases in test temp dirs. If a live Messages read fails due to macOS privacy permissions, report the exact error and do not bypass it by changing permissions silently.

## Artifacts and Notes

The committed tests create fake rows only. They include an email handle and an opaque handle to prove v0 skips them, and two phone handles that normalize to the same value to prove most-recent dedupe.

## Interfaces and Dependencies

Use `github.com/openclaw/crawlkit/control` for manifest and status count names. Use `github.com/openclaw/crawlkit/store.OpenReadOnly` with `modernc.org/sqlite` for read-only SQLite access. The exported Go type is:

    type ExportedContact struct {
        DisplayName  string   `json:"display_name"`
        PhoneNumbers []string `json:"phone_numbers"`
    }

The public CLI contract is the JSON output, not the internal Go type.

Revision note: Initial plan created with the first implementation pass so the repo is restartable from this file.

Revision note: Updated after `go mod tidy`, `go test ./...`, real `metadata` and `status` smokes, contact-export aggregate smoke, and `/tmp` binary build smoke passed.

Revision note: Removed an exact local checkout path from the plan so the public repo does not contain a private machine-specific path.

Revision note: Updated after review tightened phone-handle filtering to allow short codes while rejecting opaque alphanumeric handles; reran `go test ./...`, `go vet ./...`, real `status`, and real contact-export aggregate smoke.

Revision note: Updated after Josh flagged phone-number display names as risky for clawdex canonical naming; v0 keeps compatibility, but the design debt is explicit.
