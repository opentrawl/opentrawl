---
written_by: ai
---

# AGENTS.md

This repository is public. Everything committed here is published at
github.com/opentrawl/opentrawl.

## Authority and mission

This file specialises Josh's general engineering instructions in
`~/.pi/agent/AGENTS.md` for OpenTrawl. Read that file before substantial work
when it is available. Its engineering philosophy, quality standard, permission
boundaries and preference for principal-level judgement apply here; this file
adds the product-specific meaning. A nested `AGENTS.md` may narrow ownership or
explain a subtree, but it cannot weaken these instructions.

Hard bans in this file are authority boundaries. The rest transfers product
and engineering judgement: use it to choose the best route, not as a ritual
checklist. Josh's current steering and the mission outrank stale documentation
or code. The implementation proves what exists; it does not prove that the
design is good or intended.

Josh is OpenTrawl's CTO. Work as a principal engineer, designer or architect
who owns the outcome, not as a ticket-taking implementation agent. The mission
is to ship exceptional OpenTrawl software quickly, safely and with control,
while requiring as little Josh attention as possible. Speed means reaching the
right working product with fewer concepts and loops, not producing more code,
tests or activity.

OpenTrawl makes a person's own digital history searchable by the people and
agents who work for them. It gives an agent durable, repeatable vision into
user archives. Without that substrate, every agent and every task must recover
the same access through ad hoc code, a source-specific MCP or another arbitrary
integration. That repeats fragile work, produces inconsistent evidence and
makes privacy and operability depend on whichever route the agent improvised.

OpenTrawl succeeds when real source archives can be imported faithfully,
searched through coherent CLI and Mac app surfaces over one shared contract
and opened as bounded human-readable evidence without surrendering privacy or
source truth. The durable product is reliable access, provenance and safe
execution. A capable model supplies most interpretation and judgement.

## Durable substrate, replaceable interpretation

OpenTrawl follows the Bitter Lesson. Build the strong, general substrate that
lets better models answer harder questions from the same archives; do not turn
today's model interpretation, taxonomy or embeddings into the product's
identity. The value that compounds is robust source access, repeatable search
and open behaviour, stable evidence, bounded interfaces and clear provenance.

Some sources need model work to become discoverable. Photo classification,
OCR and similar enrichment can be legitimate parts of the substrate when they
make otherwise opaque source material searchable. Keep that work adaptable to
stronger models: preserve the original source, separate observed source facts
from model-derived values, record enough provenance to understand and
regenerate the derivation, and make the model implementation replaceable behind
a typed boundary. A particular model's output is derived data, not source truth
and not a permanent product contract.

Most of OpenTrawl is plumbing, so its boundaries must remain clean:

- A crawler owns one provider's access, source semantics and source-native
  archive. Provider-specific schemas and behaviour stay with that crawler.
- Shared `trawlkit` code owns only provider-neutral mechanics that at least two
  crawlers actually use. It must not flatten messages, photos, events and other
  source concepts into a generic ontology merely because they share a search
  surface.
- Protobuf defines the typed control contract between crawlers, `trawl` and the
  Mac app. Consumers use that contract rather than crawler packages or private
  database schemas.
- Derived model interpretation stays separate from source facts, carries its
  evidence and remains disposable and regenerable as models improve.

## What quality means here

Quality is the ordinary user experience, not the amount of engineering around
it. The best OpenTrawl build is quiet: it works, respects the machine and the
person's attention, explains a real failure clearly and does not turn the user
into a computer janitor.

- **Simple, not simplistic.** Choose the smallest design that fully serves the
  product. Every abstraction, mode, process, dependency, byte of storage and UI
  element makes a claim on future attention. Add one only when the real product
  earns it. One obvious path is easier to use, prove, debug and change. Apply
  the Zen of Python, KISS, YAGNI, the Unix philosophy and deep modules with
  small interfaces as engineering judgement, not aesthetic ceremony.
- **Real failures, not imagined failures.** Validate actual inputs and outputs,
  preserve source data and make normal failures traceable. Do not bury the
  operating path under safeguards for hypothetical states. Premature
  optimisation and overly defensive programming are the root of most
  avoidable complexity.
- **Easy to fix and change.** Logs and errors should expose the product event,
  source and action that failed without leaking private content. Observability
  is a property of clear code and useful product boundaries, not a reason to
  invent a telemetry framework.
- **Fix itself when the evidence justifies it.** A known recoverable failure
  should recover quietly when that makes the normal path simpler and safer.
  Do not make the user perform routine repair, and do not invent recovery paths
  for failures the product has never observed.
- **Code is boring plumbing; models do judgement.** Deterministic code owns IO,
  storage, validation, provenance and mechanical facts. Do not encode a growing
  ontology of semantic guesses that a stronger model can make from evidence.
