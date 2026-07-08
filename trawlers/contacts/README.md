---
written_by: ai
---

# clawdex

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

Config is stored at `~/.opentrawl/contacts/config.toml` by default. Set
`repo_path` in that file, or use `CONTACTS_REPO=DIR` to point one run at a
different contacts repo. The runner owns global flags, so there is no
contacts-specific `--repo` flag.

## Examples

```bash
trawl contacts init /path/to/contacts
trawl contacts import apple --input contacts.ndjson
trawl contacts who Ada
trawl contacts who 'Ada Lovelace'
trawl contacts repair --dry-run
```

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

The old cross-crawler `import contacts` path has been removed. Import one
reviewed source at a time with `import apple`, `import google`, `import
birdclaw`, or `import discrawl`.

`trawl contacts sync apple` and `trawl contacts sync google` are preview-only
placeholders for now. Remote address-book writes need a conflict report before
they become active. Notes stay local-only and are never written to Apple or
Google.

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

`trawl contacts doctor` reports damage without writing. `trawl contacts repair`
writes repaired markdown, updates avatar metadata when possible, and rebuilds
the derived index. Use `--dry-run` to preview the repair.

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
