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

Install from Homebrew after the first tagged release:

```bash
brew install steipete/tap/clawdex
```

Or build locally:

```bash
go install github.com/openclaw/clawdex/cmd/clawdex@latest
```

```bash
clawdex init ~/.opentrawl/clawdex/contacts
clawdex config set repo_path ~/.opentrawl/clawdex/contacts
clawdex config set git.remote https://github.com/<you>/backup-clawdex.git
```

Or set the backup remote during initialisation:

```bash
clawdex init ~/.opentrawl/clawdex/contacts --remote https://github.com/<you>/backup-clawdex.git
```

`init` creates a data repo:

```text
clawdex.toml
people/
index/
.clawdex/repairs/
```

Config is stored at `~/.opentrawl/clawdex/config.toml` by default. `--repo DIR` or
`CLAWDEX_REPO=DIR` overrides the configured contacts repo for one run.

## Examples

```bash
clawdex person list
clawdex person show sally
clawdex who sally
clawdex search dinner
clawdex export vcard --all --include-avatars -o contacts.vcf
clawdex git status
clawdex git commit -m "sync: update contacts"
clawdex git push
```

## Imports and sync safety

Imports write only to the local markdown data repo.

```bash
clawdex import apple --dry-run
clawdex import apple --avatars
clawdex import google --account you@example.com --dry-run
clawdex import google --account you@example.com --avatars --dry-run
clawdex import birdclaw --min-messages 4 --dry-run
clawdex import discrawl --min-messages 4 --dry-run
clawdex import contacts --from imsgcrawl --dry-run
clawdex import contacts --from-all --dry-run
```

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

Crawler contact imports run `<crawler> contacts export --json`. They update
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

Preview repairs:

```bash
clawdex doctor --repair --dry-run
```

Apply repairs:

```bash
clawdex doctor --repair
```

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