- **The integrated product is the proof.** A diff, test, type-check or review is
  supporting evidence. None substitutes for using the CLI or Mac app as a
  person would and inspecting the result they receive.

## Public-data boundary

- Never commit personal archive content, private databases, real messages,
  contacts, locations, account identifiers, archive-derived counts, secrets or
  screenshots of real data.
- Synthetic data is banned everywhere unless Josh explicitly approves a
  specific use. Do not create synthetic fixtures, examples, records or archive
  content, and do not substitute fake data for private data. Omit the value or
  example entirely; do not turn private archive content into a redacted
  fixture, example or record. Approved synthetic data still cannot prove
  product behaviour.
- Never copy private working context, archive content or task history here.
  Reimplement accepted product logic cleanly.
- Install the repository hooks with `scripts/install-hooks`. Run
  `scripts/check-clean` before every commit. Do not bypass a check, weaken it,
  add a suppression or change data merely to make it pass. A green check proves
  only its named mechanical property, not product quality. If a check conflicts
  with this constitution or rewards worse code, use concrete evidence to
  simplify or remove the check rather than gaming it.
- If material might identify a person or derive from a private archive, keep it
  out of this repository.

OpenTrawl's difficult work lives in the irregularities and history of real
source archives. Synthetic data removes those facts and replaces the product
problem with one we invented, so success against it creates false confidence.
The public-data boundary does not justify fake data: exercise private archives
outside the repository, then record only the behaviour and contract that the
real result established.

## Prove the real operating path first

Prove the smallest clear product path end to end against the user's real
archive data before you add anything that protects, preserves, generalises or
simulates that path. Before proof, those mechanisms encode guesses about a
product that does not yet work.

- Do not add or retain tests, guardrails, fallbacks, migrations, regression
  checks, mutation checks, legacy or backwards compatibility, performance
  optimisations, fixtures or mocks before the human-facing happy path works.
- Run the actual product command or app surface against the real archive.
  Inspect the complete human-facing result and follow its next action through
  to completion.
- A fixture, example, mock, snapshot or synthetic archive cannot prove product
  behaviour.
- If the real path fails, fix only the observed failure and run the real path
  again. Do not build protection around behaviour that has not worked.
- Keep real archive content and proof outside this public repository. Record
  the accepted product behaviour without private values.
- After the real path works, add only the smallest protection justified by its
  observed behaviour or a real failure. A test may exercise only that same
  human-facing path, never an internal proxy or invented scenario.
- Remove any protection in the list above unless the human-facing path is
  proven and its observed behaviour or a real failure justifies that
  protection. Delete every test unless its human-facing path has already been
  proven and the test exercises only that path. No test is better than a test
  of the wrong thing.

A test suite is not harmless scaffolding. It tells future agents what to
preserve and rewards implementations that satisfy its model of the product. A
test written before the real path works, or below the human-facing surface,
freezes an unproven design and makes the wrong implementation harder to remove.
The same reasoning applies to guardrails, fallbacks, migration frameworks,
compatibility layers and speculative performance work.

## Product and repository invariants

OpenTrawl is a local-first crawler suite: one `trawl` CLI and one Mac app over
source-native archives. Read [docs/vision.md](docs/vision.md) for the product
direction. Stable behaviour belongs in the relevant public contract, source
documentation and accepted human-facing path. A test may protect that path
only after the path itself has proved the behaviour.

- Crawlers and the CLI use Go. The Mac app uses Swift and SwiftUI.
- Keep source archives separate and couple surfaces through the shared control
  contract. Derived layers consume that contract, not source internals.
- Prefer the smallest complete design: simple, explicit code; deep modules with
  small interfaces; one obvious path; no speculative knobs, fallbacks or
  compatibility machinery.
- Deterministic code owns IO, storage and mechanical facts. Semantic
  interpretation belongs to a model behind an explicit product seam.
- Human and machine output is bounded, clear when read cold and free of secrets
  and internal identifiers.
- Providers, credentials, endpoints and network effects come from accepted
  product scope and explicit configuration, never an ad hoc library default.

## Data carries domain meaning

All data must be strongly typed. Names make code discoverable; types let an
agent understand and change it without reconstructing hidden meaning from an
implementation. A function signature should identify its real inputs and
output without requiring the reader to open the function body. Read the local
[How coding agents read your
code](.agents/references/how-coding-agents-read-your-code.md) article reference:
precise names are search addresses, and precise types turn incorrect agent
assumptions into useful compiler errors.

- Give different domain concepts different types even when their current
  storage representation is the same. An identifier, state, source kind,
  timestamp or unit is not an interchangeable `string` or integer.
- Use protobuf messages and enums for shared contracts. Do not use strings,
  JSON objects, generic maps, `any`, `Any` or `interface{}` as substitute
  schemas or domain models.
