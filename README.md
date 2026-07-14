---
written_by: ai
---

# OpenTrawl

One searchable archive of your digital life, on your machine.

Your history is scattered across apps that each lock it away: years of
iMessage, Telegram, WhatsApp, Gmail, calendars, notes. Finding one
conversation means searching five apps with five search boxes, and an AI
agent working for you can see none of it.

OpenTrawl crawls each service into a plain SQLite archive on your disk,
then puts one search over all of them. Nothing leaves your machine
unless you send it somewhere, and read commands never write.

The archives are built for agents too. Give an assistant access and it
can onboard into your life in one shot, the people around you, what is
going on, what changed this week, instead of you explaining yourself one
prompt at a time.

## The front door

Run `trawl` with no arguments and it tells you what it is and where to
start:

<!-- output of bare `trawl` at 53ddcc5b; regenerate when it drifts -->
```
Search your own life. Every installed crawler archives one source, and
trawl searches all of them at once.

Sources:
  imessage       iMessage chats and messages
  telegram       Telegram Desktop archive
  whatsapp       WhatsApp Desktop archive
  gmail          Gmail archive and Google Contacts export
  calendar       Apple Calendar events
  contacts       People merged from your other sources
  photos         Apple Photos library
  x (twitter)    X posts, likes, bookmarks and mentions
  notes          Apple Notes archive

Start here:
  trawl status                 your sources, and how fresh each one is
  trawl search "boat trip"     search every source, newest first
  trawl imessage               one source's own commands

Agents: add --json to any command for structured output.
Every flag and shared verb: trawl --help
```

## Quickstart

The toolchain comes from [devenv](https://devenv.sh), so you need Nix and
devenv installed, plus [direnv](https://direnv.net) for the per-terminal
setup. Everything builds from source into a repo-local bin directory, so
there are no global installs.

```sh
git clone https://github.com/opentrawl/opentrawl
cd opentrawl
direnv allow           # or: run `devenv shell` in this directory
scripts/dev-bin        # build every crawler and the trawl CLI into .dev/bin
```

`direnv allow` activates the devenv shell and puts `.dev/bin` on your
PATH, so `trawl` is on your PATH in every terminal you open here. Then
work against your own data:

```sh
trawl status                 # every source: state, freshness, counts
trawl imessage sync          # crawl one source into its archive
trawl search "boat trip"     # search every source, newest first
trawl open imessage:msg/8842 # expand any ref a search returned
```

`trawl status` and `trawl search` read only. `trawl <source> sync` is the
only command that fetches. Run `trawl doctor` if a source reports a
problem: every failing check names the exact fix.

## How it works

There is one crawler per service. Each crawler extracts its source into
its own local SQLite archive and speaks a small command contract:
`status`, `sync`, `search`, `open`, `doctor`, each with a `--json` mode.
That contract is the only coupling in the system: the CLI and the app
know each crawler through it, not through its internals. The full
specification is in [docs/contract.md](docs/contract.md).

Two surfaces sit on top of the contract:

- `trawl`, the command line tool that fronts every registered crawler and
  searches them all at once
- a Mac app that searches every source and opens each result at its exact
  match

The [Mac product contract](docs/mac-app.md) defines the app and its shared
search and open meanings.

The shared Go code both surfaces build on lives in `trawlkit`.

## Crawlers

Nine crawlers ship today, at varying levels of polish. Several began as
forks; where an upstream carries a licence, the directory retains it:

| source | directory | origin |
|---|---|---|
| iMessage | trawlers/imessage | [openclaw/imsgcrawl](https://github.com/openclaw/imsgcrawl) |
| Telegram | trawlers/telegram | [openclaw/telecrawl](https://github.com/openclaw/telecrawl) |
| WhatsApp | trawlers/whatsapp | [openclaw/wacrawl](https://github.com/openclaw/wacrawl) |
| Gmail | trawlers/gmail | began as gogcrawl |
| Calendar | trawlers/calendar | began as calcrawl |
| Contacts | trawlers/contacts | [openclaw/clawdex](https://github.com/openclaw/clawdex) |
| Apple Photos | trawlers/photos | [openclaw/photoscrawl](https://github.com/openclaw/photoscrawl) |
| Twitter (X) | trawlers/twitter | began as birdcrawl |
| Apple Notes | trawlers/notes | monorepo-native |

## For agents

Every command takes `--json` and returns structured output. Search
returns refs, and a ref is `source:kind/id`, for example
`imessage:msg/8842`. Search, then open a hit by the ref it carries:

```sh
trawl search "boat trip" --json
trawl open imessage:msg/8842 --json
```

Because the archives are local and already parsed, an agent onboards from
your history in one pass instead of one prompt at a time.

## Status

Pre-v1 and moving fast. The design is settled (see
[docs/vision.md](docs/vision.md)); the `trawl` CLI and the Mac app
are being built; the crawlers work today. It is not yet packaged for end
users.

## Contributing

Read [AGENTS.md](AGENTS.md) first. This repo is public and its privacy
rules are enforced in CI. To add a service, write a Go crawler that
implements the contract in [docs/contract.md](docs/contract.md) and
register it in `trawl`. It then appears in both the CLI and the app.

## Licence

MIT for the monorepo (see [LICENSE](LICENSE)). Forked crawler
directories keep their upstream LICENSE files and copyright notices,
which govern those directories. Credit where it began: several crawlers
originate in the [OpenClaw](https://github.com/openclaw) organisation,
and `trawlkit` began as a hard fork of Vincent Koc's crawlkit — thanks
to both.
