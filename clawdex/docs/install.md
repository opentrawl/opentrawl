---
written_by: ai
---

# Install

`clawdex` ships as a single Go binary. The visible version is injected at
build time: release builds use the tag, local builds use `git describe`.

## Homebrew (macOS, Linux)

```bash
brew install steipete/tap/clawdex
clawdex --version
```

The Homebrew formula lives in `steipete/homebrew-tap`.

## Go install

```bash
go install github.com/openclaw/clawdex/cmd/clawdex@latest
clawdex --version
```

Source builds require the Go version declared in
[`go.mod`](https://github.com/openclaw/clawdex/blob/main/go.mod).

## Build from source

```bash
git clone https://github.com/openclaw/clawdex.git
cd clawdex
go build -o ./bin/clawdex ./cmd/clawdex
./bin/clawdex --version
```

## Platform notes

- **macOS** is the most exercised target. `clawdex import apple` reads
  the local AddressBook SQLite databases, so the binary must be allowed in
  *Settings → Privacy & Security → Full Disk Access* before import.
- **Linux** builds support markdown editing, notes, search, Git, Google
  imports through `gog`, and vCard export. Apple direct import is macOS-only.
- **Windows** builds are lightly tested; the Git layer assumes a working
  `git` on `PATH`.

## Verify the install

```bash
clawdex --version
clawdex --help
clawdex doctor
```

After `clawdex init` (see [Quickstart](quickstart.md)), `clawdex doctor`
prints a one-shot health summary: config path, repo path, remote, person
count, and any avatar problems.

## Updating

- **Homebrew:** `brew upgrade clawdex`.
- **Go install:** rerun `go install github.com/openclaw/clawdex/cmd/clawdex@latest`.
- **Source:** `git pull && go build -o ./bin/clawdex ./cmd/clawdex`.

The on-disk markdown layout is forward-compatible across point releases. A
breaking layout change would ship a `clawdex doctor --repair` migration —
see [Doctor](doctor.md).
