---
written_by: ai
---

# Eval runs

Public run notes go here after private outputs have been reviewed and stripped
of user-derived content.

Allowed:

- run date;
- prompt version;
- model names;
- sample size;
- aggregate pass/fail notes;
- non-private conclusions.

Not allowed:

- photos;
- filenames;
- asset ids or UUIDs;
- exact GPS;
- OCR/barcode/ticket/passport/receipt text;
- copied model responses from real user images;
- private output paths beyond the generic
  `<crawlkit-data-dir>/evals/<run-id>` shape.

## 3 July 2026 blocked baseline and model preflight

Baseline status: blocked before sampling. No real image reached a model. The
provider must prepare the sample before the model baseline can run.

Blocked provider probe command:

```sh
photoscrawl eval-card \
  --library "$HOME/Pictures/Photos Library.photoslibrary" \
  --limit 2 \
  --models gemma3:27b \
  --ollama-url https://ollama.com/api \
  --json
```

Planned Ollama baseline command after the provider fix:

```sh
photoscrawl eval-card \
  --library "$HOME/Pictures/Photos Library.photoslibrary" \
  --prompt prompts/photo-card-v1.md \
  --limit 15 \
  --models gemma4:31b,gemini-3-flash-preview,gemma3:27b,qwen3.5:397b \
  --ollama-url https://ollama.com/api \
  --json
```

Planned Gemini direct command after the provider fix:

```sh
OLLAMA_API_KEY="<gemini-api-key>" photoscrawl eval-card \
  --library "$HOME/Pictures/Photos Library.photoslibrary" \
  --prompt prompts/photo-card-v1.md \
  --limit 15 \
  --models gemini-flash-latest \
  --ollama-url https://generativelanguage.googleapis.com/v1beta/openai/v1 \
  --json
```

Run details:

| field | value |
|---|---|
| prompt version | `photo-card-v1` |
| prompt sha256 | `1ab0fc56fb8983d7a43d3fd9bf3c8d36172c1542ee01cb8fd35b81a2bf7f65d4` |
| requested sample size | 2 |
| prepared sample size | 0 |
| model calls | 0 |
| iCloud downloads | off |
| result | blocked before asset selection |
| blocker | `primary provider failed: Photos access is denied for this process; fallback provider failed: query sqlite albums: SQL logic error: no such table: Z_33ASSETS (1)` |

A later provider-only recheck from the current tree made 0 model calls and still
prepared sample size 0 because no local original reached `eval-card`.

Synthetic vision preflight used one generated blue PNG. This did not use real
Photos data.

Preflight command:

```sh
blue_png='iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAIAAAAlC+aJAAAAaUlEQVR4nOzPUQkAIQDA0OMwgunsjzkM4cdD2Euwjbn297JfB9xqQGtAa0BrQGtAa0BrQGtAa0BrQGtAa0BrQGtAa0BrQGtAa0BrQGtAa0BrQGtAa0BrQGtAa0BrQGtAa0BrQDsBAAD//+b6AdfgDboPAAAAAElFTkSuQmCC'
for model in gemma4:31b gemma3:27b gemini-3-flash-preview qwen3.5:397b; do
  jq -nc --arg model "$model" --arg img "$blue_png" \
    '{"model":$model,"prompt":"What is the dominant colour in this image? Reply with only the colour name.","images":[$img],"stream":false,"options":{"num_predict":16,"temperature":0}}' |
    curl https://ollama.com/api/generate \
      -H "Authorization: Bearer $OLLAMA_API_KEY" \
      -H "Content-Type: application/json" \
      -d @-
done
```

| model | sample size | response | decision |
|---|---:|---|---|
| `gemma4:31b` | 1 synthetic image | `Blue` | keep for baseline |
| `gemma3:27b` | 1 synthetic image | `Blue` | keep for baseline |
| `gemini-3-flash-preview` | 1 synthetic image | `Blue` after request retry | keep for baseline |
| `gemini-flash-latest` | 1 synthetic image | `Blue` through Gemini OpenAI-compatible endpoint | keep for baseline |
| `qwen3.5:397b` | 1 synthetic image | `Blue`; exact OCR retest passed separately | keep for baseline |

