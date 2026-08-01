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
- what changed recently across several archives; and
- where an event, decision or object appears in the original record.

These are outcomes of reliable access and evidence, not separate hand-built
features. OpenTrawl owns the substrate. The agent supplies interpretation and
judgement.

## Product shape

`trawl` is the quiet human-and-agent front door to a person's local archives.
The CLI and Mac app are two presentations of the same typed product. They
preserve the same trawler identities, facts, links and failure meanings while
adapting their interaction to a person or agent.

Each trawler is a Go crawler for one app or provider. It owns access, provider
meaning and its source-native archive. TrawlKit owns only provider-neutral
mechanics that at least two trawlers use. Provider-specific schemas and
behaviour stay with their trawler.

Protobuf messages and enums define the shared typed control contract between
trawlers, `trawl` and the Mac app. Clients use that contract instead of trawler
packages, private database schemas or untyped substitute schemas.

The CLI and Mac app federate trawlers through this contract. Federation
combines search, status and open operations; it does not flatten messages,
events, photos and other record types into a universal record.

Derived artefacts may interpret records across archives. They keep their
evidence and provenance and never replace the original archives as truth.

## Design principles

- **Local first.** App access, archives, caches and user control stay on the
  Mac. Local first does not require local models. A model-backed operation may
  send an explicit, bounded input through a configured product boundary when
  the user invokes it.
- **Read apps; do not change them.** Trawlers archive and inspect app history.
  They do not send messages or write back to apps.
- **Human readable and agent usable.** Human output is a first-class surface.
  Output uses meaningful names, typed facts, real times, stable links and
  bounded fields rather than internal identifiers or raw dumps.
- **Trawler-owned meaning.** A trawler defines what matched and how its record
  opens. Federation validates, combines and orders records without rewriting
  their meaning.
- **Evidence before inference.** Derived cards, clusters and summaries retain
  their record links, inputs and generation provenance. They are replaceable
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

The durable work is faithful app access, repeatable search and open
behaviour, provenance, bounded typed interfaces and safe execution.
Interpretation, judgement and strategy belong to the best available model. A
stronger model should answer harder questions through the same archives and
contracts without a new semantic feature for every question.

Facts copied from apps, model judgements and human corrections remain distinct.
A later model can challenge or regenerate an interpretation without losing its
evidence or a person's dated correction.
