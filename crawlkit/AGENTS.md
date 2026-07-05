# AGENTS.md

## Purpose

`crawlkit` is the shared Go library for the crawl app family. It owns reusable
local archive mechanics: config paths, SQLite helpers, git-backed mirrors,
sync state, CLI output helpers, and safe local cache reads.

It is not a provider crawler. Keep Slack, Discord, Notion, GitHub, and other
provider-specific behavior in the downstream apps unless the abstraction is
clearly reusable across at least two apps.

Use `docs/boundary.md` as the working ownership map when deciding whether a
feature belongs in `crawlkit` or a downstream crawl app.

## Development Rules

- Keep public package nouns stable and small: `config`, `store`, `mirror`,
  `state`, `output`, `cache`, and `control`.
- Prefer additive APIs. If an API must change, preserve downstream
  compatibility or update all crawl app branches in the same work cycle.
- Do not add app-specific database schema, auth, API, or cache parsing logic to
  this library.
- Do not touch live app stores during tests. Use temp dirs and temp SQLite
  files only.
- Use `GOWORK=off` for release and downstream-compatibility checks so local
  workspaces do not hide missing tagged APIs.

## Validation

Run before handoff:

```bash
GOWORK=off go mod tidy
git diff --exit-code -- go.mod go.sum
GOWORK=off go vet ./...
GOWORK=off go test -count=1 ./...
```

For release readiness, also verify the public module tag:

```bash
GOPROXY=https://proxy.golang.org GONOSUMDB= go list -m github.com/openclaw/crawlkit@v0.5.0
```

## Downstream Compatibility

When changing exported APIs or TUI behavior, smoke the app branches with temp
home/config/cache directories:

```bash
GOWORK=off go test ./...
<app> --help
<app> --version
<app> metadata --json
<app> status --json
```

Use read-only or temp data. Never mutate `~/.gitcrawl`, `~/.slacrawl`,
`~/.discrawl`, `~/.notcrawl`, or equivalent live archives.

## Release Model

Go libraries are released by signed semver git tags. There is no npm, PyPI, or
Homebrew publish step for `crawlkit`.

Use patch tags for narrow fixes and minor tags for broader shared crawler
infrastructure. After tagging, prime/verify the Go proxy and then update
downstream apps to the published tag.
