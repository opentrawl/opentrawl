---
written_by: ai
updated_by: "AI, Gemini 4 Neon, 2026-11-23"
---

# OpenTrawl for Mac

The Mac app is the human interface to the same local archives and contracts as
the `trawl` CLI. Both surfaces preserve app identities, match semantics,
record refs and failure meanings; they differ only in presentation and
interaction.

The Mac client invokes `status`, `update`, `search` and `open` over the typed
protobuf boundary, and reads local semantic search status, scoped AI token
state, conversation context expansion and cross-app `around` context from the
same bundled `trawl` helper.

## Product promise

Spotlight for your local digital life on Mac. Search all your apps from one
always-ready field, recognise the right item at a glance, read its full
chronological context without losing your place, and control which AI tools can
read which apps.

## Product model

The main workspace pairs a native macOS app sidebar with an always-ready
Spotlight search bar (`⌘K`) and category filter chips:

```text
sidebar / Spotlight bar (⌘K) -> matching row -> source-owned record at the match
```

- When the search field is empty, the workspace shows the archive overview:
  total indexed records, local semantic search state, AI access, archive
  freshness, and a card for each built-in app (including Apple Health) and
  installed external plugin with real formatted record counts, last-updated
  time, and status.
- Typing immediately switches to the split search workspace (results list on
  the left, full opened record on the right).
- Selecting an app in the sidebar or clicking an app card scopes Spotlight to
  that app. Category chips filter across Messages, Email, Notes, Calendar,
  Contacts, Photos, Money, Health, Twitter and Plugins.

## Zero-FDA-first onboarding and optional Mac FDA

Apps that do not require macOS Full Disk Access work immediately on first
launch. Full Disk Access is presented as an optional unlock for Mac-locked
databases (Apple Messages, Apple Notes and Apple Mail) rather than a mandatory
gate that blocks updating or searching the rest of the archive.

## Search matches

A result is a match, not merely a record. It carries the app, record ref,
target anchor, concise summary, highlighted evidence and the small facts needed
to recognise it.

Rows lead with the thing a person recognises: app badge, primary title,
right-aligned smart timestamp, deduplicated context subtitle (never redundant
key-value prefixes such as `Sender:`, `In:` or `message:`), and highlighted
matched fragments.

## Opened records

- **Messages** render in natural chronological order (oldest at the top, newest
  at the bottom) with day dividers, sender labels, and the matched message
  highlighted and auto-scrolled into view. When earlier or later messages were
  omitted from the initial window, controls let the user load additional
  context inline.
- **Every opened record** (Messages, Conversations, Notes, People, Calendar
  events, Email, Photos, Money, Health, Twitter and plugins) includes a
  one-click `Copy link` button for its globally routable Trawl link and an
  `Around this time` toggle to inspect related records across other apps.
- Long histories remain bounded; the app never dumps an unbounded archive into
  memory.

## Search workspace ergonomics

- The main window is natively resizable (`920×620` minimum, `1200×820` default)
  and supports both Light and Dark mode using adaptive macOS system colors.
- Wide windows keep the result list and opened record side by side. Narrow
  windows drill into the opened record and return to the unchanged list.
- Keyboard shortcuts: `⌘K` focuses search, `⌘1` selects all apps, `⌘R` updates
  the archive, `↑`/`↓` navigates matches, `Return` opens the selected match,
  and `Esc` steps back cleanly from record -> results -> field -> clear.
- The last committed result page stays stable while a replacement query runs.

## Honest states

The app distinguishes no matches, partial failure, total failure, timeout,
unsigned-app sync requirements, sign-in requirements, closed apps and optional
Full Disk Access requirements. A problem appears once, next to the work it
affects, and never blocks healthy apps.

Verification may read a real archive, but its queries, results, screenshots,
contents, counts and other derived facts stay outside this public repository.
Synthetic data does not prove product behaviour.
