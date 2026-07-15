---
written_by: ai
---

# OpenTrawl

One searchable archive of your digital life, on your machine.

OpenTrawl copies history from the services you use into separate, local SQLite
archives, then searches them through one `trawl` command. An agent can find the
right message, email, event, note, photo or post and open its source-owned
context without querying each service again.

Source access and archives stay local. A read command may maintain a derived
index, but it never syncs or changes a source app. Features that deliberately
use a remote service must expose that boundary and send only the bounded input
needed for the operation.

## Build from source

The development environment uses [devenv](https://devenv.sh). Install Nix,
devenv and [direnv](https://direnv.net), then run:

```sh
git clone https://github.com/opentrawl/opentrawl
cd opentrawl
direnv allow                 # or: devenv shell
scripts/dev-bin              # builds the CLI and crawlers into .dev/bin
```

This repository does not publish an end-user installer. The Mac app source is
under `app/`; the CLI and crawlers can be built and used from the checkout.

## Use the archive

Run `trawl` to see the sources compiled into the current build and the first
commands to try. Run `trawl --help` for the complete cross-source surface.

```sh
trawl status
trawl sync imessage telegram
trawl search "boat trip"
trawl search "invoice" --source gmail --after 2026-01-01
trawl open imessage:msg/8842
trawl telegram              # source-specific commands
```

`status`, `search`, `open` and source-specific read commands use existing local
archives. `sync` is the explicit operation that refreshes them. Add `--json` to
a command when a program or agent needs structured output.

Search results carry stable, source-prefixed refs such as
`imessage:msg/8842`. `open` returns a bounded source-owned record anchored at
the matching item.

## Sources

The current build registers these sources explicitly in Go:

| Source | Directory | Archive input |
| --- | --- | --- |
| iMessage | [`trawlers/imessage`](trawlers/imessage) | Apple Messages |
| WhatsApp | [`trawlers/whatsapp`](trawlers/whatsapp) | WhatsApp Desktop |
| Telegram | [`trawlers/telegram`](trawlers/telegram) | Telegram Desktop or Telegram for macOS |
| Gmail | [`trawlers/gmail`](trawlers/gmail) | an authenticated `gog` backup |
| Calendar | [`trawlers/calendar`](trawlers/calendar) | Apple Calendar |
| Contacts | [`trawlers/contacts`](trawlers/contacts) | reviewed contact imports |
| Photos | [`trawlers/photos`](trawlers/photos) | Apple Photos |
| Twitter (X) | [`trawlers/twitter`](trawlers/twitter) | an X archive and the X API |
| Notes | [`trawlers/notes`](trawlers/notes) | Apple Notes |

A new source is a Go crawler that implements the shared contract and is added
to the registry before the product is rebuilt. There is no public drop-in
plugin discovery path.

## Product contracts

- [Vision](docs/vision.md) explains the enduring product direction and design
  boundaries.
- [Crawler control contract](docs/contract.md) defines the shared source seam.
- [Mac app contract](docs/mac-app.md) defines search and open behaviour in the
  human interface.
- Source READMEs document source-specific access, storage and commands.

Shared provider-neutral Go mechanics live in [`trawlkit`](trawlkit). Source
schemas, authentication and import logic stay with their crawler.

## Contributing safely

Read [AGENTS.md](AGENTS.md) before changing the repository. It is public:
never commit personal archives, real messages, contacts, locations, account
identifiers or archive-derived counts. Tests and examples use synthetic data.

Run `scripts/check-clean` before every commit.

## Licence

The monorepo is MIT licensed; see [LICENSE](LICENSE). Forked crawler directories
retain their upstream licences and copyright notices.
