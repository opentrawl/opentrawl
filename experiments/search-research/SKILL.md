---
name: search-opentrawl-archives
description: Search and open evidence in a person's messages, mail, notes, calendar and other enabled OpenTrawl archives. Use for exact facts, histories, outcomes, relationships, recurring patterns or broad orientation through the `trawl` CLI.
---

# Search OpenTrawl archives

Answer the user's question from their source archives. Search locates leads.
`trawl open` supplies the exact source record and its typed next actions. You
decide what the opened evidence means.

Use one search command:

```sh
trawl search [--trawler SOURCE] [--after TIME] [--before TIME] [--limit COUNT] "natural-language evidence need"
```

This experimental slice does not support `--who`. Do not use it for a task
whose answer depends on a person filter. It cannot replace released OpenTrawl
search for that path yet.

Every search prints its corpus scope before the results. This disposable corpus
may contain one source only or a bounded number of records per source. A miss
means only that the active corpus returned no candidate. It does not establish
that the source archive, another source or the person's history has no matching
record. State the printed corpus scope before making a material negative claim,
and do not make the claim when the missing scope could change it.

Do not inspect the repository or locate databases before searching. Do not run
`trawl update` or another command that changes an archive unless the user asks
for that change.

## Know what this search does

The command searches one disposable passage corpus derived at the index
boundary from OpenTrawl's existing typed source records. Every passage retains
its exact source record, source-owned result group, source time, source section
and open anchor. Search results use the same typed presentation and renderer as
ordinary `trawl search`; the index does not define another archive or output
model.

One query feeds two internal candidate generators:

- SQLite FTS5 splits ordinary query text into Unicode letter and number tokens
  and searches those tokens with `OR`. It retrieves direct archive wording.
- A local embedding model retrieves passages with related meaning when the
  archive uses different words.

OpenTrawl takes up to 100 source records from each lane, groups competing
records by the source's result identity, and alternates the two lane ranks into
one bounded list. Notes are the important grouping case: recovered versions
remain searchable, but only the best matching version of one note occupies a
result slot. Opening that result still opens the exact matched version.

The implementation does not expand synonyms, generate an answer, classify the
query or infer a source. It exposes no search modes or model scores. Candidate
order is a coverage-preserving presentation, not a claim that the first result
is the most important fact in the archive.

The current proof uses one provisional passage size and one provisional local
embedding deployment. Search syntax and source evidence are the contract; the
passage size, model and dense-search backend are not product decisions.

Read the implementation when exact mechanics matter:

- [`search.go`](search.go) owns token-OR FTS, exact dense candidates, grouping
  and the single result list.
- [`chunk.go`](chunk.go) owns the shared natural-boundary passage split.
- [`searchable_record.proto`](../../trawlkit/proto/trawl/searchable_record/searchable_record.proto)
  is the crawler-to-index contract.

## Form searches that match the evidence need

Write one short natural-language description of the evidence you need. Search
independent questions independently. Add a source or date constraint only when
the task supplies that fact.

Good:

```sh
trawl search "formal outcome of the contract negotiation"
trawl search --trawler gmail "booking cancellation and refund"
trawl search --after 2025-01-01 "what happened after the launch plan"
```

Why these work: each query describes one evidence need. Direct wording can
enter through FTS; related wording can enter through dense retrieval. The
filters remove ineligible records before either lane ranks them.

Weak:

```sh
trawl search "when did I finally agree to renew the lease and did the landlord sign it and what happened next"
trawl search "passionate obsessed adventurous favourite"
trawl search "cancelled OR postponed OR delayed"
```

Why these are weak: the first combines several evidence questions; the second
searches conclusions rather than behaviour; the third assumes Boolean syntax
that this interface does not expose. Split them:

```sh
trawl search "agreement to renew the lease"
trawl search "signed lease or extension outcome"
trawl search "lease renewal later outcome"

trawl search "cancelled"
trawl search "postponed"
trawl search "delayed"
```

Quotes, `OR`, `NEAR`, wildcards and raw FTS5 expressions are not query
operators here. Exact names, phrases and identifiers are still useful query
text because the lexical lane sees their tokens. Run another search for an
alternative term instead of constructing a query language.

Start with search. Do not begin broad recall by touring every person,
conversation, folder or calendar. Those branches organise a useful lead after
search finds it; they are not a substitute for content retrieval.

## Use each source for what it can establish

OpenTrawl keeps source archives separate. The shared index does not erase what
each source means.

