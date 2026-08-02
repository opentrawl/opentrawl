---
written_by: ai
---

# OpenTrawl

One searchable archive of your digital life, on your machine.

OpenTrawl copies history from the apps you use into separate, local SQLite
archives, then searches them through one `trawl` command. A person or an agent
can find a message, conversation, note, person or calendar event and open its
surrounding context without querying each app again.

App access and archives stay local. Read commands do not change the original
apps. A trawler that uses a remote service must state what leaves the Mac and
send only the input needed for that operation.

## Build OpenTrawl

The development environment uses [devenv](https://devenv.sh). Install Nix,
devenv and [direnv](https://direnv.net), then run:

```sh
git clone https://github.com/opentrawl/opentrawl
cd opentrawl
direnv allow                 # or: devenv shell
scripts/dev-bin              # builds the CLI and trawlers into .dev/bin
```

The installed Mac app includes the complete CLI at
`/Applications/OpenTrawl.app/Contents/Helpers/trawl`; it does not require Go or
a checkout. The app code is under `app/`. For development, the CLI and trawlers
can also be built and used from the checkout.

### Run a Mac development candidate

From the repository root, build and launch one isolated candidate with a new,
descriptive name:

```sh
app/scripts/dev-run --candidate NAME
```

The app is written to
`~/Library/Developer/OpenTrawl/Builds/NAME/OpenTrawl.app`. Candidate names are
not overwritten, so use a new name for each build. OpenTrawl does not remove
older candidates automatically.

Every candidate uses the bundle ID `org.opentrawl.trawl` and the persistent
`OpenTrawl Dev Signing` identity. This preserves one OpenTrawl permission
identity across development builds. The command does not replace
`/Applications/OpenTrawl.app`; install the chosen candidate there when it is
ready for integrated proof.

## Use OpenTrawl with an agent

OpenTrawl gives a coding agent searchable access to the local archive of your
messages, notes, contacts and calendar events. Give an agent this instruction:

> My intent is for you to answer my questions from my local OpenTrawl archives.
> Use `/Applications/OpenTrawl.app/Contents/Helpers/trawl` as the `trawl` CLI.
> Start by running it with no arguments and with `--help`. You may use the
> read-only `status`, `search`, `who`, `conversations`, `messages`, `open` and
> trawler commands. Use the normal text output. Do not run `update`, import or
> install anything, or change my agent configuration or `PATH`, unless I ask.

## Use the archive

Set `TRAWL` for the current terminal, then run it with no arguments to see what
you can search. Use the installed helper or the development build:

```sh
TRAWL=/Applications/OpenTrawl.app/Contents/Helpers/trawl
# TRAWL=./.dev/bin/trawl

"$TRAWL"
"$TRAWL" --help
"$TRAWL" status
"$TRAWL" search "boat trip"
"$TRAWL" who "Avery"
"$TRAWL" conversations --with "Avery"
"$TRAWL" messages --conversation LINK
"$TRAWL" open LINK
"$TRAWL" contacts people
"$TRAWL" calendar events
"$TRAWL" update imessage telegram
```

All commands except `update` use the existing local archives. `update` gets new
items from the selected apps. Normal text is the interface for people and agents.
The Mac app uses the same typed protobuf contract.

Root commands use stable exit statuses: `0` means complete, `1` means failed,
`2` means the command was used incorrectly, and `3` means the result is partial
but stdout is still usable. On a partial result, stderr names the trawlers that
did not complete.

Search and list results show a stable link. Use that link directly with
`"$TRAWL" open LINK`; you do not need to add a trawler name. Internal record
identities stay inside the typed and storage contracts.

## Available trawlers

One Go registry decides which trawlers every CLI and Mac app operation can use:

| Trawler | Directory | Archive input |
| --- | --- | --- |
| iMessage | [`trawlers/imessage`](trawlers/imessage) | Apple Messages |
| WhatsApp | [`trawlers/whatsapp`](trawlers/whatsapp) | WhatsApp Desktop |
| Telegram | [`trawlers/telegram`](trawlers/telegram) | Telegram for macOS |
| Notes | [`trawlers/notes`](trawlers/notes) | Apple Notes |
| Contacts | [`trawlers/contacts`](trawlers/contacts) | Apple Contacts and identities found by messaging trawlers |
| Calendar | [`trawlers/calendar`](trawlers/calendar) | Apple Calendar |

The Mac app catalogue marks Gmail, Photos and Twitter (X) as coming soon.
Normal CLI commands do not include them.

## Product contracts

- [Vision](docs/vision.md) explains the enduring product direction and design
  boundaries.
- [Trawler control contract](docs/contract.md) defines the shared typed seam.
- [Mac app contract](docs/mac-app.md) defines search and open behaviour in the
  human interface.
- Trawler READMEs document app access, storage and commands.

Shared provider-neutral Go mechanics live in [`trawlkit`](trawlkit). Archive
schemas, authentication and import logic stay with their trawler.

## Contributing safely

Read [AGENTS.md](AGENTS.md) before changing the repository. It is public:
never commit personal archives, real messages, contacts, locations, account
identifiers or archive-derived counts. Real archive proof stays outside this
repository. Do not replace it with synthetic archive data, fixtures or examples.

Run `scripts/check-clean` before every commit.

## Licence

The monorepo is MIT licensed; see [LICENSE](LICENSE). Forked trawler directories
retain their upstream licences and copyright notices.
