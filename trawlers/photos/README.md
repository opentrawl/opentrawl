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
trawl update photos
trawl search "words" --trawler photos
trawl open LINK
```

The CLI uses normal text output. Human search output includes a link that
`open` accepts.

`update` owns source indexing, media acquisition, location enrichment and card
generation. It sends each eligible current image and its bounded readable
context through the configured model boundary. The rendered request, raw
response and stored typed card remain linked by private provenance.

## Architecture

The crawler is a resumable typed dependency graph whose substantial components
can be inspected independently. The update composer is the only concurrency
owner. Different assets may occupy different nodes, but one asset advances only
when its required evidence is complete or explicitly proved absent.

[Photos architecture](docs/architecture.md) defines that source-specific
contract.

Search is a projection of stored facts and cards. It does not run a second
semantic inference pass. Photos does not create durable person, trip,
relationship or life-event truth tables.
