# Timeline

`clawdex timeline <person>` prints every note for one person, sorted by
`occurred_at`. It's the fastest way to remember what's been going on with
someone.

```bash
clawdex timeline sally
clawdex timeline sally --limit 50
clawdex timeline sally --json
```

Default output is a labelled table, newest first, 20 notes unless
`--limit` says otherwise:

```text
Notes for Sally O'Malley: showing 3 of 3, newest first.
Add one: clawdex note add "sally" --kind note --source manual --text TEXT

date              where    text
2026-05-08 09:15  dm       Follow up about dinner
2026-04-22 08:01  dm       Sent recipe link
2026-04-12 19:30  meeting  Drinks at Bar Centrale
```

`--json` returns the full note objects, including bodies, topics, and IDs,
inside a `{person_id, person, notes, total, truncated}` envelope.

## Resolving the person

The argument is the same query string that
[`clawdex person show`](people.md) accepts: an ID slug, a name substring,
an email, or a phone number. The first unambiguous match wins.

## Reading flow

Pair `timeline` with `search` for a quick "what was that about" loop:

```bash
clawdex search "negroni"               # find the conversation
clawdex timeline sally | head -20      # surrounding context
```

Or pipe into `less` for long histories:

```bash
clawdex timeline sally | column -t -s $'\t' | less -S
```

## Limits

- Sort key is `occurred_at`, not file mtime. Edit `occurred_at` in the note
  frontmatter to fix ordering after the fact.
- Only one person at a time. To get a multi-person timeline, use
  [Search](search.md) with a date-ish term, or grep notes directly:

  ```bash
  rg -n "occurred_at: 2026-05" ~/.clawdex/contacts/people
  ```

## Related pages

- [Notes](notes.md), [People](people.md), [Search](search.md)
- [Markdown Storage](markdown-storage.md)
