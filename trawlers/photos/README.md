---
written_by: ai
---

# Photos

The Photos crawler builds a local, read-only archive of Apple Photos metadata,
searchable text and photo cards. It does not back up image files or replace the
Photos library as the source of truth.

## Source and storage

`sync` reads the configured Photos library without changing assets, albums,
metadata, faces or iCloud state. Runtime data is stored under
`~/.opentrawl/photos/`; the primary archive is
`~/.opentrawl/photos/photos.db`.

Set `library_path` in `~/.opentrawl/photos/config.toml` only when the library is
not at the default macOS location.

The archive keeps source facts, selected image identities, derived place
evidence, stored card output and provenance. Private media, metadata, locations,
requests and responses remain outside this public repository.

## Commands

```sh
trawl photos metadata --json
trawl photos status
trawl photos sync
trawl photos classify --limit 100
trawl photos classify --model MODEL --limit 20
trawl photos search "drone beach portugal"
trawl photos open photos:asset/REF
```

Add `--json` for structured output. Human search output may use a short ref;
JSON keeps the canonical `photos:asset/<32-hex>` ref. `open` accepts either form
when the short ref resolves to one asset.

`classify` without `--model` writes deterministic metadata observations. A
model-backed run sends the selected image and bounded readable context through
the crawler's Ollama Cloud boundary, using `OLLAMA_API_KEY`; `--model` selects
the model. The rendered request, raw response and stored card remain linked by
private provenance.

## Architecture

The crawler is a resumable dependency graph: source snapshot, normalised asset,
image roles, metadata, place evidence, rendered request, response and stored
card. Different assets may occupy different stages, but one asset advances only
when its required evidence is complete or explicitly proved absent.

[Photos architecture](docs/architecture.md) defines that source-specific
contract.

Search is a projection of stored facts and cards. It does not run a second
semantic inference pass. Photos does not create durable person, trip,
relationship or life-event truth tables.
