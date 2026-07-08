---
written_by: ai
---

# 🧩 clawdex

Local-first contact crawler and markdown archive CLI.

`clawdex` is a local-first contact crawler and markdown archive CLI. The app
lives in this repo; your contacts live in a separate private Git-backed
markdown repo.

Contacts stay local by default. To back up or sync across machines, configure a
private Git remote you own:

```bash
https://github.com/<you>/backup-clawdex.git
```

## Setup

The standalone `clawdex` binary is gone. Contact commands are not exposed until
clawdex is registered behind `trawl`.

`init` creates a data repo:

```text
clawdex.toml
people/
index/
.clawdex/repairs/
```

Config is stored at `~/.opentrawl/contacts/config.toml` by default. `--repo DIR` or
`CLAWDEX_REPO=DIR` overrides the configured contacts repo for one run.

## Examples

No standalone command examples are listed.

## Imports and sync safety

Imports write only to the local markdown data repo.

Apple direct import reads the local macOS AddressBook SQLite databases under
Full Disk Access. Linux builds still support markdown, notes, search, Git,
Google via `gog`, and vCard export.

Avatar imports are opt-in with `--avatars`. Apple reads thumbnails from
the same AddressBook databases. Google uses
`gog contacts raw --person-fields photos`, fetches the selected photo URL
bytes, then stores thumbnails as local files under each person directory and
records only metadata in `person.md`. Manual avatars are not overwritten by
Apple/Google imports.

Birdclaw and Discrawl DM imports read local archives only. They import DM
conversations with more than `--min-messages` messages, add source-specific
tags, and store stable pointers under `accounts.x` or `accounts.discord`.

Crawler contact imports read `trawl <crawler> contacts export --json`. They update
existing people when a phone, email, or handle matches the sqlite index. They
do not create person files for unknown contacts. Unknown contacts are staged
one line at a time in `index/unmatched.md` for deliberate review.

`sync apple` and `sync google` are preview-only placeholders for now. Remote
address-book writes need a conflict report before they become active. Notes stay
local-only and are never written to Apple or Google.

## Markdown Repair

People and note files use YAML frontmatter plus a Markdown body. `clawdex`
parses strictly first, then does best-effort repair when frontmatter is damaged:

- salvage known scalar keys such as `id`, `name`, `created_at`, and note fields
- infer missing IDs and timestamps
- preserve the Markdown body
- copy the original file under `.clawdex/repairs/`
- append damaged metadata to the body under `Recovered metadata`
- warn about missing or stale avatar files and repair avatar metadata when the
  image still exists

Repair commands are not exposed without a command surface.

## Storage

```text
people/
  sally-o-malley/
    person.md
    avatars/
      avatar.jpg
    notes/
      2026-05-08T09-15-00Z-whatsapp.md
    attachments/
index/
  index.db
```

`index/index.db` is derived and rebuildable. Clawdex refreshes it on reads when
person markdown changes. Markdown is canonical.
