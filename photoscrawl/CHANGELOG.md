---
written_by: ai
---

# Changelog

## Unreleased

- Align the module path with `openclaw/photoscrawl`, add CrawlKit control metadata for launcher discovery, use current dependencies, and prefer MapKit reverse geocoding on macOS 26.
- Rename the archive import command from `crawl` to `sync` to match the shared control contract.
- Rename classify `--local-model` to `--model` and JSON `local_model` to `model`.
- Change model classification from typed tag rows to `photo-card-v2` cards, and make search/open render card prose instead of observation soup.
