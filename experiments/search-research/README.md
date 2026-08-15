# OpenTrawl search research harness

This directory contains the smallest product-shaped path needed to test natural-language search without making the experiment a second product.

The path is:

```text
crawler-owned searchable record export
  -> one index-boundary projection into a disposable passage corpus and FTS5 index
  -> one disposable embedding index
  -> token-OR lexical candidates + dense candidates
  -> canonical result-group de-duplication
  -> stable alternating coverage order
  -> one trawl search result list
  -> trawl open
```

The crawler export contract carries existing typed source records with their exact canonical references, a source-owned result-group reference and the existing local short reference. It does not define a second generic archive or presentation model. Each crawler reuses its ordinary row-to-record projector; one switch at the index boundary derives passages from those records. Notes retains every recovered version for search, groups competing versions at the note level, and opens the exact winning version.

## Passage contract

Short source sections stay whole. A longer section is split once at paragraph, line, sentence or whitespace boundaries. Passages do not overlap. Each passage retains its section-relative UTF-8 byte range and opened-record anchor.

The current 2,000 UTF-8 byte ceiling is provisional. The earlier 3,200-byte bounded corpus produced a real EmbeddingGemma context-length rejection with truncation disabled, so it was discarded. The indexer sends every exact formatted passage with runtime truncation disabled. A rejected passage fails the build. The shared ceiling is not final until every faithful contender accepts the identical formatted corpus with truncation disabled.

## One operating path

Build the wrapper and ordinary CLI with the repository Go environment and the `sqlite_fts5` build tag. Keep the frozen source snapshot, corpus, model index and measurements outside Git.

```text
trawl experiment corpus build \
  --state-root /absolute/frozen-checkpoint \
  --database /absolute/private/corpus.sqlite \
  --page-record-count 10000

trawl experiment index build \
  --corpus /absolute/private/corpus.sqlite \
  --index /absolute/private/index.sqlite \
  --model-artifact-name MODEL \
  --model-contract-sha256 MODEL_CONTRACT_SHA256 \
  --runtime-model-digest RUNTIME_MODEL_DIGEST \
  --runtime-loaded-model-bytes RUNTIME_LOADED_MODEL_BYTES \
  --runtime-name RUNTIME \
  --runtime-version VERSION \
  --runtime-endpoint ENDPOINT \
  --document-input-prefix PREFIX \
  --query-input-prefix PREFIX \
  --maximum-input-tokens TOKENS \
  --native-dimensions NATIVE_DIMENSIONS \
  --stored-dimensions STORED_DIMENSIONS \
  --stored-precision float32 \
  --maximum-concurrent-requests 1 \
  --maximum-batch-passages 32 \
  --runtime-process-id PID
```

The runtime PID must be the root of one isolated runtime process tree. Resource measurements include the harness and that tree only, with their baselines recorded separately.

For models with Matryoshka dimensions, the runtime returns the declared native
vector. The indexer takes the declared stored prefix, normalizes it with
float64 arithmetic and stores float32 once. Query vectors use the same path.

Set `TRAWL_SEARCH_RESEARCH_CORPUS`, `TRAWL_SEARCH_RESEARCH_INDEX`, `TRAWL_SEARCH_RESEARCH_RUNTIME_PROCESS_ID` and `TRAWL_SEARCH_RESEARCH_BASE_TRAWL` for the wrapper. The agent-facing path is then ordinary OpenTrawl:

```text
trawl search [--trawler SOURCE] [--after TIME] [--before TIME] [--limit COUNT] QUERY
trawl open LINK
trawl open --anchor ANCHOR_ID --start-utf8-byte START --end-utf8-byte-exclusive END LINK
trawl agent-instructions
```

Root help lists `agent-instructions`. The command prints the exact experimental
`SKILL.md` embedded in the wrapper. A human-owned `AGENTS.md` may point an agent
to `trawl agent-instructions`; the experiment does not install or copy the
skill into a personal directory.

The private corpus records the absolute frozen state root used for export and
a content hash of the source database files in scope. Corpus construction,
resume and index build verify that identity. Search reads the recorded root,
and delegated `open` sets the base CLI's state root to it regardless of the
caller's ambient OpenTrawl state-root setting. Search and open trust that frozen
input invariant; they do not verify or re-hash the archive. Exact passage range
validation still occurs when a result is opened. The path and hash stay in the
private corpus; they are not public configuration or repository data.

The recorded root is an operationally frozen experiment input. If its source
database content is intentionally changed, the derived corpus and embedding
index must be regenerated. Open does not adjust an old byte range, retry
against a different archive root or fall back to a broader record. Exact byte
identity relies on that operationally frozen root; open rejects an invalid
anchor, range or UTF-8 boundary.

Search uses fixed internal top-100 lexical and dense record pools. It applies source and time eligibility before an exact in-process cosine scan, de-duplicates both lanes on the source-owned canonical result group and alternates their ranks into one list. FTS5 returns a match-centred lexical excerpt. The command does not expose retrieval modes. The exact scan is measured research plumbing, not a selected product backend.

The experimental semantic path supports source and time filters. It does not support `--who`, so this bounded wrapper cannot replace product search for person-constrained tasks.

Each search prints the corpus source and record bound before its results. A
bounded corpus can establish only what it contains. A no-result response from
this experiment does not establish that the full source archive has no match.

Anchored Note, Gmail, iMessage text and Telegram text results print the typed
passage command. `START` is the section-relative UTF-8 byte start. `END` is the
exclusive section-relative UTF-8 byte end. Open selects the anchored source
text, applies the same outer whitespace trim as export, validates both UTF-8
boundaries, and replaces only that text in the normal record presentation with
the exact passage and explicit omission markers. Record fields, context, links
and source actions remain available.

Calendar sections, WhatsApp sections, and Telegram media titles have no exact
opened-text mapping in this slice. Those results print ordinary
`trawl open LINK` and expose the source's complete bounded record presentation.

## What this proves

This harness can prove that the public export, shared corpus, one currently runnable deployment configuration, candidate union, filters and search-to-open route work on a bounded real archive. It does not select an embedding deployment, freeze the passage ceiling, establish full-corpus resource use or show that semantic retrieval improves agent answers. Those decisions require the cold-reviewed contender screen, judgments and paired agent runs described outside this public repository.