| Archive | Searchable evidence | What an opened record can establish |
|---|---|---|
| iMessage, WhatsApp, Telegram | Message and supported media text | Reported or enacted behaviour, informal decisions, reactions and later outcomes. Open the message to check speaker and surrounding dialogue. |
| Notes | Titles and bodies, including recovered versions | Deliberate reflection, self-description, plans, project records and changes between versions. A note can prove intent without proving execution. |
| Gmail | Subject and body | Formal commitments, decisions, correspondence, receipts, bookings, applications and external outcomes. Quoted thread history can duplicate earlier evidence. |
| Calendar | Summary, description, location and participants | Scheduling and intended attendance. It does not by itself prove attendance, completion or outcome. |

This bounded experiment indexes these six sources. Photos and X are outside
this slice even if another build exposes them.

For broad work, use source types as different evidence roles, not quotas.
Notes can explain intent. Messages can show what was reported or enacted.
Gmail can settle a formal outcome. Calendar can locate a planned event whose
completion must be checked elsewhere. Do not let the highest-volume message
archive stand in for the whole person.

## Search, open and follow the tree

Search results are leads. Open every record used for a material claim. Copy the
printed command exactly:

```sh
trawl open SOURCE_LINK
trawl open --anchor ANCHOR_ID --start-utf8-byte START --end-utf8-byte-exclusive END SOURCE_LINK
```

The anchored command opens the exact matched passage of an anchored Note,
Gmail or message-text section. Both offsets are relative to the trimmed UTF-8
text at the anchor, and `END` is exclusive. Open keeps the normal record title,
fields, context, links and actions, and prints a marker when text is omitted
before or after the passage. The ordinary command opens a complete bounded
record whose searchable section has no exact opened-text mapping. Calendar,
WhatsApp and Telegram media results use that path in this slice. Do not
reconstruct links, anchors or byte offsets.

The normal path is:

```text
search
  -> one candidate
    -> printed Open command
      -> exact source passage or complete bounded source record
        -> printed source-native action when more context is needed
```

OpenTrawl is a typed record tree:

```text
message -> chronological context -> wider conversation messages
conversation -> messages and participants
person -> conversations
note -> exact matched passage and recovered versions
Gmail message -> headers, body and attachments
calendar event -> event details
```

Follow a printed next action when it resolves the remaining question. For a
message, surrounding dialogue establishes speaker and meaning; the wider
conversation can establish what happened later. For a note, versions can show
how a plan changed. Do not keep issuing root searches after the answer has
moved below a known record or conversation branch.

Read complete command output. Do not pipe it through `head`; that can remove
the link or action that defines the next traversal.

## Build a broad account from behaviour, not adjectives

For a broad orientation request, begin with independent evidence areas that
could change how an unfamiliar agent works with the user: close relationships,
repeated activities, commitments and projects, places, explicit reflections,
enacted choices and later outcomes. Search those areas separately. Open the
strongest leads. Let archive vocabulary suggest the next query.

Keep four claims separate:

- **Occurrence:** one opened record shows that something was said, planned or
  happened once.
- **Recurrence:** independent opened records show repetition across time or
  contexts. Forwarded or quoted copies remain one occurrence.
- **Salience:** the evidence shows sustained practical weight or influence on
  decisions. Frequency alone can be routine noise.
- **Identity:** a durable claim about the person. It needs salient evidence
  across more than one context and a search for facts that narrow it.

One vivid dish proves one meal. Several dishes, recipe discussions,
ingredient choices and meal plans across time can support a sustained interest
in cooking and may support cuisine preferences. The vivid dish remains an
example; it does not become an obsession.

Separate the archive owner's activity from another speaker's activity. Keep an
idea, plan, booking, attempt, completion, cancellation and later outcome
distinct. Describe observed behaviour before assigning a motive or
psychological label.

Before presenting a material claim as current, search for its latest state.
Look for completion, cancellation, replacement, contradiction or the newest
unresolved evidence. An old, well-supported record does not establish the
present when a later outcome exists elsewhere.

## Finish when the answer is covered

Set the evidence bar from the question, not from a search-count cap. Resolve
shortlisted leads by opening or discarding them. Search gaps and counter-
evidence that could materially change the answer. Stop when another deliberate
query no longer changes an important conclusion or adds material later
evidence.

Before answering:

1. Verify every material claim against an opened record.
2. Narrow claims based only on a plan, passing mention, vivid example,
   duplicated thread text or another person's activity.
3. Distinguish source facts from your interpretation.
4. State a source or filter limitation when it could change the answer.
5. Include exact opened OpenTrawl links where the user may inspect the evidence.

This file ships with the OpenTrawl experiment and must remain easy to edit with
the product. Read the shipped copy with `trawl agent-instructions`. Do not
install or copy it into a personal skills directory. A human-owned `AGENTS.md`
may point to that command so product updates and operating guidance stay
together.
