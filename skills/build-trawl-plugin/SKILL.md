---
name: build-trawl-plugin
description: Build, sandbox-install, and verify a standalone external plugin for OpenTrawl (`trawl plugin install`). Use when a user asks to add a new data source, app, or export to OpenTrawl.
---

# Build an OpenTrawl plugin

Build a standalone out-of-process plugin in its own repository or directory so the user can install it with `trawl plugin install` without modifying OpenTrawl core. OpenTrawl does not accept new built-in sources as core PRs; every new source belongs in a standalone plugin.

## Read the live contract first

Read these files in `github.com/opentrawl/opentrawl` before writing code so your plugin matches the exact current interfaces:

- [`trawlkit/contracts.go`](../../trawlkit/contracts.go): Go interfaces (`Trawler`, `Updater`, `Searcher`, `CanonicalRecordOpener`, `ArchiveIndexChangeExporter`, and optional messaging/people interfaces).
- [`trawlkit/request.go`](../../trawlkit/request.go): `TrawlerCommandExecutionRequest` fields injected by the host (`TrawlerArchivePaths`, `PluginHTTPClient`, `BrokeredSecrets`, `RecordSecretMutation`, `RequestOpenBrowserURL`).
- [`trawlkit/external_plugin_contract.go`](../../trawlkit/external_plugin_contract.go): `PluginCapabilityDeclarator` and how OpenTrawl builds your plugin's manifest.
- [`trawlkit/proto/trawl/plugin/plugin.proto`](../../trawlkit/proto/trawl/plugin/plugin.proto): length-delimited binary protobuf wire protocol over stdin and stdout (use this directly if writing a plugin in a language other than Go).
- [`trawlkit/pluginruntime/plugin_executable_server.go`](../../trawlkit/pluginruntime/plugin_executable_server.go): `pluginruntime.ServePluginExecutable` entrypoint and invocation dispatch.
- Reference implementations in this repo:
  - [`trawlers/notes/crawler.go`](../../trawlers/notes/crawler.go) for an offline local-file source.
  - [`trawlers/twitter/crawler.go`](../../trawlers/twitter/crawler.go) for a networked source with declared hosts and brokered Keychain secrets.

## Pin `trawlkit` to the installed app's commit

The plugin protobuf wire protocol has no schema versioning before v1, and external repos cannot use the monorepo's local `replace` directives. Check the installed app's commit first (for example from `/Applications/OpenTrawl.app/Contents/Info.plist` `GitCommit`, or `trawl --version` matched against git tags), then pull `trawlkit` at that exact commit in your plugin's Go module:

```sh
go get github.com/opentrawl/opentrawl/trawlkit@<commit>
```

## Architecture rules

- **Source-native SQLite archive:** Keep the source's own schema in the archive path the host gives you (`req.TrawlerArchivePaths.TrawlerArchivePath`). Read the source app or API read-only; never write back to source apps.
- **Join unified search:** Implement `ArchiveIndexChangeExporter` so OpenTrawl indexes your records into `~/.opentrawl/index.db`. That gives your plugin cross-source `trawl search`, temporal joins in `trawl around`, and Apple Silicon topic search (`trawl semantic`) without core ever opening your private database.
- **Declare exact capabilities:** Implement `PluginCapabilityDeclarator` (`DeclaredPluginCapabilities()`) with only the resources your plugin actually needs:
  - `DeclaredNetworkHosts`: public `host:port` HTTPS destinations (send all outbound requests through `req.PluginHTTPClient`; direct sockets are blocked by the sandbox).
  - `DeclaredSelfHostedNetworkHosts`: self-hosted `host:port` endpoints (approved at install time with `--allow-self-hosted HOST:PORT=CIDR`).
  - `DeclaredReadPaths`: local files or folders to read outside the plugin's archive directory.
  - `DeclaredReadSecretKeys` / `DeclaredWriteSecretKeys`: Keychain keys delivered via `req.BrokeredSecrets` and updated via `req.RecordSecretMutation`.
  - `DeclaredAuthOrigins`: `https://` origins the host may open in a browser via `req.RequestOpenBrowserURL`.
- **No direct printing:** Return typed protobuf responses (`trawlkit/proto/trawl/...`). OpenTrawl renders all human and agent output through `trawlkit/render`.
- **Single-line Go entrypoint:** In `cmd/trawl-<name>/main.go`, call `os.Exit(pluginruntime.ServePluginExecutable(myplugin.New()))`.

## Build, get user approval, install, and verify

Use the installed CLI binary at:

```sh
TRAWL=/Applications/OpenTrawl.app/Contents/Helpers/trawl
```

Build the plugin binary with SQLite FTS5 enabled:

```sh
CGO_ENABLED=1 go build -tags=sqlite_fts5 -o ./trawl-<name> ./cmd/trawl-<name>
```

**Before running `plugin install`, show the user every capability your plugin requests** (every network host, self-hosted CIDR, read path, Keychain secret key, and auth origin) and wait for their approval. Because the CLI installs whatever flags you pass, you must not silently grant a plugin access to the user's files, network, or secrets.

Once approved, stage the plugin with the exact approved flags (OpenTrawl probes the manifest under a zero-disk, zero-network macOS Seatbelt sandbox, pins the binary's SHA-256 hash, and writes host-owned approvals in `trust.pb`):

```sh
"$TRAWL" plugin install ./trawl-<name> \
  --allow-host api.example.com:443 \
  --allow-self-hosted nas.local:8443=100.64.0.0/10 \
  --allow-read-path ~/path/to/export \
  --allow-read-secret api_token \
  --allow-write-secret api_token \
  --allow-auth-origin https://auth.example.com
```

Omit any flag your plugin does not need. Re-run `plugin install` whenever you rebuild the binary so the pinned SHA-256 hash updates.

Verify the real human-facing path end to end against real local data (never fake or synthetic archive fixtures):

1. `"$TRAWL" plugin list` shows the plugin as `staged` and `ok`.
2. `"$TRAWL" update <name>` imports real records and exits `0`.
3. `"$TRAWL" status <name>` reports accurate counts, last update time, and healthy status.
4. `"$TRAWL" search "<real query>"` returns matches from the new source with clickable short links.
5. `"$TRAWL" open <LINK>` opens the full record with surrounding context.
