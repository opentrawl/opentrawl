---
written_by: ai
---

# Short refs
Recommend deterministic short refs with collision extension. Keep full refs
canonical. Do not add a Trawl mapping database or make short refs the JSON
identity.

The full ref stays authoritative: `telecrawl:msg/1234567890123456789`. The
short ref is human copy sugar: `t7k3f`, up to 6 characters.

A human should be able to copy `t7k3f` from search into `trawl open t7k3f`.
Agents and scripts should keep using full refs from JSON because they are
exact, source-owned, and durable.

## Recommended design
Generate the alias from the canonical full ref:

- hash a versioned string that includes the source-prefixed full ref
- encode it with a lowercase alphabet that avoids common lookalikes
- display the shortest unambiguous prefix, and keep extending as the
  corpus grows — the alias is a prefix of one digest, so a longer form
  is always available. Git does exactly this: its default abbreviation
  length grows with the repository's object count, and the Linux
  kernel needs 12 hex characters. Each crawler computes its default
  length from its own archive size the same way.

The source-prefixed full ref is the hash input, so aliases work across
federated sources without becoming item ids.

Short refs are stable enough to copy from current search output into open. They
are not permanent public identifiers. An alias changes only when the full ref
changes, the source id changes, or a later archive item creates a collision
that forces extension. If an old alias becomes ambiguous, it must fail safely.

## Where the mapping lives

There is no authoritative mapping table, because none is needed: the
short ref is computed, not assigned. Concretely:

    alias = base32(sha256("sr1|" + full_ref))[:length]

Same input, same alias, on every machine, forever — no storage, no
sync, and a rebuilt archive produces identical aliases. Extending on
collision means taking more characters of the same digest. This is
not a merkle tree; it is a flat hash of the ref string, the same
prefix-of-digest scheme git and docker use for object ids. The "sr1"
version tag means the scheme can change without old aliases silently
meaning something new.

The arithmetic: a 31-character alphabet gives 28.6 million 5-character
aliases. Today's corpus (~300k items) collides for roughly 1% of
refs, which extend to 6 characters (887 million); at millions of
items the default simply starts at 6. Ambiguity is always detected at
resolution time and always fails safely.

Each crawler owns the archive and canonical refs. It may keep a derived alias
index beside its search index for speed. That index is a cache, not state. It
can be rebuilt, deleted, or healed on read under the same rules as other
derived indexes.

Trawl may keep an in-memory map while rendering one search result. It must not
write a "last search" alias database.

Why not a crawlkit mapping table of assigned ids? Assignment creates
state: ids depend on insertion order, so two machines — or one
machine after a rebuild — assign different ids to the same item,
which breaks the rule that archives are derived data reproducible
from the source. The table would also need creating, backing up and
healing in every crawler. The function needs none of that and any
process can evaluate it. A crawler may still keep a computed
alias-to-ref cache table for fast resolution — that is a cache of
the function, self-healing like every other derived index, never a
source of truth.

## Open resolution flow

`trawl open` keeps the current path for full refs:

1. If the argument contains `:`, split it as `<source>:<path>`.
2. Select that source.
3. Call the crawler's `open` with the full ref.

For `trawl open t7k3f`:

1. Validate the alias length and alphabet.
2. Discover sources that declare short-ref support.
3. Ask each source to resolve the alias against its archive.
4. Require exactly one canonical full ref across all sources.
5. Call that source's normal `open` with the full ref.
6. Render the normal open result.

Resolution is read-only. A crawler may refresh a derived alias index while it
resolves, but it must not sync, import, or mutate source content.

## Collision and unknown aliases

Collisions may exist. They must never resolve silently.

Search display should prefer the shortest safe alias. If 5 characters collide,
show 6. If 6 still collides, show the full ref for those rows.

`trawl open t7k3f` has 3 outcomes:

- one match: open the item
- no match: return `unknown_short_ref` with a remedy to use a full ref
- more than one match: return `ambiguous_short_ref` with a remedy to rerun the
  search or use the full ref

An ambiguous alias must never pick the newest, first, or "best" result.

## JSON mode

Keep short refs out of `trawl search --json` by default.

JSON is the machine contract. Agents can copy the full `ref` field exactly and
avoid alias expiry, archive collisions, and human display policy. Adding short
refs to JSON would invite agents to use the weaker identifier.

`trawl open t7k3f --json` may accept the short ref as input. The output should
still return the normal item payload with its canonical full `ref`. Error JSON
should use the normal error shape, with codes such as `unknown_short_ref` and
`ambiguous_short_ref`.

## Options considered

- Per-archive content-hash table: reject. It can be safe inside one crawler,
  but content normalisation can change it, federated collisions need another
  layer, and every crawler gets a second identity store.
- Trawl-level mapping database: reject. It makes `open t7k3f` depend on
  previous local search history, makes reads write hidden state, and creates
  stale mapping and cleanup policy. Replacing all refs with assigned
  short ids has the same flaw one level deeper: canonical refs are
  source-owned and meaningful (a message row, an event UID); an
  assigned id scheme detaches identity from the source and differs
  across rebuilds. Short refs are an encoding of the ref, not a new
  identity.
- Deterministic hash with collision extension: choose this. It stores no
  authoritative mapping, keeps full refs as the contract identity, works across
  sources, and fails safely on collision.

## Contract impact

Contract v1 full refs remain valid and authoritative. Add a v1.1 capability,
`short_refs`, for crawlers that can resolve aliases. The capability means:

- the crawler can resolve a short alias to zero, one, or many full refs
- the crawler can return the shortest safe alias for a known full ref
- `open` output still carries the canonical full `ref`
- search JSON keeps the current `ref` field unchanged

Trawl should display short refs only when every displayed alias can be resolved
safely. Sources without `short_refs` keep showing full refs.

The deterministic mechanism this all rests on is spelled out with the
formula and the collision arithmetic under "Where the mapping lives".
