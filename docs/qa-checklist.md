---
written_by: ai
---

# Output QA checklist

Use this checklist when a change touches output code. The default answer is
reject until every defect is fixed or filed with a clear owner.

Run `scripts/output-dump` before review. A reading agent then reads the full
transcript against section 1. This applies to any landing that touches:

- `*output*.go`
- `render/`
- crawlkit's `result.go` or any `render_*.go`
- `search.go` or `search_flags.go`

Until scheduling exists, `scripts/output-dump` is a manual or on-demand dump.
Use the same dump and checklist to catch data-shape drift from real archive
changes before it becomes the normal output.

## Round 1: consistency baseline

Check every crawler and both human and JSON output.

- command hints name real commands that work, such as `trawl <source> open REF`
- labels, headings, ref labels and field order mean the same thing across
  crawlers
- refs are clear, source-prefixed in JSON and usable in `open`
- counts and other large numbers use the same readable format
- empty states say what is empty and what to do next
- `who` and `where` identify a person, sender, chat, place or event location
  without repeating the same raw value twice
- JSON is valid and has the same facts as the human output
- JSON keeps machine-only sugar out of the contract, including short refs where
  the contract says full refs
- output does not expose raw row IDs, enums, Apple constants, base64 blobs,
  Go struct dumps or `key=value` debug text
- error and doctor remedies tell the reader the next useful action
- no source prints deleted binary names or crawler-internal command shapes

Fix every failed item when the fix is local and clear. File it when the fix
needs a separate design choice or a different lane. Do not wave defects through
because another lane is expected to change nearby strings.

## Round 2: human usability

INACTIVE until the TRAWL-1 consistency baseline lands.

This round asks whether a person can use the output to do the job, not only
whether the strings are consistent.

- can they find a result with the likely query words
- can they tell which source, person, place and time the result belongs to
- can they copy the displayed ref and open the right item
- can they read the opened item without needing hidden source knowledge
- can they decide what to do after an empty result, partial result or doctor
  warning
- does the output hide detail that a reader would still need to ask for
