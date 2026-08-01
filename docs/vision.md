---
written_by: ai
---

# Vision

OpenTrawl makes a person's own digital history searchable by the people and
agents who work for them.

Useful context already exists across messages, mail, calendars, notes, photos,
contacts and social archives. OpenTrawl copies that history into separate,
source-native archives on the user's Mac. It then gives people and agents one
bounded way to search the archives and open the original context.

## North star

An agent should be able to answer questions from a person's history without
making them reconstruct that history one prompt at a time. Reliable archives
should let a capable agent work out, with cited evidence:

- who the important people are and where conversations with them happen;
- what someone bought, planned, attended or discussed;
- what changed recently across several sources; and
- where an event, decision or object appears in the original record.

These are outcomes of reliable access and evidence, not separate hand-built
features. OpenTrawl owns the substrate. The agent supplies interpretation and
judgement.

## Product shape

The Mac app is the human front door. It helps a person install OpenTrawl, grant
permissions, see available sources, keep them current, search them and open
their records. The bundled `trawl` CLI is the complete agent interface to the
same product.

Each Go crawler owns one source: access, source meaning and its source-native
archive. TrawlKit owns only provider-neutral mechanics that at least two
crawlers use. Provider-specific schemas and behaviour stay with their crawler.

Protobuf messages and enums define the shared typed control contract between
crawlers, `trawl` and the Mac app. Clients use that contract instead of crawler
packages, private database schemas or untyped substitute schemas.

The CLI and Mac app federate sources through this contract. Federation
combines search, status and open operations; it does not flatten messages,
events, photos and other source concepts into a universal record.

Derived artefacts may interpret records across sources. They keep their
evidence and provenance and never replace the source archives as truth.

## Design principles

- **Local first.** Source access, archives, caches and user control stay on the
  Mac. Local first does not require local models. A model-backed operation may
  send an explicit, bounded input through a configured product boundary when
  the user invokes it.
- **Read the source; do not change it.** Crawlers archive and inspect. They do
  not send messages or write back to source apps.
- **Human readable and agent usable.** Human output is a first-class surface.
  Output uses meaningful names, typed facts, real times, stable links and
  bounded fields rather than internal identifiers or raw dumps.
- **Source-owned meaning.** A crawler defines what matched and how its record
  opens. Federation validates, combines and orders records without rewriting
  their meaning.
- **Evidence before inference.** Derived cards, clusters and summaries retain
  their source links, inputs and generation provenance. They are replaceable
  interpretations, not canonical facts.
- **Explicit privacy boundaries.** Secrets never appear in output. Network
  operations are deliberate, configured and narrow. OpenTrawl has no hosted
  copy of a user's archives.
- **One live path.** Before v1, change the live schema and its consumers
  directly. Do not add fallback or compatibility paths.
- **Prove the real path.** A person proves the complete human-facing flow with
  a real archive, inspects the result and follows its next action. Tests,
  documents and component checks can support this proof but cannot replace it.
  Private proof stays outside this public repository.

## Build for stronger models

OpenTrawl follows the Bitter Lesson: general models supply the intelligence;
the product supplies reliable access to the world.

The durable work is faithful source access, repeatable search and open
behaviour, provenance, bounded typed interfaces and safe execution.
Interpretation, judgement and strategy belong to the best available model. A
stronger model should answer harder questions through the same archives and
contracts without a new semantic feature for every question.

Observed source facts, model judgements and human corrections remain distinct.
A later model can challenge or regenerate an interpretation without losing its
evidence or a person's dated correction.
