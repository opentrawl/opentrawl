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
  whose output row is missing". The card-input step revisits every ready
  photo and stores only changed input. There is no status column, queue table or
  state machine. An output row records success or failure and when a failed
  item may be retried. Why: a separate state record drifts from the data.
- **Three families of tables, by origin.** What Apple's library holds; what
  the picture file holds; what was derived (place lookups, the card model
  input, the card). Why: anyone can look at the database and tell what came
  from Apple, what came from the file and what a model wrote.
- **Facts stay facts.** Apple's values keep Apple's identifiers and Apple's
  own codes. A missing value stays missing, never zero. Computed values (the
  spot a photo belongs to, field of view, distance and direction to a place)
  are computed when needed, not stored as if Apple had said them.
- **Code gathers; the model judges.** Code reads, fetches, looks up, sorts by
  distance, computes geometry and renders the card model input from a template
  file. It never filters, ranks or scores candidate places, and never decides
  what a photo shows. Why: every rule that guessed meaning in earlier builds
  was wrong somewhere the model would have been right.
- **One model call per photo.** The model reads the picture and a plain-text
  card model input rendered from protobuf through a template file, never JSON
  and never string assembly, and answers through one typed tool call. The
  model is one configuration value.
- **Cards are remade only on request.** When an input changes, the card is
  marked stale with a plain reason ("new nearby places since this card was
  written") and stays usable. Nothing reruns until someone asks with
  `--recard`. Why: cards are the expensive part.
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
- Map lookups are cached per exact request and never sent twice. Overpass and
  Apple map search are the sources; respect their pacing and terms.
- Models are hosted, never local. Full card runs need Josh's approval with the
  measured time, tokens and failure rate from a sample.
- Known places come from a file the user or their assistant keeps: home, work
  or other, each with a free-text name. Add no further categories.

## Output

Photos designs no output of its own. It returns typed records; the shared
output library renders them like every other trawler's. See the root
`AGENTS.md`.
