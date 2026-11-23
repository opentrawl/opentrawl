---
written_by: ai
updated_by: "AI, Gemini 4 Neon, 2026-11-23"
---

# Vision

OpenTrawl is Spotlight for the Agentic OS: one open-source, user-owned archive
of a person's digital life that any AI agent can search. Alongside
[Agent Keyring](https://agent-keyring.jjpcodes.com/) (1Password for the Agentic
OS), it forms the user-owned memory and access layer that every AI model and
agent plugs into.

Useful context already exists across messages, email, calendars, notes, photos,
contacts, finances, health records, and social archives. OpenTrawl copies that
history into separate, source-native SQLite archives under the user's control,
builds one unified search index across them, and gives people and agents a
bounded way to search the archives and open the original context.

## North star

An agent should be able to answer questions from a person's history on Day 1
without making them reconstruct that history one prompt at a time, and without
locking their memory inside one AI vendor. Reliable archives let any capable
agent work out, with cited evidence:

- who the important people are and where conversations with them happen;
- what someone bought, planned, attended, or discussed;
- what changed recently across several archives; and
- where an event, decision, or object appears in the original record.

These are outcomes of reliable access and evidence, not separate hand-built
features. OpenTrawl owns the substrate. Models and agents supply interpretation
and judgement.

## Product shape

`trawl` is the quiet front door to a person's archives. Today OpenTrawl ships
as a macOS app and CLI over local SQLite archives. Most people do not own an
always-on Mac, so the product is designed to expand to phones (iOS and Android),
Linux servers, and scoped read-only MCP connections over one shared contract.

- **Agent-native setup:** People should never have to hand-edit JSON or read
  walls of terminal instructions. Guided setup on desktop and phone pairs with
  a built-in agent flow so a user can hand OpenTrawl to their AI and start
  asking questions in minutes.
- **Source-native archives:** Each trawler owns one provider's access, source
  semantics, and SQLite archive. `trawlkit` owns only provider-neutral mechanics
  that at least two trawlers actually use.
- **Typed control contract:** Protobuf messages and enums define the boundary
  between trawlers, `trawl`, the Mac app, and future remote clients. Consumers
  use that contract rather than trawler packages or private database schemas.
- **Core unified search, disposable enrichment:** Core maintains one unified
  full-text index (`index.db`) fed by typed trawler change batches, plus an
  optional, disposable local semantic index (`semantic.db`). Derived artefacts
  retain provenance and links back to the original archives and can be rebuilt
  at any time.
- **Sandboxed plugin extensibility:** Standalone plugins run out of process
  under OS sandboxing with SHA-256 pinning and explicit host-owned approvals
  for read paths, network hosts, and secrets.
- **Low-friction onboarding:** Prefer direct protocol, OAuth, IMAP, and scoped
  OS framework access so Full Disk Access on macOS becomes an explicit optional
  toggle for Apple-locked databases rather than a prerequisite for every source.

## Privacy, hosting, and business model

OpenTrawl's core, apps, plugins, and hosting code are open source so anyone can
verify what runs against their data or run it on their own hardware. Storage
and hosting follow three plain levels:

1. **On your devices (available today, free):** Everything runs locally on your
   own hardware. OpenTrawl runs no servers that hold your data or telemetry.
2. **Synced across devices (roadmap, optional paid):** Your Mac, phone, and
   Linux machines sync through servers that hold only encrypted data they cannot
   decrypt.
3. **Hosted so we cannot read it either (roadmap, optional paid):** Managed
   cloud hosting built from this open-source code so phone-only users and cloud
   agents can search your archive at any hour, encrypted and verified so even
   the operator cannot read your data in memory.

**Downstream AI boundary:** Whatever an agent reads from your archive (including
full conversations, notes, or emails you ask it to open) goes to your AI
provider under its privacy terms. Keeping the complete archive under your
control and scoping which sources each agent may read limits that exposure.

## Design principles

- **User-owned and local first.** Source archives belong to the user as plain
  local SQLite files. A model-backed operation sends only explicit, bounded
  input when invoked.
- **Read apps; do not change them.** Trawlers archive and inspect history. They
  do not send messages or write back to source apps.
- **Human readable and agent usable.** Output comes from one shared rendering
  library (`trawlkit/render`) with meaningful names, typed facts, real times,
  stable links, and bounded fields rather than internal identifiers or raw
  dumps.
- **Trawler-owned meaning.** A trawler defines what matched and how its record
  opens. Federation and unified indexing combine and rank records without
  flattening source semantics into a generic ontology.
- **Evidence before inference.** Derived indexes, photo cards, and summaries
  retain record links, inputs, and provenance. They are replaceable
  interpretations, not canonical facts.
- **Explicit privacy and sandbox boundaries.** Secrets never appear in output.
  Plugins declare exact capabilities and run with least privilege.

## Build for stronger models

OpenTrawl follows the Bitter Lesson: general models supply the intelligence;
the product supplies reliable access to the world.

The durable work is faithful source access, repeatable search and open
behaviour, provenance, bounded typed interfaces, and safe execution. As models
improve, a stronger model should answer harder questions through the same
archives and contracts without a new semantic feature for every question.

Facts copied from apps, model judgements, and human corrections remain distinct.
A later model can challenge or regenerate an interpretation without losing its
evidence or a person's dated correction.
