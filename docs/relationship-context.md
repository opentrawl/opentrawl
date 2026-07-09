---
written_by: ai
---

# Relationship context: a design direction, not a build order

Future work. This document exists so the direction is on record and
the boundary between now and later stays sharp. Nothing here is in
contract v1.1.

## The problem it will solve

"Get the latest contract from Dave" has two Daves: the friend you
message daily and the lawyer you emailed four times. Name matching,
recency and volume all point at the friend. Only subject matter points
at the lawyer — you have never once discussed contracts with the
friend. Resolving people by what you talk to them about is how a human
assistant disambiguates, and it is the layer that lets an agent nail a
vague request first time.

## The shape, when it comes

- A derived, per-person brief: feed one person's corpus to a model and
  keep a short classification — the topics you actually discuss with
  them, their evident role, the sources they live in. "You usually
  talk to this person about legal matters; one thread, formal tone."
- `trawl who` gains a query-aware mode: candidates ranked not only by
  name match, recency and volume, but by how well each person's brief
  fits the words of the search. The brief is shown, never a bare
  score — the same no-hidden-ranking rule as v1.1.
- Briefs are derived data in the clawdex person layer: rebuilt from
  archives, deletable, self-healing, and never a source of truth.
  Model inference follows the vision's provider seam — local by
  default, remote only by explicit opt-in, since briefs summarise
  private conversations.

## How it lands, mechanically

The pipeline is derived layers all the way: per-person corpus in from
the archives, topic clusters out, then a brief plus per-topic
frequencies stored beside the person. Frequencies become new columns
in the resolver (talks weekly about cycling, once ever about
contracts); the brief becomes the sentence an agent reads. Everything
re-derives from the archives on demand — wrong output is fixed by
better derivation, never by hand-editing derived data.

No agent-tweakable knobs: derived layers re-derive from evidence, and
a knob would let configuration masquerade as knowledge. One open
question is recorded, not decided: whether specialised queries (an
organisation mailing from many domains, a person with heavy sender
aliasing) justify query-time hints an agent can pass. Any such hints would
be per-query arguments, never persisted configuration.

## Why not now

- It depends on clustering and per-source derived layers that are
  scheduled after the archives themselves are proven; building
  interpretation on top of unproven archives compounds errors.
- v1.1's transparent columns (match quality, last seen, volume)
  already let a context-carrying agent disambiguate most cases; the
  evidence for what the briefs must contain should come from watching
  agents fail with v1.1, the same way the who design itself came from
  a mined failure corpus.

## What v1.1 leaves in place for it

The resolver's output shape has room for a `brief` column later
without changing existing fields, and the contract's capability list
can grow a `person_briefs` entry the same additive way `--who`
arrives. No placeholder code ships now.
