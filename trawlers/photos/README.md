---
written_by: ai
---

# photoscrawl

`photoscrawl` is OpenTrawl's read-only Apple Photos crawler. It builds a local
`photos.db` archive so people and agents can find and understand photos without
turning the Photos library into a second source of truth.

The archive is not a photo backup. It records source facts, derived place
evidence and photo cards. The target contract requires enough provenance to
reconstruct each card; current coverage is listed in the
[data contract](docs/data-contract.md).

## Local-first storage, frontier classification

Local first describes storage, source access and user control:

- Photos access is read-only
- the archive, caches and run state stay on the user's machine
- `sync` never exports media or asks iCloud to download an original
- user-derived data never belongs in this public repository

Local first does not mean local image models are preferred. When a user runs
model-backed classification, photoscrawl sends the selected image and its
bounded context to a frontier vision model through Ollama Cloud. Direct paid
Gemini API calls are not part of the product path. Ollama Cloud is the Photos
image-classification and image-evaluation service, not a code-review or general
agent-reasoning service.

## Commands

```sh
trawl photos metadata --json
trawl photos status --json
trawl photos doctor --json
trawl photos sync --json
trawl photos classify --limit 100 --json
trawl photos classify --model MODEL --limit 20 --json
trawl photos search "drone beach portugal" --json
trawl photos open photos:asset/REF --json
```

`MODEL` is an Ollama Cloud vision-model name. A classify run without `--model`
writes deterministic metadata observations only.

Human search output uses a short ref when the archive can resolve it safely.
JSON keeps the canonical `photos:asset/<32-hex>` ref. `open` accepts either
form when the short ref resolves to one asset.

Runtime state lives under `~/.opentrawl/photos/`. The primary archive is
`~/.opentrawl/photos/photos.db`. Set `library_path` in
`~/.opentrawl/photos/config.toml` only when the Photos library is not at the
default macOS path.

## Product contract

The accepted product is an asset-parallel, resumable dependency graph. Different
assets may occupy different stages at once, but one asset reaches the expensive
model step only after its required upstream evidence is complete and inspected.
Unknown or unexamined missing evidence blocks that asset rather than becoming an
empty prompt field.

See [architecture](docs/architecture.md) for the stable dependency graph and the
[data contract](docs/data-contract.md) for the only current-main implementation
inventory and accepted boundary details.

## Research tooling

`photoscrawl-lab` exists as a research-only binary outside the `trawl` contract.
Its output does not prove the product path unless both use the same resolver,
metadata projection, place evidence, request renderer and persistence boundary.
Reusable logic moves into the product path; the parallel classification path is
retired after parity is proved.

The remaining disposition work is tracked in
[TRAWL-150](https://linear.app/joshpalmer/issue/TRAWL-150). Linear is the only
source of truth for its state, priority, ownership and decisions.

Historical research and eval records are labelled under `docs/evals/`. They are
evidence about an old run, not current provider, prompt or model decisions.

## Product boundaries

photoscrawl does not write to Photos or create durable people, trip,
relationship or life-event truth. It stores source facts, checked provider
evidence, model output and provenance. Search is a projection of those stored
records, not a second inference pipeline.

Live Photos are classified as still images for now. Motion frames and later
reclassification are separate future work.
