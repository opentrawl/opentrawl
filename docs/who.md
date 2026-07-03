---
written_by: ai
---

# Who: people as a first-class query dimension

Design for the ship-blocking gap, grounded in mined evidence: a sweep
of months of real agent sessions (the moments the owner got angry, and
the questions actually asked) found that agents repeatedly fail on
people. They run "getting to know you" interviews
instead of reading archives that already hold the answer. They state stale
personal facts with confidence. In several sessions the agent itself names
"resolve handles into people" as the unbuilt gap. Real requests are
imperative — "check my emails for the specs", "we already discussed
this, go find it" — and the retrieval verb constantly runs through a
person or a vendor.

Today no surface serves that. Search shows who said things but cannot
filter on it, and full-text-matching a person's name finds messages
that mention them, not conversations with them — the ones that
matter almost never contain the name.

## The shape

Two additions, one dependency, in strict order.

1. Resolve first. `trawl who <fuzzy>` turns a human fragment into
   a person: `trawl who dave` returns every matching candidate with
   the columns that decide between them — display name, how the name
   matched (exact, prefix, contains), the sources they appear in,
   message volume, and when you last exchanged anything. Matching is
   prefix and substring over names, aliases and identifiers
   (clawdex's self-healing FTS index is exactly this; the command is
   a thin view over it).

   The ranking is the columns, in order: name-match quality, then
   recency, then volume — and every column is printed, so nothing is
   scored in secret. Queries land all over the distribution (the Dave
   you message daily, the Dave you emailed once in 2019), so the tool
   must never guess which one you meant: it shows the evidence and
   lets the caller decide. An agent that can see last-seen dates and
   volumes can apply whatever context it has; a hidden relevance
   score would take that away.

   How this ranking grows later — clustering each person's corpus,
   per-topic frequencies enriching the columns, and how those land —
   is [relationship-context.md](relationship-context.md), including
   the open question of query-time hints for specialised cases. The
   v1.1 columns are chosen so those arrive as new evidence columns,
   not as changed semantics.

2. Then filter. `search --who <person>` on every crawler that
   exports contacts, and federated on `trawl search`. The crawler
   filters at the archive (trawl-level post-filtering would silently
   lose recall behind per-crawler limits). Contract v1.1 addition,
   declared as a capability.

3. The dependency: identity join lands first. `--who alice` is
   dead on arrival while iMessage rows say `+15550100123`. Order:
   imsgcrawl resolves numbers to names via the Contacts store at sync;
   every crawler's contact export feeds clawdex; clawdex owns the
   cross-source identity graph. Then the flag works everywhere on day
   one.

## Method: stub before build

Before any implementation, a stubbed `trawl` (canned `who` results,
fake `--who` filtering) goes in front of cold agents with tasks taken
from the mined corpus of real requests — vendor-charge hunts ("find
the subscription that keeps charging me"), spec retrieval ("check my
mail for the bike specs"), decision recall ("we already discussed
this, go find it"), and person-scoped history. What agents type before
reading help is the API we should have built. The observed grammar
decides the final surface; this document records the starting
hypothesis, not the answer.

### Experiment record (2026-07-02, two cold agents)

Both agents (gpt-5.5, Opus) behaved identically at the decision points:
read `--help` then `search --help` first, then used `--who` correctly
on their very first real search. One help line was enough teaching.
Neither ever reached for Gmail-style `with:`/`from:` operators, killing
that option. Neither used the `who` resolver to orient; both ran it
late, as verification. One agent passed `--who "Vendor Support"` — an
organization, not a person — and one typed a multi-word name unquoted
after `--who`, taking only the first word.

Design consequences, adopted:
- `--who` is the grammar. No search operators.
- Resolve-first dies as a workflow assumption. Ambiguity surfaces in
  search output instead: when the filter matches several people, the
  result says so and names `trawl who` as the disambiguator.
- `--who` filters any sender identity — people and senders like
  organizations — not a curated-person allowlist.
- Multi-word names need an unambiguous exact form agents can pass
  after resolving; the resolver's output must hand it to them.

Open questions the stub answered or replaced:
- Do agents reach for `--who alice` or `search alice: boat` or
  `search with:alice boat`? (Gmail taught the world `from:`-style
  operators; they may be the more discoverable grammar.)
- Does anyone use `trawl who` unprompted, or must search errors and
  help teach it?
- Is ranking by message volume the right orientation, or do agents
  want per-source identifiers echoed back for exact re-use?

## Later: relationship context

Ranking by name, recency and volume cannot answer "the contract from
Dave" when the frequent Dave is a friend and the rare Dave is your
lawyer. That needs subject-matter knowledge per person — see
[relationship-context.md](relationship-context.md) for the design
direction. It is deliberately not in v1.1: it depends on derived
layers (classification, clustering) that build on top of proven
archives, never inside the crawlers.

## Non-goals

No fuzzy matching inside `--who` itself: resolve fuzziness in `who`,
filter with the exact resolved person. One obvious way; no similarity
knobs.