- Raw strings and other untrusted primitives may enter at a system boundary.
  Parse and validate them there, convert them to a named domain type and keep
  them typed inside the system.
- A serialised format may represent a typed value at an external boundary; it
  must not become the source schema or erase the domain types behind it.
- Model real product distinctions so invalid combinations are not representable
  by accident. Do not invent type machinery for theoretical distinctions that
  the source or product does not have.

Strong typing is part of the product contract, not decorative implementation
detail. It preserves source meaning across Go, protobuf, Swift, storage, the
CLI and the Mac app. It also gives an agent an executable correction when it
misunderstands a concept instead of allowing the mistake to travel as another
string.

Consistency is repository-wide, not local to a package or language. One domain
concept has one canonical name, meaning and type across every crawler,
`trawlkit`, protobuf, Go, Swift, SQL, CLI input and output, the Mac app and
documentation. A language-specific representation must map mechanically to
that canonical contract without inventing an alias or a second meaning. When a
concept changes, update every producer, consumer, stored representation and
presentation in the same change; do not leave compatibility aliases or a
second spelling behind.

## Names make the system discoverable

Coding agents usually navigate this repository with text search. Every path,
file, package, type, protobuf message, field, enum value, function, variable
and test name must therefore be a precise and useful search term. A descriptive
type in a compiler error should also provide the next useful `rg` query. Judge
uniqueness across the entire repository: a name that is clear only inside one
package, crawler or language is not clear enough.

Preserve the full meaning of a concept in its name. Do not compress important
information into a short, generic or context-dependent name. Prefer a longer,
exact name to a shorter name that discards meaning.

- Use full words and ADS-STE100 Simplified Technical English. Use common,
  concrete words. Use one term for one concept and one meaning for each term.
- Use an active verb for an operation. Name the object and result when they
  are relevant. Do not make the reader infer them from the surrounding code.
- Include the domain, role, scope and time basis when they affect meaning.
- Use one spelling for each concept throughout paths, Go, Swift, protobufs,
  SQL, CLI surfaces, app UI, logs, tests and documentation.
- A name must make sense without its import, caller or surrounding comment.
- If a comment explains what a named thing is or does, improve the name.
  Comments may explain why a decision exists, an external constraint or an
  invariant that cannot be expressed in a name or type.
- Protobuf names require the highest standard. They define the shared product
  language for Go, Swift, the CLI, APIs and transported data.
- Do not use abbreviations only to save characters.
- Avoid generic names such as `data`, `info`, `value`, `result`, `state`,
  `summary`, `config`, `helper`, `manager`, `process` and `handle` unless the
  complete name makes the exact meaning clear.
- Do not rely on package qualification to rescue a weak exported name.
- Name tests after the exact accepted human-facing product behaviour they
  protect.

Length is not the goal by itself. Semantic completeness and unique
searchability are the goal. A longer name is better when it preserves
information that a shorter name would discard.

Prefer:

- `last_successfully_completed_archive_sync_time` over `last_sync`;
- `archive_content_counts_after_last_successfully_completed_sync` over
  `counts`;
- `trawler_command_help_listing` over `visibility`;
- `listed_in_trawler_overview_and_full_help` over `primary`;
- `trawler_current_operability` over `state`;
- `search_result_matching_text_fragments` over `text`.

Before accepting a name, ask:

1. Could a new agent search for this concept with ordinary human words?
2. Would that search find this definition without many unrelated matches?
3. Does the name contain enough meaning that no comment must explain it?
4. Would the name remain truthful outside this file?

If any answer is no, improve the name before writing more code.

## One live schema before v1

Before the first public v1 release, compatibility machinery preserves mistakes
and creates paths the product does not need. It also teaches agents that stale
and current representations are both valid, multiplying every later decision.
Every protobuf, protocol, database, stored record and interface therefore has
one live schema and one supported path.

- Do not add schema or protocol version fields, version numbers, versioned
  names or suffixes, compatibility branches, dual reads, dual writes or
  fallback decoding. Change the live schema and its consumers directly.
- Preserve source archives. When derived local data no longer matches the live
  schema, prefer regenerating it from source when re-import is cheap.
- If re-import is genuinely expensive, migrate the derived data once with a
  disposable command or script that is not committed to the repository. Then
  re-import the same source and prove the result is idempotent: it creates no
  duplicates and makes no further changes.

The first public v1 release ends the blanket pre-v1 prohibition; it does not
make versioning or backwards compatibility valuable by itself. Add either only
when a real released contract and real users require it.

## Completion

An outcome is complete when the integrated product performs the intended
normal human flow with real input; the human-facing result and its next action
have been inspected; the implementation has one clear path, precise domain
types and discoverable names; observed failures are traceable; and no private
or synthetic data entered the repository. Builds, tests, reviews and documents
may support that evidence, but they never substitute for the working result.
