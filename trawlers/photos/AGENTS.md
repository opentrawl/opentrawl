---
written_by: ai
---

# AGENTS.md

This file specialises the root `AGENTS.md` for the Photos trawler. The root
file's mission, quality bar, public-data boundary, naming and typing rules all
apply here; this file adds what is particular to Photos and why.

## What Photos is for

A person's photo library is the most detailed record of their life, and
nobody can read it. Photos makes it reachable by the assistants that work for
them: searchable by what a photo shows and says, queryable by when and where,
and openable down to the picture itself.

Photos copies what Apple Photos already knows about each photo, reads the
picture file's own metadata, looks up the named places around where the camera
stood, and makes one model call per photo to write a card: a one-line summary,
a long description, all the text visible in the picture, and which place the
photo shows. Everything except the card is mechanical. Trips, habits, homes
and how much of a life a place took are worked out by the assistant reading
these records, not by Photos.

The card is the one place OpenTrawl interprets data with a model. It is
derived data: it records exactly what it was made from, it can be remade when
models improve, and it never replaces the facts it was made from.

## Read raw inputs and outputs, always

Success depends on knowing exactly what goes into and comes out of every step.
Every step, model call, provider call and CLI command must have its raw input
and raw output read by the agent changing it: untruncated, unfiltered, not
summarised by another model and not piped through `grep` or `jq` first. Read a
representative sample that includes the awkward cases: photos without GPS,
China, rural places, the iCloud Shared Library, screenshots and documents,
edited photos, videos, and the oldest photos.

Why: earlier Photos builds failed because agents judged their work by exit
codes, counts and summaries. Garbage went into the model and garbage came out,
and nobody looked. Reading the raw data catches almost every failure before it
is built in.

## How Photos is built

- **Steps are typed functions.** Each step takes one protobuf message and
  returns one protobuf message, with no archive access. A thin wrapper selects
  the step's pending work from the archive and stores its output. Every step
  can run on its own from the CLI: `trawl photos run STEP --photo PHOTO`
  prints its exact input and output. Why: a step that can be run and read on
  one photo can be proved, debugged and changed without the rest.
- **The tables are the state.** A step's work is "keys whose inputs exist and
  whose output row is missing". There is no status column, queue table or
  state machine. An output row records success or failure and when a failed
  item may be retried. Why: a separate state record drifts from the data.
- **Three families of tables, by origin.** What Apple's library holds; what
  the picture file holds; what was derived (place lookups and the card). Why: anyone can look at the database and tell what came
  from Apple, what came from the file and what a model wrote.
- **Facts stay facts.** Apple's values keep Apple's identifiers and Apple's
  own codes. A missing value stays missing, never zero. Computed values (the
  spot a photo belongs to, field of view, distance and direction to a place)
  are computed when needed, not stored as if Apple had said them.
- **Code gathers; the model judges.** Code reads, fetches, looks up, sorts by
  distance, computes geometry and renders the card model input from a template
  file. It never ranks or scores candidate places, and never decides what a
  photo shows. The place lookup request leaves out bare house numbers and
  minor roads for cars, and that choice is part of the exact request text;
  after the answer, code only drops repeats of one place with the same name
  and kind. Why: every rule that guessed meaning in earlier builds
  was wrong somewhere the model would have been right.
- **One model call per photo.** The model reads the picture and a plain-text
  card model input rendered through a template file, never JSON and never
  string assembly. It answers in four labelled plain-text sections (summary,
  description, visible text, place shown) that map onto `PhotoCard`, and an
  answer that breaks the format is asked once more, then stored as invalid.
  Why plain text: it is what language models write best, and it is the one
  format that works the same through Ollama Cloud and the Codex command line,
  which has no custom tool calls. The provider and model are configuration
  values.
- **Cards are remade only on request.** When the card model input rendered
  from today's facts differs from the one a card was written from, `open`
  and `inspect` say so and the card stays usable. Nothing reruns until
  someone asks with `--recard`, which also remakes cards written by another
  model. A photo whose pixels were edited gets a new card on its next card
  run. Why: cards are the expensive part.
- **One search row per photo.** Apple's facts and the written card share one
  full-text row, rebuilt whenever either is written, so one query can match a
  person Apple named and a dish the card describes. Each Chinese, Japanese and
  Korean character is its own token, so one character finds every photo that
  holds it and several characters match only as a phrase. Why: Josh searches
  photos as whole records, and "茶" alone must work.
- **Small first, then compose.** No step runs on more than a handful of photos
  until a run on one photo, then ten, has been read in full, including the
  rows it stored. Then 100 across the awkward cases. Only then a full run.
  Why: earlier builds ran whole pipelines repeatedly without checking that one
  photo came out right and was stored right.
- **Every delivered stage gets an independent data review.** A stage is not
  done until a fresh agent, which did not build it, has run it on the real
  library, read its raw inputs, outputs and stored rows across the awkward
  cases, and judged them against Josh's own steering, the vision and these
  rules. The reviewer forms its view from the data, not from the
  implementer's report, and does not add requirements Josh excluded. Why: an
  implementer reviewing its own work, or a reviewer grading someone else's
  summary, is how earlier builds drifted into slop unnoticed.
- **Tests follow proof, on real photos.** Once a step is proved on real photos,
  it may get one focused test that runs the step on known real photos from
  the private evaluation set. The photos and expected results stay in the
  private repository; the test reads them from a path in an environment
  variable and skips when it is absent. No synthetic photos, fixtures or mocks.

## Boundaries

- Read Apple Photos only. Never write to the library, albums, faces, metadata
  or iCloud state.
- One app, one permission. Pictures come through the installed OpenTrawl app,
  never through a helper app with its own identity.
- Disk is bounded. Pictures live in a ring-buffer cache with a byte cap; the
  oldest are deleted when it is full and fetched again if needed.
- Place lookups are cached per exact request and never sent twice. Photon,
  over OpenStreetMap, is the source: a self-hosted Photon when the
  configuration names one, otherwise photon.komoot.io at one request a second.
  One spot is a cell of about 55 metres.
- Models are hosted, never local. Full card runs need Josh's approval with the
  measured time, tokens and failure rate from a sample.
- Known places come from a file the user or their assistant keeps: home, work
  or other, each with a free-text name. Add no further categories.

## Output

Photos designs no output of its own. It returns typed records; the shared
output library renders them like every other trawler's. See the root
`AGENTS.md`.