Scoring setup: `docs/evals/photo-card-protocol.md` defines the rubric but does
not name a judge. Baseline scoring will follow the May eval method: inspect the
saved private JSON outputs against the rubric, keep the scoring evidence
private, and publish only aggregate scores, counts, commands, model names,
prompt versions and generic findings. Any score produced by the same model being
judged must carry a self-judge caveat.

## 3 July 2026 photo-card baseline

This run supersedes the blocked baseline above. The May 30 continuity sample was
not reusable as a full sample: the local run directory held only a one-image
smoke manifest, and the frozen harness has no manifest-input flag. This run used
the protocol `latest` sample with 15 prepared images.

Important caveat: the supplied binary still prepared no images on this library
without iCloud downloads. To keep iCloud downloads off, B1 used a temporary
eval-only build outside the repo that accepts package-local rendered derivatives
when no package original is available. Treat these as derivative-input scores,
not original-input scores.

No private images, asset ids, filenames, exact locations, OCR text, model
responses or output paths are recorded here. Raw outputs and scoring notes stayed
outside the repo.

Baseline command shape for Ollama-hosted models:

```sh
photoscrawl eval-card \
  --library "$HOME/Pictures/Photos Library.photoslibrary" \
  --prompt prompts/photo-card-v1.md \
  --limit 15 \
  --sample latest \
  --models gemma4:31b,gemma3:27b,gemini-3-flash-preview,qwen3.5:397b \
  --ollama-url https://ollama.com/api \
  --json
```

Baseline command shape for the Gemini OpenAI-compatible endpoint:

```sh
GEMINI_API_KEY="<from secret>" OLLAMA_API_KEY="$GEMINI_API_KEY" photoscrawl eval-card \
  --library "$HOME/Pictures/Photos Library.photoslibrary" \
  --prompt prompts/photo-card-v1.md \
  --limit 15 \
  --sample latest \
  --models gemini-flash-latest \
  --ollama-url https://generativelanguage.googleapis.com/v1beta/openai/v1 \
  --json
```

Run details:

| field | value |
|---|---|
| date | 3 July 2026 |
| prompt | `prompts/photo-card-v1.md` |
| prompt sha256 | `1ab0fc56fb8983d7a43d3fd9bf3c8d36172c1542ee01cb8fd35b81a2bf7f65d4` |
| sample size | 15 |
| sample mode | `latest` |
| source kind | package-local rendered derivatives |
| iCloud downloads | off |
| scoring method | B1 manual read of saved private JSON against the protocol rubric |
| scoring caveat | directional self-judge pass, not blinded human adjudication |

Scores use a 1 to 5 scale. Higher is better.

| model | summary | visual detail | OCR and text | location | uncertainty | privacy and format | mean | notes |
|---|---:|---:|---:|---:|---:|---:|---:|---|
| `gemini-flash-latest` | 4.8 | 4.6 | 4.6 | 4.2 | 4.4 | 4.4 | 4.50 | best overall; strong on documents, screenshots, food and grill hardware |
| `gemini-3-flash-preview` | 4.7 | 4.5 | 4.5 | 4.1 | 4.3 | 4.3 | 4.40 | close second; slightly more location overreach than `gemini-flash-latest` |
| `gemma4:31b` | 4.3 | 4.1 | 3.9 | 3.7 | 4.0 | 4.3 | 4.05 | good visual baseline; less specific on technical details and place context |
| `qwen3.5:397b` | 4.1 | 3.8 | 3.2 | 2.8 | 3.0 | 2.9 | 3.30 | verbose; useful summaries but more metadata echo and equipment misclassification |
| `gemma3:27b` | 3.6 | 3.1 | 2.7 | 2.0 | 2.3 | 1.9 | 2.60 | dropped; serious place hallucinations and raw metadata leakage |

Generic findings:

