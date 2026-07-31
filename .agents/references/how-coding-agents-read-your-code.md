---
title: How coding agents read your code
source: https://modem.dev/blog/how-coding-agents-read-your-code
source_author: Ben Vinegar
source_published: 2026-07-20
source_downloaded: 2026-07-31
---

# How coding agents read your code

This local reference records the engineering conclusions OpenTrawl uses from
Ben Vinegar's Modem article, [How coding agents read your code (and how to write
for them)](https://modem.dev/blog/how-coding-agents-read-your-code). The source
article was downloaded and converted to Markdown with `markitdown`. This file
is an attributed working reference, not a copy of the article.

## Relevant conclusions

Coding agents usually navigate a repository by searching plain text, reading a
small window around a result and refining the search. Files, exported symbols,
types, methods, errors and comments are therefore retrieval addresses. A vague
or misleading name increases unrelated matches, consumes context and makes a
wrong answer more likely.

Names should preserve the domain words a person or agent would search for.
Paths and package qualification cannot reliably rescue a generic symbol
because reverse searches often start from the symbol or concept. One concept
should use one spelling across the repository so each search finds the complete
implementation rather than fragments hidden behind synonyms.

Precise types reduce the amount of implementation an agent must inspect and
turn incorrect assumptions into useful compiler errors. Distinct domain values
need distinct types even when they share a primitive representation. A type's
name matters because it appears in compiler feedback and becomes the agent's
next search query. Untyped escape hatches remove both enforcement and that
searchable correction.

Put the small amount of explanation that code cannot express at the definition
where a search lands. Comments explain constraints and reasons; they do not
compensate for names or types that hide meaning.

## OpenTrawl application

[OpenTrawl's root AGENTS.md](../../AGENTS.md) is authoritative. It applies these
findings across the entire repository and defines the stronger project-specific
rules for semantic completeness, canonical cross-language vocabulary, strong
domain types, human-facing tests and the removal of pre-v1 compatibility paths.
