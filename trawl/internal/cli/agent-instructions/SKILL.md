---
name: search-opentrawl-archives
description: Search and open evidence in a person's enabled OpenTrawl archives through the canonical trawl CLI.
---

# Search OpenTrawl archives

Use OpenTrawl when an answer depends on the person's messages, mail, notes,
calendar or other enabled local archives. Search finds leads. Open supplies the
source record. You decide what the evidence means.

## Search

Use the one canonical command:

```sh
trawl search [--trawler SOURCE] [--who PERSON] [--after TIME] [--before TIME] [--limit COUNT] "natural-language evidence need"
```

Write one short description of the evidence you need. Search independent
questions independently. Add source, person or date constraints only when the
task supplies them. Quotes, `OR`, `NEAR`, wildcards and raw FTS expressions are
not operators.

OpenTrawl keeps each crawler's normal keyword search and may add related
passages from a local semantic index. It groups duplicate source records and
returns one bounded list through the normal renderer. There are no retrieval
modes or model scores to select. When the semantic index or model is unavailable,
or semantic search cannot enforce `--who`, the command says that it is showing
keyword results only.

The current product reads an existing semantic index but does not yet own its
build or refresh lifecycle. Do not improvise a private index command or treat
keyword-only output as semantic coverage.

The semantic index is derived and replaceable. It does not replace source
archives or source facts. A missing result does not prove that the event never
happened, especially when a source reports a failure or is not enabled.

## Open evidence

Search results are leads. Open every record used for a material claim. Copy the
printed command exactly:

```sh
trawl open SOURCE_LINK
trawl open --anchor ANCHOR_ID --start-utf8-byte START --end-utf8-byte-exclusive END SOURCE_LINK
```

The anchored command opens the exact matched passage in its source section.
Do not reconstruct links, anchors or byte offsets. Read complete command output;
do not pipe it through `head`, because the next source-native action can appear
after the visible record.

Follow the opened record's actions when the answer has moved into a known note,
message context, conversation, person, attachment or version history. Do not
keep issuing broad root searches when a narrower source-native action answers
the remaining question.

## Make claims at the evidence level

- A note or calendar event can prove a plan, not completion.
- One message can prove that something was said or reported once.
- Recurrence needs independent records across time or contexts.
- A current-state claim needs a search for later completion, cancellation,
  replacement or contradiction.
- Keep source facts separate from your interpretation.

Before answering, open the evidence for every material claim, narrow claims
supported only by plans or passing mentions, and state source or filter
limitations that could change the answer.

Read this shipped copy with `trawl agent-instructions`. Do not install a private
copy: product guidance and product behaviour must change together.
