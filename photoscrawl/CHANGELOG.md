---
written_by: ai
---

# Changelog

## Unreleased

- Move the model client and classify batch loop (retries, adaptive
  concurrency, quota abort, outcome accounting) into `crawlkit/model`;
  behavior unchanged.
- Index raw card prose for search instead of deduped term lists so ranking
  reflects real term frequency, version the FTS rebuild, and drop the
  write-only `observation_term` table.
- Keep unselected POI candidates out of the search index: bm25 favors short
  documents, so nearby-business names outranked real card matches.
- Stem and OR-combine search matching (porter tokenizer, bm25 ranking),
  rebuilding older archives in place on the write path.
- Requeue quota-refused classify items and stop the batch after sustained
  model 429s instead of recording them as permanent failures.
- Bound read commands (metadata/status/doctor/search/open) with a two-minute
  deadline and switch crawlkit's SQLite to the C driver.
- Add real `--help` output for the CLI and every verb; delete the `neighbors`
  verb.
- Align the module path with `openclaw/photoscrawl`, add CrawlKit control metadata for launcher discovery, use current dependencies, and prefer MapKit reverse geocoding on macOS 26.
- Rename the archive import command from `crawl` to `sync` to match the shared control contract.
- Rename classify `--local-model` to `--model` and JSON `local_model` to `model`.
- Route command logs through crawlkit's local log grammar and add `short_refs`
  for human search aliases that `open` can resolve.
- Change model classification from typed tag rows to `photo-card-v2` cards, and make search/open render card prose instead of observation soup.
- Move `place-context` and `eval-card` to the new `photoscrawl-lab` research binary, fold cached place-card rendering into `place-context`, and remove the standalone place backfill command.