- `gemini-flash-latest` and `gemini-3-flash-preview` were the only models that
  stayed consistently useful on both document-like images and grill hardware
- `gemma4:31b` scored higher than the May anchor, but this sample was not
  comparable to the May image set and used rendered derivatives
- `gemma3:27b` should not be used for photo-card work until metadata echoing is
  fixed or filtered
- `qwen3.5:397b` passed the classifier run but did not recover from the May
  concern enough to beat the Gemini models

## 3 July 2026 original-input top-model rerun

The orchestrator later corrected the run constraint: full originals are the
product path, and PhotoKit export with `--allow-icloud-downloads` was approved
for this sample size. B1 rebuilt from the repo and reran the top 2 baseline
models on the same 15 assets with original export enabled.

The first parallel Gemini endpoint attempt crashed inside the PhotoKit export
bridge before model calls. The successful runs were then run sequentially against
the populated private original cache. No raw stack trace, asset id, filename or
output path is recorded here.

Original-input command shape:

```sh
photoscrawl eval-card \
  --library "$HOME/Pictures/Photos Library.photoslibrary" \
  --prompt prompts/photo-card-v1.md \
  --limit 15 \
  --sample latest \
  --allow-icloud-downloads \
  --models gemini-3-flash-preview \
  --ollama-url https://ollama.com/api \
  --json
```

The Gemini endpoint used the same command shape with
`--models gemini-flash-latest` and the Gemini OpenAI-compatible base URL.

| field | value |
|---|---|
| prompt | `prompts/photo-card-v1.md` |
| prompt sha256 | `1ab0fc56fb8983d7a43d3fd9bf3c8d36172c1542ee01cb8fd35b81a2bf7f65d4` |
| sample size | 15 |
| sample relation | same assets as the derivative-input baseline |
| source kind | PhotoKit-exported originals |
| iCloud downloads | on |
| successful model calls | 30 |
| failed model calls | 0 |

Scores use the same 1 to 5 rubric. These are the product-path top-model scores;
the prompt variant deltas below were not rerun on originals.

| model | summary | visual detail | OCR and text | location | uncertainty | privacy and format | mean | delta from derivative run |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| `gemini-flash-latest` | 4.9 | 4.7 | 4.7 | 4.5 | 4.5 | 4.8 | 4.68 | +0.18 |
| `gemini-3-flash-preview` | 4.8 | 4.7 | 4.6 | 4.4 | 4.4 | 4.8 | 4.58 | +0.18 |

Originals improved the technical grill and document reads and removed the small
metadata-echo risk seen in derivative-input variant scoring. The model order did
not change: `gemini-flash-latest` remains the best model, with
`gemini-3-flash-preview` close behind.

## 3 July 2026 photo-card prompt variants

Variants used the same 15 asset sample as the derivative-input baseline and the
top 2 baseline models:

- `gemini-flash-latest`
- `gemini-3-flash-preview`

One screenshot rendered from a different package-local derivative size between
baseline and variants, but it was the same asset and same visual subject. The
variant comparison is still valid for prompt behaviour.

Variant command shape:

```sh
photoscrawl eval-card \
  --library "$HOME/Pictures/Photos Library.photoslibrary" \
  --prompt prompts/photo-card-v2.md \
  --limit 15 \
  --sample latest \
  --models gemini-3-flash-preview \
  --ollama-url https://ollama.com/api \
  --json
```

The Gemini endpoint used the same command shape as the baseline, with
`--models gemini-flash-latest` and the Gemini OpenAI-compatible base URL.

| prompt | prompt sha256 | calls | failed calls | mean delta | location delta | uncertainty delta | privacy and format delta | verdict |
|---|---|---:|---:|---:|---:|---:|---:|---|
| `photo-card-v2` | `a8b83b78b77f382a1e86fdf849928b9717e3482391e5ee50fde369e9364a1aef` | 30 | 0 | +0.05 | +0.15 | 0.00 | 0.00 | wins on this sample |
| `photo-card-v3` | `748eb531611f552859845454546daef56298bdcb0a0a70223b458450eb020cb2` | 30 | 0 | +0.01 | +0.10 | +0.10 | -0.20 | useful idea, but one raw-metadata echo |
| `photo-card-v4` | `2853c5b97c1fb75658bca24ecffec63739428c0f3b9bab684adabc035cbcd560` | 30 | 0 | -0.03 | -0.05 | +0.15 | 0.00 | loses; more cautious words did not stop place overclaims |

