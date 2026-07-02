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

## What this repo is

OpenTrawl: local-first crawlers for your digital life, one `trawl` CLI,
one Mac app. Read [docs/vision.md](docs/vision.md) first, then
[docs/roadmap.md](docs/roadmap.md).

## Layout and upstreams

Each crawler directory is a git subtree synced with an upstream repo.
Do not edit a subtree without knowing where the change will land; see
[docs/sync.md](docs/sync.md) and run `scripts/sync list`.

| directory | upstream | outbound path |
|---|---|---|
| crawlkit | openclaw/crawlkit | direct (maintainer) |
| imsgcrawl | openclaw/imsgcrawl | direct (admin) |
| photoscrawl | openclaw/photoscrawl | direct (admin) |
| telecrawl | openclaw/telecrawl | via joshp123/telecrawl fork + PR |
| wacrawl | openclaw/wacrawl | via joshp123/wacrawl fork + PR |
| clawdex | openclaw/clawdex | via joshp123/clawdex fork + PR |

Everything else (docs, scripts, the `trawl` CLI, the app) is
monorepo-native with no upstream.

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

## Standards

- Go for crawlers and the CLI, SwiftUI for the Mac app.
- Code must be self-documenting: no magic constants, one obvious job per
  function. If a reader needs a comment to follow the code, rewrite the
  code.
- Keep files under about 500 lines.
- Prose (docs, PRs, commits) follows plain-language style: front-loaded,
  short sentences, sentence case, British English, no filler.
- Outputs of every crawler command must be readable by a human cold;
  that is the bar that makes them agent-safe too.

## Upstream tool drift

Upstream tools such as `gogcli` and `crawlkit` move fast. Pin minimum
versions explicitly, and periodically re-check upstream for new
primitives before building workarounds. Concrete example: `gogcrawl`
originally paginated Gmail search because the pinned `gog` 0.11
predated `gog backup gmail`; the crawler now depends on the backup
pipeline instead.

## Blockers are surfaced, not sat on

When work is aligned with the roadmap and ready, fire it — do not
announce and wait. When a task needs a human decision, put the
concrete question with options in front of Josh the moment it becomes
blocking, not in a status list. Track every blocked item with what
unblocks it. The inverse also holds: never invent work to look busy —
aligned and ready, or it doesn't run.
