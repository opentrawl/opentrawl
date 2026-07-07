---
name: opentrawl-demo
description: Produce X-ready demo videos of OpenTrawl — record a real agent-driven session, sanitize it with Josh in the loop, re-enact it in the branded Remotion player, render 16:9 + vertical. Use when asked to make a demo, demo video, launch clip, or feature video.
---

# OpenTrawl demo videos

Paste-ready session prompts live in [prompts.md](prompts.md).

## What these videos are for

OpenTrawl is your whole digital life — photos, WhatsApp, Telegram,
iMessage, calendar, X, Claude history — crawled locally and made
queryable by your agent. Nothing leaves your machine. The videos
exist to make one viewer reaction happen on the X timeline:
**"wait, my agent could do THAT?"** — followed by clicking through
to opentrawl.sh.

Every video makes exactly ONE claim and proves it on screen with
real product output. The series covers the product; a single video
never tries to. If you can't say the video's claim in one sentence
("your agent can find a photo from a half-remembered dinner"),
the video isn't scoped yet.

The wow hierarchy — what actually lands, strongest first:

1. **Cross-source joins.** One question answered from three sources
   at once (the photo + the message thread + the calendar event).
   No other product does this. Lead with these.
2. **Fuzzy temporal recall.** "that dinner with Anna sometime last
   spring" resolving to an exact evening. Human memory is the
   competitor, and it loses.
3. **Agent autonomy.** The agent chaining queries unprompted —
   noticing it needs the calendar to disambiguate the photos, and
   just doing it. Don't script this; when it happens in a real
   recording, that's the take.
4. **Local/private.** Never the headline (privacy doesn't hook),
   always the kicker — one caption beat near the end:
   "all local. nothing leaves your machine."

Anti-goals: feature lists, UI tours, install instructions,
benchmark tables, anything a viewer must read to believe. If the
wow needs explaining, pick a different query.

## Narrative shapes (pick one per video)

- **Impossible question.** Open cold on a question no app can
  answer, typed into the agent. Fan-out. Answer. Receipts. The
  default shape; when in doubt use this.
- **Receipts.** The answer lands FIRST (seconds 0–3), then the
  video rewinds to show where it came from — punch-ins on the
  photo, the message, the event. Good for queries whose answer is
  visually rich.
- **Speedrun.** 4–6 killer queries back-to-back, ~2s each,
  escalating in difficulty, no answers shown until the last one.
  Montage energy; use sparingly or it reads as slop.
- **Over-the-shoulder.** The agent is mid-task for the human
  (planning a trip, writing a bio, finding a document) and reaches
  for opentrawl as a tool — the product as ambient infrastructure.
  The "agents are here" angle.

Hook formulas for second one (huge burned-in text, pick one):
the question itself ("where did anna say that ramen place was?") /
a claim ("my agent knows my life better than i do") / a count
("6 years. 11 sources. 4 seconds."). Lowercase, no punctuation
theatrics, no emoji in hooks.

## House style

- **The terminal is the set.** Dark terminal aesthetic, real
  monospace output, the branded player frames it — we never cut to
  slides, mockups, or stock anything. If it isn't product output
  or a caption, it doesn't go on screen.
- **Show, never claim.** No marketing copy, no adjectives in
  captions. Captions narrate what is literally happening ("searching
  6 years of messages…") or land the kicker. The output is the ad.
- **Confident, playful, a bit unserious.** Benny-Hill-adjacent
  music under a genuinely hard technical demo is the brand: the
  product flexes, the tone winks. Never smug, never "revolutionary",
  never exclamation points.
- **Fast but legible.** Hard cuts every 2–4s, speed-ramp the
  tool-call sequences, but every output punch-in must hold long
  enough to actually read (≥1.2s for a line). Brainrot pacing,
  librarian legibility.
- **Real texture stays.** Timing jitter in typing, real row counts,
  real (sanitized) names with real-name energy — "Anna", not
  "Alice_Demo". The moment it smells staged, the wow dies.
- Watermarks: `@opentrawl` + `opentrawl.sh` corners, wordmark
  end-card with the one-line claim restated. Aesthetic decisions
  (palette, wordmark, caption font, pacing feel) get interviewed
  ONCE, then frozen in `demo/README.md` — video #2 never re-asks.

## The technique (always this shape)

1. **RECORD REAL.** Run the actual flow: the agent drives `trawl` /
   the crawlers via CLI against the real local archives — READ-ONLY
   (`~/.opentrawl/`, legacy `~/Library/Application Support/` dirs);
   search/status/open yes, anything that writes no. Capture the
   complete transcript: prompt, every tool call verbatim, real
   output, final answer. Record more takes than you need; pick the
   take where the agent surprised you.
2. **SANITIZE, Josh-gated.** Rewrite names, places, employers,
   faces-adjacent details, anything identifying — non-deterministic
   replacements with identical lengths, casing, formatting, counts,
   and texture. Then STOP: show Josh the before/after diff and each
   choice. He approves before any render. Raw recordings live only
   in git-ignored `demo/raw/`, never committed; approved transcripts
   go in `demo/transcripts/`.
3. **RE-ENACT.** Feed the approved transcript to the Remotion agent
   player (`demo/`): chat bubble types the prompt, tool-call rows
   animate, real output renders in the terminal pane, answer types
   out. `dur_ms` in the transcript is display time — compress
   ruthlessly; the product's real latency is not the video's.
4. **RENDER BOTH.** 16:9 master (~30–45s) and <15s vertical cutdown
   from the same composition. Then extract frames
   (`ffmpeg -i out.mp4 -vf fps=2 frames/%03d.png`) and LOOK at them.
   Never deliver a video whose frames you haven't inspected.
5. **GATE.** Show Josh both cuts. Done = Josh has the files and
   approved them. You never post.

## Transcript schema

`demo/transcripts/<slug>.json` — ordered events:

```json
{ "title": "…", "claim": "one-sentence claim this video proves",
  "hook": "the huge text for second one",
  "shape": "impossible-question | receipts | speedrun | over-shoulder",
  "events": [
    {"t": "prompt",    "text": "…"},
    {"t": "tool_call", "cmd": "trawl search …", "dur_ms": 900},
    {"t": "output",    "text": "…verbatim sanitized output…"},
    {"t": "answer",    "text": "…agent's final answer…"}
  ] }
```

## Pipeline (`demo/`)

First use: scaffold may not exist. Build it once (Remotion project,
player components, transcript schema, `make demo
TRANSCRIPT=transcripts/<slug>.json` → `out/<slug>-16x9.mp4` +
`out/<slug>-vert.mp4`, git-ignored `raw/` and `out/`), interview
Josh on the frozen aesthetics, commit with his approval. Toolchain
is nix-pinned via `demo/devenv.nix` following the house pattern
(see photoscrawl/devenv.nix); no global installs. ffmpeg for mux/
cutdown/audio; music bed is a file Josh supplies — render must work
with and without it. VHS exists for pure-terminal shots; the player
is the default (cutdowns need layout control).

## Checklist before handing Josh a video

- [ ] One-sentence claim written down; every scene serves it
- [ ] Transcript from a real run; nothing hand-invented
- [ ] Sanitization diff approved by Josh BEFORE render
- [ ] No real names/places/private data in any committed file
- [ ] Raw recording in git-ignored `demo/raw/` only
- [ ] Both formats rendered; frames visually inspected for
      legibility at feed size (squint test on a phone-width frame)
- [ ] Hook lands in second one; captions complete; kicker present
- [ ] Watermarks + end-card; `(TRAWL-nnn)` ref if ticketed
- [ ] Josh has seen and approved the final cuts
