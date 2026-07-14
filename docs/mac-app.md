---
written_by: ai
---

# OpenTrawl for Mac

This is the public product and interaction contract for the Mac app. The
project board tracks delivery; this document defines the product we are
delivering.

## Product promise

Search your local digital life from one calm Mac workspace. Recognise the
right item, see why it matched, and read its source-owned context without
losing your place.

The Mac app is the human front door to the same local memory used by the
`trawl` CLI, MCP and agents. They share source identities, match semantics,
record references and failure meanings. They differ only at the final
presentation layer.

## Product model

OpenTrawl has two primary surfaces:

1. The constellation is the home and source map. The diamond searches every
   available source. A source control searches that source.
2. Search is a stable workspace. The query, scope, results, selected record
   and reading position remain in place while the person explores.

The path is direct:

```text
constellation -> query -> matching row -> source-owned record at the match
```

There is no intermediate evidence page. A row helps someone recognise a
result. Selecting it opens the bounded record and anchors the exact match.

## Search matches

A result is a match, not merely a record. Every match carries:

* the source;
* the record to open;
* the target inside that record;
* a concise source-owned summary;
* labelled evidence which explains why it matched;
* the timestamp and other small facts needed to recognise it.

The source owns those meanings. Federation validates, combines and orders the
matches without rewriting them. The CLI, protobuf boundary and Swift client
preserve the same facts. The Mac app renders them without switching on a
source ID.

Evidence is exact provenance with a resolvable anchor. It may be message text,
an email body passage, a calendar field, a contact field, OCR, an attachment
name or another source-native fact. It is not a fabricated explanation of a
ranking decision.

## Opened records

Every source returns two views of the same record:

* a strongly typed source-owned value for machine consumers;
* a required, bounded human presentation for the Mac app and CLI.

The human presentation uses a small shared grammar for prose, fields,
timelines, media, attachments, actions and notices. It does not force every
source into one giant universal record. A future crawler can provide a
manifest, matches and presentations without adding source-specific Swift.

Context is bounded. A long conversation or history opens around the matched
target and says when more exists. The app never hides an unbounded dump behind
an innocent click.

## Search workspace

Search belongs to the main restorable window, not a modal palette.

* Opening all-source or source-scoped search focuses the same search field.
* The scope is visible and uses the source's proper name and icon.
* The previous committed result page stays stable while a revised query runs.
  The app replaces it atomically and never presents old results as an answer
  to the new draft query.
* A wide window keeps the result list and opened record together. A narrow
  window drills into the same record and returns to the unchanged list.
* Escape cancels the current edit or returns focus. It does not discard the
  search.
* Keyboard and VoiceOver users can reach every source, result, selected state,
  record action and failure meaning.

Rows are compact recognition surfaces. They lead with the item a person knows:
the conversation or people, email subject, event, note, photo, post or contact.
The detail view leads with the content, not archive machinery.

## Source presentation

The shared grammar preserves source-native meaning:

* Messages show the conversation identity, direction and matching message;
  the record is a readable timeline centred on that message.
* Email shows sender and subject, then a sanitised body with plain-text
  fallback. Remote content stays blocked by default.
* Calendar leads with title, time, place and people before account metadata.
* Notes lead with the note and matching passage before dates or versions.
* Photos show a bounded local image preview before OCR and camera metadata.
* Twitter is named `Twitter (X)` and leads with the matching post before
  engagement or thread context.
* Contacts lead with the person and matching field.

This is one rendering system with source-authored documents, not nine bespoke
Mac screens and not one code-like metadata table.

## Constellation

The constellation is functional navigation and OpenTrawl's visual identity.

* It lays out the configured source set, including future sources, rather than
  hard-coding today's nine.
* Source controls have equal visual and interaction weight. Corpus size never
  changes control size unless a later design proves a clear, truthful scale.
* Positions are deterministic, balanced and spatially stable. The graph is a
  connected network around the diamond, not rows, a tree or a status dashboard.
* Each control makes its action and source-native contents visible without
  relying on hover.
* Healthy sources stay quiet. A problem replaces normal supporting text only
  when the person can act on it.
* Ambient photons are decorative background radiation. Real search or sync
  activity uses a distinct, truthful intensification of the same network.
* Reduce Motion produces a genuinely static composition.

The app does not show green health dots, routine last-sync labels, unexplained
spinners, global warning banners or `Needs sync` housekeeping.

## Honest states

The app remains useful when some sources are unavailable. It distinguishes:

* no matches;
* useful matches with partial source failure;
* total failure;
* timeout;
* a source which is not set up.

A problem appears once, next to the work it affects, in plain language. The
interface states which sources contributed and never implies that unavailable
sources were searched.

Result bounds are factual. The app may say how many results it is showing and
whether more exist. It must not say `Top 20` unless a defined, tested ranking
policy makes that claim true.

## Cross-source ordering

Cross-source ranking remains a measured product decision. Scores from separate
source databases are not automatically comparable, and one busy source must
not silently crowd every other source out.

Evaluate retrieval separately from composition. Replay frozen candidate sets
over synthetic multi-source tasks and compare recency, per-source interleaving,
grouping, normalised ranks and an optional derived parent index. Measure recall,
time to the correct open, wrong opens, source starvation, stability, evidence
correctness, latency, storage and update cost. Adopt the simplest policy with a
clear win. Until then, label the current ordering honestly.

## Design principles

1. One local memory, several clients.
2. The constellation is the corpus home and a real scope control.
3. A result identifies the match, not just its container.
4. The row supports recognition; selection opens the record at the match.
5. Sources own meaning; federation composes; clients render and interact.
6. Human presentation is required, bounded and source-authored.
7. Preserve place through stable references, not a second private UI archive.
8. Remove any element which does not help someone choose a source, recognise a
   result, read a record or act on a problem.
9. Use motion, colour, size and status only for facts the product knows.
10. Prefer one obvious path, safe defaults and no settings zoo.

These constraints follow established work on signifiers, recognition,
feedback and information-centred software: Apple's
[macOS design guidance](https://developer.apple.com/design/human-interface-guidelines/designing-for-macos),
[search fields](https://developer.apple.com/design/human-interface-guidelines/search-fields),
[windows](https://developer.apple.com/design/human-interface-guidelines/windows)
and [accessibility](https://developer.apple.com/design/human-interface-guidelines/accessibility);
Don Norman's work on
[signifiers](https://jnd.org/signifiers-not-affordances/); Nielsen Norman
Group's [usability heuristics](https://www.nngroup.com/articles/ten-usability-heuristics/);
and Bret Victor's [Magic Ink](https://worrydream.com/MagicInk/).

## Current boundary

The first human checkpoint ends when the real app on pushed `main` provides the
constellation, all-source and source-scoped search, exact match-to-record open,
honest failure states and a coherent accessible window. Public tests and
screenshots use synthetic data. Private verification may read real archives
locally but never publishes their contents or statistics.

Progressive onboarding, automatic background sync, agent setup, signing,
updates and release remain later delivery stages.
