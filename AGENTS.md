---
written_by: ai
---

# AGENTS.md

This repo is public. Everything committed here is published to
github.com/opentrawl/opentrawl. Read the privacy rules before any other
work.

## Privacy rules

1. Never commit personal data. That means: real message, mail, note or
   calendar content; archive databases; phone numbers; personal email
   addresses; account handles used as data (public code handles are
   fine); absolute paths containing a real username; screenshots showing
   real data; counts or statistics taken from a real person's archive.
2. Test data must be synthetic. Use `example.com` addresses, `+1555`
   phone numbers, and invented names.
3. The private workspace (`~/code/crawlers` on Josh's machine) is not
   part of this repo. Never copy a file from there into here. If logic
   must migrate, rewrite it clean; do not carry files or history across.
4. Run `scripts/check-clean` before every commit. CI runs it on every
   push. If it flags something, fix the content; never work around the
   check.
5. If you are unsure whether something is private, it is. Stop and ask.

## Scope discipline

1. External service endpoints and API integrations come only from explicit
   application configuration. Library code validates shape; it does not choose
   providers.
2. Do not add provider integrations, endpoints, API keys, network calls or side
   functionality beyond what the ticket explicitly asks for.
3. If a task seems to need a new endpoint, provider, credential or side
   surface, stop and ask the maintainer before coding it.

## What this repo is

OpenTrawl: local-first crawlers for your digital life, one `trawl` CLI,
one Mac app. Read [docs/vision.md](docs/vision.md) first, then
[docs/roadmap.md](docs/roadmap.md).

## Engineering rules

Go for crawlers and the CLI, SwiftUI for the Mac app.

The bar in one paragraph: boring code — simple, explicit, local,
readable; files under about 500 lines; cognition belongs to models
and shells stay deterministic (code does IO, batching, exact matching
and mechanical transforms, but any semantic decision — is this a
receipt, which item, what quality — goes to a model); and every
agent-produced diff gets a model principles review before commit,
because human review is for taste, not for catching violations.
Outputs must read clearly to a human cold, which is what makes them
safe for agents too.

The full distilled ruleset — code bar, output/design bar,
architecture, model grounding, process rules, ticket quality gates,
and the war stories behind them — lives in the private workspace at
`~/code/crawlers/docs/rules.md`. Agents on Josh's machine read it
before writing any code or artifact; on any conflict it wins. Never
copy its text into this public repo.

## Layout

Crawler modules live under `trawlers/` with surface-name directory
names. They are edited in this monorepo. `trawlkit`, docs, scripts,
the `trawl` CLI and the app are edited here too.

## Documentation rules

All documentation — READMEs, docs/, PR text, error messages, anything a
person reads — follows [docs/style.md](docs/style.md) (the anti-slop
style: plain English, front-loaded, sentence case, no filler) and is
written for end users:

1. Respect the reader. Assume they are smart, busy, and new here.
2. Be conscious of their time: front-load the point, cut what does not
   help them act.
3. Write external-facing and explanatory, always — explain what and
   why, not internal status. Writing for outsiders is also what keeps
   internal thinking sharp.
4. Internal working files may exist but are held to the same standard;
   good notes produce good work.
5. Every generated document gets adversarial review against these rules
   and docs/style.md before it merges. Reviewers are told to refute,
   not to approve.

## Blockers are surfaced, not sat on

When work is aligned with the roadmap and ready, fire it — do not
announce and wait. When a task needs a human decision, put the
concrete question with options in front of Josh the moment it becomes
blocking, not in a status list. Track every blocked item with what
unblocks it. The inverse also holds: never invent work to look busy —
aligned and ready, or it doesn't run.

## Josh reviews inline, in the files

Josh reviews both ways: inline comments saved into the files
(`-> COMMENT` style) and chat messages. Before editing
any doc, re-read it from disk — pattern-editing against a remembered
version silently skips his commented lines and can clobber his review.
Treat comment lines as review to answer, never text to delete; remove
them only when resolving that comment with him.
