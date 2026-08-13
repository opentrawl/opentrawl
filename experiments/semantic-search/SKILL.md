---
name: opentrawl-semantic-search-experiment
description: Use the private OpenTrawl lexical-versus-semantic search experiment to investigate the user's own archive. This file describes an experimental interface, not the released OpenTrawl CLI.
---

# Investigate the archive in the search experiment

Your task is to answer the user's question from their OpenTrawl archives. Search
locates evidence. `trawl open` supplies the source record and its surrounding
context. You decide what the evidence means.

This evaluation holds the research instructions constant and compares
end-to-end recall under the assigned retriever. Follow the retrieval mode
assigned in the task and do not use the other mode.

The intended product surface is one command:

```sh
trawl search "natural-language search intent"
```

That command should run hybrid retrieval by default. The `--retrieval` switch
below exists only to hold one retriever constant during this evaluation. It is
not a proposed user-facing choice. The prototype has no query operators or
special syntax for lexical anchors. Write the intent in natural language. If an
exact name, phrase or identifier is evidence-bearing, include it as ordinary
query text.

Do not run `trawl update` or any other command that changes an archive.

## Experimental commands

The task provides two executable paths:

- `TRAWL` is the development OpenTrawl CLI. It includes Gmail and the other
  trawlers enabled for this experiment.
- `TRAWL_SEARCH_EXPERIMENT` is the experimental search front door.

Use `TRAWL` for `status`, `who`, `conversations`, `messages`, `open`, and
source-native traversal. Use `TRAWL_SEARCH_EXPERIMENT` for every content search.

The assigned retrieval mode is either `lexical` or `hybrid`:

```sh
"$TRAWL_SEARCH_EXPERIMENT" --retrieval lexical "natural-language search intent"
"$TRAWL_SEARCH_EXPERIMENT" --retrieval hybrid "natural-language search intent"
```

Both modes use the same frozen corpus manifest, searchable source text, source
scope, total candidate budget, and OpenTrawl record references. Semantic search
splits very long records for embedding, then returns at most one candidate per
canonical source record.

`lexical` spends the whole result budget on exact lexical candidates from the
frozen corpus FTS index. OpenTrawl treats the words as quoted FTS5
tokens joined by AND; punctuation cannot introduce search operators. `hybrid`
starts with an equal lexical and semantic allocation. Unused lexical places
flow to the semantic group so both modes can return the same total candidate
budget. The semantic group comes from vector similarity over a disposable local
index. This experiment keeps the groups separate so their contribution remains
inspectable; it does not yet claim to be the final ordering design. A record
already present in the lexical group is removed from the semantic group, so a
duplicate cannot consume two places in the result budget.

The experiment does not expand the query, generate a hypothetical answer,
rerank candidates, or merge lexical and semantic scores. Scores from the two
retrievers are not comparable. Judge the records, not their scores.

You can scope either mode with `--trawler NAME` and control the bounded result
set with `--limit N`. Run `"$TRAWL_SEARCH_EXPERIMENT" --help` for the exact
grammar. Use `who`, conversations, and source-native commands through `TRAWL`
when the investigation needs identity or container traversal.

## What the indexes can retrieve

OpenTrawl keeps separate source-native archives. The experimental semantic
index contains a mechanical projection of the same searchable source text and
retains the canonical OpenTrawl reference for every chunk.

| Archive | Searchable evidence | What it can establish |
|---|---|---|
| iMessage, WhatsApp, Telegram | Message and supported media text | Enacted behaviour, reported outcomes, informal decisions, reactions and what happened after a plan. Open the message to check speaker and surrounding dialogue. |
| Notes | Titles and bodies, including recovered versions | Deliberate reflection, self-description, plans, project records, lists and changes between versions. Treat these as intent until another record establishes action or outcome. |
| Gmail | Subject and body | Formal commitments, decisions, external correspondence, receipts, bookings, applications, outcomes and longer explanations. Quoted thread history may repeat earlier text. |
| Calendar | Summary, description, location, and participants | Scheduling and intended attendance. It establishes that time was reserved, not that attendance, completion or an outcome occurred. |
| Contacts | Identity fields | Identity resolution for `who` and `--who`; it is not general history recall. |

The initial experiment does not use Photos or X. The search front door limits
both retrieval groups to iMessage, WhatsApp, Telegram, Notes, Gmail and
Calendar even if other sources appear in live status.

## Form useful searches

Start from the evidence the question needs. Split independent questions into
independent searches. State one evidence question as plain natural language.
Use the same query-generation and refinement process in both modes. Revise a
query only from vocabulary or gaps observed in opened records, not from the
retrieval mode.

