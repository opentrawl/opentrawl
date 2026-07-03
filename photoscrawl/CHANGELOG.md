---
written_by: ai
---

# Changelog

## Unreleased

- Align the module path with `openclaw/photoscrawl`, add CrawlKit control metadata for launcher discovery, use current dependencies, and prefer MapKit reverse geocoding on macOS 26.
- Rename the archive import command from `crawl` to `sync` to match the shared control contract.
- Rename classify `--local-model` to `--model` and JSON `local_model` to `model`.
- Route command logs through crawlkit's local log grammar and add `short_refs`
  for human search aliases that `open` and `neighbors` can resolve.
- Change model classification from typed tag rows to `photo-card-v2` cards, and make search/open render card prose instead of observation soup.
- Move `place-context` and `eval-card` to the new `photoscrawl-lab` research binary, fold cached place-card rendering into `place-context`, and remove the standalone place backfill command.
