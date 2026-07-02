---
written_by: ai
---

# Who: people as a first-class query dimension

Design for the ship-blocking gap: "what did Alice and I say about the
boat trip?" is the most natural personal-memory question, and today no
surface answers it. Search shows who said things but cannot filter on
it, and the instinctive `trawl search alice boat` full-text-matches
the word "alice", which the messages that matter almost never contain.

## The shape

Two additions, one dependency, in strict order.

1. **Resolve first.** `trawl who <fuzzy>` turns a human fragment into
   a person: `trawl who mo` returns ranked candidates — display name,
   the sources they appear in, message volume — so humans and agents
   orient before filtering. Matching is prefix and substring over
   names, aliases and identifiers (clawdex's self-healing FTS index is
   exactly this; the command is a thin view over it). Ranked by
   message volume: the people you actually talk to come first.

2. **Then filter.** `search --who <person>` on every crawler that
   exports contacts, and federated on `trawl search`. The crawler
   filters at the archive (trawl-level post-filtering would silently
   lose recall behind per-crawler limits). Contract v1.1 addition,
   declared as a capability.

3. **The dependency: identity join lands first.** `--who alice` is
   dead on arrival while iMessage rows say `+15550100123`. Order:
   imsgcrawl resolves numbers to names via the Contacts store at sync;
   every crawler's contact export feeds clawdex; clawdex owns the
   cross-source identity graph. Then the flag works everywhere on day
   one.

## Method: stub before build

Before any implementation, a stubbed `trawl` (canned `who` results,
fake `--who` filtering) goes in front of cold agents with real tasks
("what did Alice and I say about the boat trip?"). What they type
before reading help is the API we should have built. The observed
grammar decides the final surface; this document records the starting
hypothesis, not the answer.

Open questions the stub must answer:
- Do agents reach for `--who alice` or `search alice: boat` or
  `search with:alice boat`? (Gmail taught the world `from:`-style
  operators; they may be the more discoverable grammar.)
- Does anyone use `trawl who` unprompted, or must search errors and
  help teach it?
- Is ranking by message volume the right orientation, or do agents
  want per-source identifiers echoed back for exact re-use?

## Non-goals

No fuzzy matching inside `--who` itself: resolve fuzziness in `who`,
filter with the exact resolved person. One obvious way; no similarity
knobs.