Search first. Do not begin a recall task by touring people, conversations or
the record tree. Those surfaces organise records after search has found a
useful branch; they do not replace content retrieval.

The lexical group depends on archive wording. The semantic group can retrieve
related wording, but it can also return merely similar material. Semantic
proximity is not factual support.

Use OpenTrawl's person and date filters in source-native follow-up work when the
question supplies those facts. Filters are better than adding a person's
display name or a date as another content word.

Do not stuff unrelated evidence questions into one query. Run another search.
Do not generate a large synonym list before seeing the archive. Results teach
you its vocabulary and which source owns the useful branch.

## Search, then open the evidence

Search results are leads. Open every record used for a material claim:

```sh
"$TRAWL" open RECORD_LINK
```

Copy links and printed commands exactly. Do not reconstruct them.

The normal traversal is `search -> open -> printed next action`. Search finds a
candidate across archives. Open establishes the source record, speaker and
nearby context. Follow the next action only when that context can resolve the
question, such as whether a plan was completed or abandoned.

Candidate order, retrieval group, and repeated wording do not establish
importance. Open competing leads. Prefer evidence that answers the user's
question and recurs independently across time or sources.

OpenTrawl is a typed record tree:

```text
search candidate
└─ open record
   ├─ message → chronological context around the selected message
   │             └─ Messages action for the wider conversation
   ├─ conversation
   │  ├─ Messages action
   │  └─ Participants action
   ├─ person → Conversations action
   ├─ note → bounded body and recovered-version action
   ├─ Gmail message → bounded headers, body and attachments
   └─ calendar event → event detail
```

For a root command that was not printed by prior output, first run
`"$TRAWL" COMMAND --help`. After opening a record, copy its printed next action
exactly. Follow the printed `Messages` action when the meaning of a message
depends on what happened before or afterwards.

Read the complete command output. Do not pipe it through `head`; that can remove
the link or action needed for the next step.

## Assess a broad orientation request

Let the user's question set the areas to investigate. Start wide only when the
request is broad, then follow the few leads that could change the answer. Notes
are valuable for self-description and plans. Messages can establish reported
behaviour and later outcomes. Gmail carries formal commitments and external
outcomes. Calendar establishes scheduling, so confirm completion elsewhere. Do
not let one source stand in for the whole archive.

Keep four different claims separate:

- **Occurrence:** one opened record establishes that something was said, planned
  or happened once.
- **Recurrence:** independent opened records establish repetition across time or
  contexts. Repeated quoted text is still one occurrence.
- **Salience:** the evidence shows that a subject affects decisions, receives
  sustained attention or has practical weight. Frequent mentions can be routine
  noise, so recurrence alone does not establish salience.
- **Identity:** a broad claim about the person. Support it with salient, durable
  evidence from more than one context, and look for evidence that narrows it.

A vivid occurrence is still one occurrence. A handful of similar meals,
projects or trips can establish a recurring activity without making it central
to the person's identity. Prefer the narrowest claim that follows from the
opened evidence.

Separate these distinctions explicitly while investigating:

- the user speaking versus another person speaking about the user;
- an idea, plan, booking, attempt, completion, cancellation, and later outcome;
- observed behaviour versus an inferred motive;
- an old fact versus a current fact;
- repeated evidence versus repeated quoted or forwarded copies of one record.

For a broad conclusion, look for evidence that would weaken or narrow it. A
counter-search can reveal that a project stopped, an apparent preference was
temporary, or a plan never happened. State the narrower conclusion when that
is what the records support.

## Know when the investigation is complete

Set the answer bar from the user's question. For broad orientation, cover the
major supported areas that would change how an unfamiliar agent works with the
user. Resolve shortlisted leads by opening or discarding them. Search gaps that
could materially change the account.

Stop when further searches no longer change the important conclusions or add
material counter-evidence. A fixed number of searches is not a coverage rule.
Neither a long candidate list nor a large citation count proves completeness.

Before answering:

1. Check that every material claim is supported by an opened record.
2. Remove or narrow claims based only on a plan, passing mention, vivid example,
   duplicated thread text, or another person's activity.
3. Distinguish facts from interpretation.
4. State source gaps that could materially alter the answer.
5. Include exact opened OpenTrawl links where the user may want to inspect the
   evidence.

This file belongs to the experiment. Do not install it in a skills directory.
The released OpenTrawl agent instructions must remain independently editable
and update with OpenTrawl itself.
