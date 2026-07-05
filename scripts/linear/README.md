---
written_by: ai
---

# Linear CLI

`linear` posts to Linear as an OAuth app actor. Write commands require
`--as`, so every agent-created issue or comment carries an explicit
display name instead of posting as the human OAuth user.

## Build

Run this from `scripts/linear`:

```sh
go build -o linear .
```

The repo MCP config runs `scripts/linear/linear mcp`, so rebuild the
binary after changing this module.

## Configure

Set the OAuth app credentials in the environment:

```sh
export LINEAR_CLIENT_ID=...
export LINEAR_CLIENT_SECRET=...
```

The CLI caches the app token at `~/.opentrawl/linear/token.json` with
file mode `0600`.

## Use

```sh
linear comment TRAWL-99 --as coordinator "Ready for review."
linear issue new --team TRAWL --title "Fix sync output" --as reviewer --label agent-filed
linear issue TRAWL-99
linear issues --team TRAWL
linear mcp
```
