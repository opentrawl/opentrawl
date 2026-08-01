---
written_by: ai
---

# Photos

The Photos crawler builds a local, read-only archive of Apple Photos metadata,
searchable text and photo cards. It does not back up image files or replace the
Photos library as the source of truth.

## Source and storage

`update` reads the configured Photos library without changing assets, albums,
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
trawl photos metadata
trawl photos status
trawl update photos
trawl photos classify --limit 100
trawl photos classify --model MODEL --limit 20
trawl photos search "drone beach portugal"
trawl photos open LINK
```

The CLI uses normal text output. Human search output includes a link that
`open` accepts.

The current implementation still exposes `classify` and its Ollama-backed
model route. Photos v1 removes those competing journeys. Its accepted design
makes `update` own source indexing, media acquisition, location enrichment and
Luna card generation. The rendered request, raw response and stored typed card
remain linked by private provenance.

## Architecture

The Photos v1 design is a resumable typed dependency graph whose
substantial components can be inspected independently. The update composer is
the only concurrency owner. Different assets may occupy different nodes, but one asset advances only
when its required evidence is complete or explicitly proved absent.

[Photos architecture](docs/architecture.md) defines that source-specific
contract.

Search is a projection of stored facts and cards. It does not run a second
semantic inference pass. Photos does not create durable person, trip,
relationship or life-event truth tables.
