---
written_by: ai
---

# OpenTrawl for Mac

The Mac app is the human interface to the same local archives and contracts as
the `trawl` CLI. Both surfaces preserve source identities, match semantics,
record refs and failure meanings; they differ only in presentation and
interaction.

## Product promise

Search your local digital life from one calm Mac workspace. Recognise the right
item, see why it matched and read its source-owned context without losing your
place.

## Product model

The constellation is the home and source map. The central diamond searches all
available sources; a source control scopes the same search to that source.

Search is a stable workspace:

```text
constellation -> query -> matching row -> source-owned record at the match
```

There is no intermediate evidence page. A row helps someone recognise a
result. Selecting it opens the bounded record and anchors the exact match.

## Search matches

A result is a match, not merely a record. It carries the source, record ref,
target anchor, concise source-owned summary, labelled evidence and the small
facts needed to recognise it.

The source owns those meanings. Federation validates, combines and orders the
matches without rewriting them. The protobuf boundary and Swift client preserve
the same facts, and the app renders them without switching on a source ID.

Evidence is exact provenance with a resolvable anchor. It may be message text,
an email passage, a calendar field, a contact field, OCR, an attachment name or
another source-native fact. It is not an invented ranking explanation.

## Opened records

Every source returns a typed value for machine consumers and a required bounded
presentation for the CLI and app. The presentation grammar supports prose,
fields, timelines, media, attachments, actions and notices without forcing all
sources into one universal record.

Long conversations and histories open around the matched target and say when
more exists. The app never hides an unbounded dump behind a click.

## Search workspace

- All-source and source-scoped search use the same field and make the scope
  visible.
- The last committed result page stays stable while a revised query runs. Old
  results are never presented as the answer to a new draft query.
- A wide window keeps the result list and opened record together. A narrow
  window drills into the record and returns to the unchanged list.
- Escape cancels the current edit or returns focus; it does not discard the
  search.
- Keyboard and VoiceOver users can reach every source, result, selected state,
  record action and failure meaning.

Rows lead with the thing a person recognises: the conversation or people,
email subject, event, note, photo, post or contact. Detail leads with content,
not archive machinery.

## Source presentation

- Messages show conversation identity, direction and the matching message in a
  readable timeline.
- Email shows sender and subject, then a sanitised body with plain-text
  fallback. Remote content is blocked by default.
- Calendar leads with title, time, place and people.
- Notes lead with the note and matching passage.
- Photos show a bounded local preview before OCR and camera metadata.
- Twitter is named `Twitter (X)` and leads with the matching post.
- Contacts lead with the person and matching field.

This is one rendering system with source-authored documents, not one bespoke
screen per source and not one code-like metadata table.

## Constellation

- Source controls have equal visual and interaction weight.
- Positions are deterministic, balanced and spatially stable. The graph is a
  connected network around the diamond, not a status dashboard.
- Each control makes its action and source contents visible without hover.
- Healthy sources stay quiet. A problem replaces normal supporting text only
  when the person can act on it.
- Ambient motion is decorative. Search or sync activity uses a distinct and
  truthful state. Reduce Motion produces a static composition.

The app does not show green health dots, routine last-sync labels, unexplained
spinners, global warning banners or `Needs sync` housekeeping.

## Honest states

The app distinguishes no matches, partial source failure, total failure,
timeout and a source that is not set up. A problem appears once, next to the
work it affects, and states which sources contributed.

Result bounds are factual. The app may state how many results it shows and
whether more exist. It does not claim `Top 20` without a defined and tested
ranking policy.

## Cross-source ordering

Scores from separate source databases are not automatically comparable, and a
busy source must not silently crowd out the others. Ordering is evaluated on
frozen synthetic candidate sets for recall, time to the correct open, wrong
opens, source starvation, stability, evidence correctness and latency. The
simplest policy with a clear measured win should be used and labelled honestly.

## Design principles

1. One local memory, several clients.
2. The constellation is the corpus home and a real scope control.
3. A result identifies the match, not just its container.
4. The row supports recognition; selection opens the record at the match.
5. Sources own meaning; federation composes; clients render and interact.
6. Human presentation is required, bounded and source-authored.
7. Preserve place through stable refs, not a second private UI archive.
8. Remove anything that does not help someone choose a source, recognise a
   result, read a record or act on a problem.
9. Use motion, colour, size and status only for facts the product knows.
10. Prefer one obvious path, safe defaults and no settings zoo.

Public tests and screenshots use synthetic data. Local verification may read a
real archive but never publishes its contents or statistics.
