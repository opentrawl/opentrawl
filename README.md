---
written_by: ai
---

# OpenTrawl

One searchable archive of your digital life, on your machine.

OpenTrawl copies history from the apps you use into separate, local SQLite
archives, then searches them through one `trawl` command. In the beta, an agent
can find a message, note or person and open its source-owned context without
querying each app again.

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

The installed Mac app includes the complete CLI at
`/Applications/OpenTrawl.app/Contents/Helpers/trawl`; it does not require Go or
a source checkout. The app source is under `app/`. For development, the CLI and
crawlers can also be built and used from the checkout.

## Use OpenTrawl with an agent

OpenTrawl gives a coding agent searchable access to the local archive of your
messages, notes and contacts. Give an agent this instruction:

> My intent is for you to answer my questions from my local OpenTrawl archives.
> Use `/Applications/OpenTrawl.app/Contents/Helpers/trawl` as the `trawl` CLI.
> Start by running it with no arguments and with `--help`. You may use the
> read-only `status`, `search`, `open`, `chats`, `who` and source-specific read
> commands. Prefer normal text output; use `--json` only for a script. Do not
> sync, import, install anything, or change my agent configuration or `PATH`
> unless I ask.

## Use the archive

Run `trawl` to see the sources available in the beta and the first commands to
try. Run `trawl --help` for the complete cross-source surface.

```sh
trawl status
trawl search "boat trip"
trawl who "Avery"
trawl chats --with "Avery"
trawl open imessage:msg/8842
trawl telegram              # source-specific commands
trawl contacts person list  # people in the Contacts archive
trawl sync imessage telegram # explicitly refresh two archives
```

`status`, `search`, `open` and source-specific read commands use existing local
archives. `sync` is the explicit operation that refreshes them. Normal text is
the interface for people and agents. `--json` exists for scripts that need to
compose command output mechanically.

The cross-source commands use stable exit statuses: `0` means complete, `1`
means failed, `2` means the command was used incorrectly, and `3` means the
result is partial but stdout is still usable. For `who`, `4` means more than
one person matched and `5` means no person matched. On a partial text result,
stderr explains which sources were incomplete; JSON keeps the same result and
failure details together on stdout.

Search results carry stable, source-prefixed refs such as
`imessage:msg/8842`. `open` returns a bounded source-owned record anchored at
the matching item.

## Beta sources

One Go registry decides which sources every CLI and app-helper operation can
use. The beta exposes these sources by default:

| Source | Directory | Archive input |
| --- | --- | --- |
| iMessage | [`trawlers/imessage`](trawlers/imessage) | Apple Messages |
| WhatsApp | [`trawlers/whatsapp`](trawlers/whatsapp) | WhatsApp Desktop |
| Telegram | [`trawlers/telegram`](trawlers/telegram) | Telegram for macOS |
| Notes | [`trawlers/notes`](trawlers/notes) | Apple Notes |
| Contacts | [`trawlers/contacts`](trawlers/contacts) | Apple Contacts and identities exported by messaging sources |

Gmail, Calendar, Photos and X remain compiled for development but are not part
of the beta interface. A developer can expose all compiled sources for one
local command or process with `OPENTRAWL_ALL_SOURCES=1`. This override is not a
second product configuration and is not enabled by the installed beta.

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