Recommendation: use `photo-card-v2` as the next prompt baseline for this
sample. It improved neighbourhood and hub context without adding metadata leaks.
Do not ship `photo-card-v4` as-is; it made uncertainty prose longer but still
allowed exact-place overclaims. Keep the `photo-card-v3` rural/non-POI idea for a
future merged prompt, but only after tightening the metadata-echo guard.

## 3 July 2026 photo-card v3 venue plausibility rerun

This run compares the historical `photo-card-v2` prompt with the current
`photo-card-v3` prompt after adding structured venue plausibility. It uses the
same 15 latest original-input assets for both prompts.

The v2 prompt was extracted from git history to a temporary path before the
comparison. It was not restored to the repo.

Command shape for v3:

```sh
photoscrawl eval-card \
  --library "$HOME/Pictures/Photos Library.photoslibrary" \
  --prompt prompts/photo-card-v3.md \
  --limit 15 \
  --sample latest \
  --allow-icloud-downloads \
  --models gemini-3-flash-preview \
  --ollama-url https://ollama.com/api \
  --json
```

Command shape for v2:

```sh
photoscrawl eval-card \
  --library "$HOME/Pictures/Photos Library.photoslibrary" \
  --prompt /tmp/photo-card-v2.md \
  --limit 15 \
  --sample latest \
  --allow-icloud-downloads \
  --models gemini-3-flash-preview \
  --ollama-url https://ollama.com/api \
  --json
```

Run details:

| field | value |
|---|---|
| date | 3 July 2026 |
| sample size | 15 |
| sample mode | `latest` |
| sample relation | same assets, same order |
| source kind | PhotoKit-exported originals |
| iCloud downloads | on |
| classifier model | `gemini-3-flash-preview` |
| successful model calls | 30 |
| failed model calls | 0 |
| scoring method | model-judge pass over saved private images and JSON |
| judge model | `gemma4:31b` |
| scoring caveat | directional model-judge result, not blinded human review |

Scores use the 1 to 5 rubric from `docs/evals/photo-card-protocol.md`.

| prompt | prompt sha256 | summary | visual detail | OCR and text | location | uncertainty | privacy and format | mean | inconsistent venue flags | thin descriptions | verdict |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| `photo-card-v2` | `a8b83b78b77f382a1e86fdf849928b9717e3482391e5ee50fde369e9364a1aef` | 5.00 | 5.00 | 4.93 | 2.47 | 5.00 | 5.00 | 4.57 | 7 | 0 | loses on location |
| `photo-card-v3` | `9eb8a52d62ff56084028995af83b2b31e5f0dac27246bf12967c0f46aed7d158` | 4.87 | 4.93 | 4.80 | 4.33 | 4.87 | 5.00 | 4.80 | 2 | 0 | wins overall |

Verdict: keep `photo-card-v3` as the product prompt. It wins because the
location score rises sharply and venue overclaim flags drop from 7 to 2.

v3 still loses some axes. It trails v2 slightly on summary, visual detail, OCR
and uncertainty. The likely cause is the stricter venue section: it spends model
attention on place calibration and leaves a little less incidental detail than
v2. A first v3 scoring pass also showed thin descriptions; adding an explicit
220 to 420 word target fixed that before the published comparison above.

Venue caveat: the 2 v3 flags are not cleanly equivalent. One came from the
structured inconsistent-candidate field that JSON keeps by design and the text
renderer hides. The other should be treated as a remaining prompt risk. The code
gate is still required: inconsistent structured verdicts must not render a
human `Venue:` line.
