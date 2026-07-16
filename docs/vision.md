---
written_by: ai
---

# Vision

OpenTrawl makes a person's own digital history searchable by the agents that
work for them.

The useful context already exists, but it is split across messages, mail,
calendars, notes, photos, contacts and social archives. OpenTrawl extracts that
history into source-native archives on the user's Mac and gives people and
agents one bounded way to search and open it.

## North star

An agent should be able to answer questions from a person's history without
making them reconstruct that history one prompt at a time. Clean, complete
archives should let a capable agent work out, with cited evidence:

- who the important people are and where conversations with them happen;
- what someone bought, planned, attended or discussed;
- what changed recently across several sources; and
- where an event, decision or object appears in the original record.

These are outcomes of good access and evidence, not separate hand-built
features. OpenTrawl owns the substrate; the agent does the interpretation.

## Product shape

OpenTrawl has four layers:

1. Each Go crawler reads one source and owns its source-native SQLite archive,
   authentication, privacy boundary and source-specific commands.
2. The shared control contract gives every crawler manifest-driven status,
   search and open meanings, plus sync where the source declares it. Normal
   failures are actionable and every operation writes useful logs.
3. The `trawl` CLI and Mac app federate those contracts without reading crawler
   internals.
4. Derived artefacts may interpret records across sources, but they keep their
   evidence and never replace the source archives as truth.

Sources remain separate. Federation is a query boundary, not a universal
schema. A message, event and photo should not be flattened into one generic
record merely because one interface can search all three.

## Design principles

- **Local first.** Source access, archives, caches and user control stay on the
  Mac. Local first does not require local models. A model-backed operation may
  send an explicit, bounded input through a configured product boundary when
  the user invokes it.
- **Read the source; do not change it.** Crawlers archive and inspect. They do
  not send messages or write back to source apps.
- **Human readable and agent usable.** Human output is a first-class surface.
  Structured output uses meaningful names, real timestamps, stable refs and
  bounded fields rather than internal row IDs or raw dumps.
- **Source-owned meaning.** A crawler defines what matched and how its record
  opens. Federation validates, combines and orders those records without
  rewriting them.
- **Evidence before inference.** Derived cards, clusters and summaries retain
  their source refs, inputs and generation provenance. They are replaceable
  interpretations, not canonical facts.
- **Explicit privacy boundaries.** Secrets never appear in output. Any network
  operation is deliberate, configured and narrow.
- **One obvious path.** Defaults should cover ordinary use. New modes,
  fallbacks and compatibility layers need evidence that the simpler path is
  insufficient.

## Build for stronger models

OpenTrawl follows the Bitter Lesson: general models supply the intelligence;
the product supplies reliable access to the world.

The durable work is lossless source access, persistent state, provenance,
bounded interfaces and safe execution. Interpretation, judgement and strategy
belong to the best available model. A stronger model should answer harder
questions through the same archives and contracts without a new semantic
feature for every question.

Observed source facts, model judgements and human corrections remain distinct.
A later model can challenge or regenerate an interpretation without losing the
evidence or a person's dated correction.

## Architectural boundaries

- Crawlers do not perform cross-source identity resolution.
- Derived layers consume public source contracts, not private database schemas.
- The current source registry is compiled into `trawl`; there is no public
  drop-in plugin mechanism.
- The product has no hosted copy of a user's archives.
- Output is bounded. Long conversations and histories open around the matching
  item and state when more exists.

The [control contract](contract.md) defines the crawler seam. The
[Mac app contract](mac-app.md) defines how the human interface preserves it.
