---
written_by: ai
---

# Imports

Imports project an external contact graph into the same markdown shape
clawdex uses everywhere else. They are local-only: every import only writes
to your data repo. No address book on Apple, Google, X, or Discord is
mutated by any `clawdex import` subcommand.

If you want to push changes back into Apple Contacts or Google Contacts, see
[Sync](#sync-preview-only) below — it's preview-only today.

## What gets written

Each import reports its changes as a labelled table (`changes` in
`--json`):

```text
Import: 3 contact changes.

action  who             id
create  Sally O'Malley  person_...
update  Bo Burnham      person_...
stage   Unknown Sender  -
```

Combine with `--dry-run` to preview without writing:

```bash
clawdex import apple --dry-run
clawdex import google --account you@example.com --dry-run
```

## Apple Contacts

```bash
clawdex import apple --dry-run
clawdex import apple
clawdex import apple --avatars
clawdex import apple --input ~/Desktop/contacts.json
```

- Default source is the local macOS AddressBook database under
  `~/Library/Application Support/AddressBook`. It uses Full Disk Access, not
  the separate Contacts permission.
- `--input PATH` reads JSON or NDJSON instead — useful on Linux, in CI,
  or when round-tripping a snapshot.
- `--avatars` imports thumbnail bytes. Without it, only structured fields
  are imported.

Manual avatars set with [`clawdex person avatar set`](avatars.md) are never
overwritten. Tags, notes, and any custom frontmatter you've added by hand
are preserved.

## Google Contacts

```bash
clawdex import google --account you@example.com --dry-run
clawdex import google --account you@example.com
clawdex import google --account you@example.com --avatars
```

The Google adapter shells out to [`gog`](https://github.com/steipete/gogcli),
the local-first Google Workspace CLI. You need to be authenticated there
first:

```bash
gog auth credentials ~/Downloads/client_secret_*.json
gog auth add you@example.com --services contacts
```

If `--account` is omitted, clawdex falls back to `google.default_account`
from your config — set it once with:

```bash
clawdex config set google.default_account you@example.com
```

`--avatars` fetches photo bytes through `gog contacts raw --person-fields photos`
and stores them locally.

## Birdclaw — X / Twitter DMs

```bash
clawdex import birdclaw --dry-run
clawdex import birdclaw --min-messages 4
clawdex import birdclaw --db ~/.birdclaw/birdclaw.sqlite
```

Reads from your local [birdclaw](https://github.com/steipete/birdclaw)
SQLite archive. For each DM thread above the `--min-messages` threshold,
clawdex creates or updates a person, stores the X handle as a stable
pointer under `accounts.x`, and adds a source-specific tag.

The default DB path is `~/.birdclaw/birdclaw.sqlite`. Threads with fewer
than `--min-messages` messages are skipped — most of those are one-shot
spam or intros that died.

## Discrawl — Discord DMs

```bash
clawdex import discrawl --dry-run
clawdex import discrawl --min-messages 4
clawdex import discrawl --db ~/.discrawl/discrawl.db
```

Same shape as birdclaw, but reads from
[discrawl](https://github.com/steipete/discrawl)'s SQLite cache. Discord
handles land under `accounts.discord`.

## Crawler contacts

```bash
clawdex import contacts --from telecrawl --dry-run
clawdex import contacts --from wacrawl --dry-run
clawdex import contacts --from /path/to/crawler --dry-run
clawdex import contacts --from-all --dry-run
```

Reads a local crawler's crawlkit metadata, checks that it declares a read-only
JSON contacts export, then runs:

```bash
<crawler> contacts export --json
```

This is the v0 machine contract for source crawler contacts:

- metadata schema is `crawlkit.control.v1`
- command key can be `contact-export` or `contacts_export`
- command is read-only, advertises `json: true`, and declares `contacts export`
- payload root is `contacts`
- each contact has `display_name` plus one or more identifiers:
  `phone_numbers`, `emails`, `accounts`, or `handles`

Source crawlers own source-native extraction and privacy filtering. Clawdex
owns canonical people, markdown storage, matching, and human edits.

Crawler contact imports match existing people through the sqlite index, using
normalised phone numbers, email addresses, and handles such as
`telegram:alice`. They never merge by name alone.

If a crawler contact matches an existing person, clawdex adds any missing
identifiers to that person's `person.md`. The source evidence is recorded in
frontmatter with `last_seen_at`.

If a crawler contact does not match, clawdex does not create a person file.
It appends one review line to `index/unmatched.md`. Promote that contact by
editing an existing `person.md` or adding a new one, then rerun the import.

If one exported crawler contact contains a phone already owned by a different
person, clawdex leaves that conflicting phone off the matched person instead of
creating an automatic cross-person join.

When a crawler contact matches an existing person, clawdex records that source
under the person's local markdown frontmatter:

```yaml
sources:
  telecrawl:
    names: ["Ada Example"]
    phones: ["15550100"]
    last_seen_at: 2026-07-02T10:00:00Z
  wacrawl:
    names: ["Ada Example"]
    phones: ["+1 555 0100"]
    last_seen_at: 2026-07-02T10:00:00Z
```

That source evidence is local-only and stable across repeated imports. A
second import of the same crawler payload does not rewrite person markdown or
duplicate unmatched staging lines.

## Sync (preview-only)

```bash
clawdex sync apple
clawdex sync google --account you@example.com
```

These commands exist as placeholders. They report:

```text
status: remote writes not implemented yet; use import apple for local
markdown projection
```

Two-way sync requires a conflict report you can read before anything is
written remotely; that report doesn't exist yet, so the writes don't
either. Until it lands, treat clawdex as a one-way mirror: imports come
in, [vCard export](vcard-export.md) goes out.

## Related pages

- [People](people.md), [Avatars](avatars.md)
- [Markdown Storage](markdown-storage.md) — the shape imports project into
- [Git Sync](git-sync.md) — committing the import diff
- [Config](config.md) — `google.default_account`, repo path
