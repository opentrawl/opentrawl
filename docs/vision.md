---
written_by: ai
---

# Vision

The project is called OpenTrawl: a trawl net drags the deep and brings
everything up, and the open prefix names the open-source, plugin-first
ambition (and the OpenClaw lineage). GitHub org `opentrawl`, one
monorepo, one CLI (`trawl`), one Mac app shipped as Trawl.

## North star

An entire history of you, agent searchable. An agent onboards into a
person's life with minimal steering: it can work out who the people in
your life are, how you talk to them and where, what you did, and what is
going on right now, by querying local archives of the services you
actually use.

Long-horizon tests, all of the same shape — inferences an agent should
make from the archives without being told:

- who someone's girlfriend is, and how they message her across
  Telegram, WhatsApp and Gmail, what events they share in Calendar,
  what notes they share, how many group chats they have in Signal
- who the family, close friends and colleagues are, and which channel
  each relationship lives on
- what the person does for work, what projects are active, and what
  changed this month
- where they lived and travelled, what they bought, what appointments
  recur, which social groups meet and how often

None of these are features to build one by one. They fall out of clean,
complete, queryable archives plus a capable agent. Get the data out;
the inferences follow.

## What the suite is

A local-first crawler suite for macOS: one app and one CLI that let you
authorise your services and crawl them into local, per-source SQLite
archives that agents can query through a single entry point.

The layers, bottom to top:

1. Source apps: Messages, Telegram, WhatsApp, Gmail, Calendar, Notes,
   Signal, and later Photos, X and others.
2. Source crawlers: one Go binary per source. Each owns extraction, its
   own archive database, auth and session handling, search, status and
   privacy policy. Each conforms to the shared control contract.
3. Control contract: a crawlkit-defined JSON contract every crawler
   speaks: `metadata`, `status`, `sync`, `search`, `open`, `doctor` and
   `contacts export`, all with `--json`, all bounded, all human readable.
4. Federation surface: one CLI (`trawl`) that discovers installed
   crawlers through the contract and gives agents and humans a single
   surface: status across everything, sync anything, search across
   sources. One Mac app that shows the key per-crawler metrics and
   handles authorisation. No knobs.
5. Derived layers (later): daily deltas, cross-source identity joins,
   clustering and per-thing cards, life orientation. These build on the
   substrate; they never reach around it into source databases. The
   clustering and card mechanics OpenClaw already runs on maintainer
   data (clawsweeper, gitcrawl, discrawl) are the working prior art
   here; see below.

## Design principles

- Agent first, human readable. Every output must make sense to a human
  reading it cold; if a human can read it properly, an agent can too.
  This goes down to the field level: every field, name and value must
  semantically make sense — real timestamps, human names, properly
  named keys. No machine row IDs, no raw epoch timestamps, no unbounded
  dumps.
- Local first, privacy by design. Archives live on your machine.
  Nothing leaves it unless you explicitly opt in. Sending content to
  remote frontier models gives the best inference results today and is
  supported, but it is never the default; the default is local and
  private. Model access goes through one pluggable provider seam — not
  a new integration surface per model.
- Federated, not unified. Each source keeps its own source-native
  database. The single entry point is a query surface, not a shared
  schema. Cross-source joins happen in derived layers, later, on top of
  proven per-source archives.
- Contract first. The plugin story is the control contract. Anyone who
  wants to add their own messaging app implements the contract in any
  language and their crawler appears in the CLI and the app.
- No knobs. One and only one obvious way of doing things. Defaults over
  configuration. A settings surface is a design failure until proven
  otherwise.
- Secrets never in output. Auth state is reported as booleans and expiry
  dates. No command prints tokens, cookies or key material.
- Bounded output everywhere. Every command paginates or truncates with an
  explicit indicator. Token budgets are a design axis, not an
  afterthought.
- Read only in v1. Write capability (sending messages) comes later
  through the existing upstream access CLIs, behind explicit
  authorisation.

## Engineering principles

The bar for every line of code and every surface in this repo:

- As simple as possible, but no simpler. KISS, YAGNI, less is more.
  Prefer deleting a concept to documenting it.
- One and only one obvious way to do each thing, with as few knobs as
  possible. No modes, no fallbacks, no compatibility shims without
  current evidence.
- Human readable with no magic. Code must be self-explanatory: no magic
  constants, one obvious job per function, boundaries in the Ousterhout
  sense (deep modules, simple interfaces). If a reader needs a comment
  to follow the code, rewrite the code.
- Files under about 400 lines. Split when they grow.
- The tree tells the story. Running `tree` should reveal what the repo
  is and how it is organised — for humans and for agents. Structure is
  documentation.
- CLIs follow [clig.dev](https://clig.dev). Curated, minimal,
  consistent surfaces; taste is a requirement, not a polish step.
- Go for crawlers and the CLI. Swift and SwiftUI for the Mac app.
- Trunk-based development. Small logical commits on main. Speed over
  ceremony.
- Test against real inputs and outputs. Agents and tests must exercise
  raw, unmodified, untruncated real data flows — never faked,
  abbreviated or hand-massaged fixtures for the paths that matter. This
  is the only way to prove the thing actually works and to catch quiet
  degradation.
- Agents run unimpeded. The full dev loop — build, run, test, crawl,
  verify — works end to end with zero humans in the loop. One-time
  human setup (a permission grant, a signing certificate) is
  acceptable exactly once per machine; anything that recurringly needs
  a human is a design failure. If an agent hits a wall a human must
  clear, the wall is the bug.
- Declarative, minimal install surface. The dev loop needs no install
  step at all: one devenv at the monorepo root, crawlers run from
  source, and a repo-local bin directory on the shell's PATH is where
  `trawl` discovers dev binaries — clone, `devenv shell`, everything
  works. For consumers the Mac app is the main method and the only
  global install; a Homebrew tap and a Nix flake cover CLI users. No
  ad-hoc global installs, and no `trawl install` package manager. State
  and config live under one root with per-crawler subdirectories, not
  scattered dotfiles, via a crawlkit config option (no fork needed).
- Observability for free. Structured logs, run history and doctor
  diagnostics come from crawlkit once, in one consistent, greppable,
  agent-first shape — a crawler gets debuggability by using the
  substrate, not by designing its own logging. Nothing leaves the
  machine.

## Now, next, later

- v1: iMessage, Telegram, WhatsApp, Gmail, Calendar and Contacts at the
  golden-path bar, behind the federation CLI, with the new Mac app.
- v1.5: Apple Notes, once the version-chain extractor matures.
- v2: Signal (research spike first), Photos, X, daily deltas ("what
  changed in the last 24 hours"), write capability, and a published
  plugin API with agent surfaces (MCP or an Executor source) as thin
  adapters over the contract.
- Horizon: cross-source inference: identity resolution, relationship
  inference, life orientation reports. This is where clustering and
  per-thing cards come in — the clawsweeper/gitcrawl pattern applied to
  people, threads and events instead of issues and PRs. The suite's job
  is to make this possible by shipping clean archives and contact
  exports; the inference layer stays out of the crawlers.

## Prior art and how we relate to it

- crawlkit (openclaw): the substrate. Shared SQLite, snapshot, backup,
  sync-state, vector and control mechanics. We build on it and push
  contract work back upstream; we do not fork it.
- crawlbar (openclaw): proved the control-plane idea and wrote down the
  control protocol and a quality rubric worth keeping. Its
  settings-driven implementation is what the new Mac app replaces.
- imsg, wacli, gogcli, remindctl (openclaw): per-service access CLIs with
  read and write verbs. Too specific to be the entry point, exactly right
  as adapters under crawlers and as the later write path.
- Executor (executor.sh, MIT): an MCP gateway that normalises every tool
  to name plus input and output schemas, with host-side credential
  resolution and aggressive token economy. Validates our contract-first
  design and our secrets rule. Difference: Executor is a gateway service;
  we are local-first CLIs, so it is a v2 integration target, not a
  dependency.

### Clustering and cards: the clawsweeper pattern

OpenClaw already runs, in production on maintainer data, the exact
inference pattern our horizon needs. It splits across three repos, and
each half is reusable:

- gitcrawl is the clustering engine. Every issue and PR gets an
  embedding; edges combine cosine similarity with deterministic
  reference links (explicit cross-references), because links catch what
  embeddings miss; bounded union-find turns edges into clusters. The
  governance layer is the real prize: clusters have stable
  content-hashed identities, memberships move through a state machine
  (active, excluded, removed), and human curation verbs (exclude,
  include, set-canonical, merge, split, close) are stored as overrides
  that survive every recomputation. Re-running the maths never destroys
  human judgement.
- clawsweeper is the per-thing card and judge. Each reviewed item gets
  one durable card: a markdown record plus a typed JSON verdict
  (decision, confidence, evidence with file and line references, risks,
  suggested action) and one edit-in-place comment. An LLM judges over
  hydrated context but is proposal-only; a deterministic apply step
  re-checks live state before any mutation. It does not cluster itself
  — it reads gitcrawl's SQLite snapshot by convention, proving the
  crawl layer and the reasoning layer can stay decoupled through a
  portable store.
- discrawl supplies the archive-side primitives: per-message
  embeddings, semantic and hybrid search, statistical roll-up reports,
  and structured entity extraction over people (member profiles).

Our horizon layer is this pattern generalised: gitcrawl's clustering
and durable governance applied to people, threads and events instead
of issues and PRs; clawsweeper-style typed cards and a proposal-only
judge on top; per-source crawler archives underneath, exchanged
through the same portable-SQLite seam. This one-shot orientation over
a corpus — cluster it, card it, review it — is the same move as
onboarding an agent into a life.

COMMENT: yes, and important to state that what makes sense for clawsweeper (small units, one PR at a time) might not make sense for other surfaces, we have to eval it and test. bear in mind we have an ollama sub with some good cheap not-quite-frontier-but-still-quite-good models and some examples of this work in photoscralw and elsewhere; credentials in nix-secrets

## Non-goals for v1

- no writes to any source
- no cloud sync or hosted service
- no cross-source joins or identity resolution inside crawlers
- no MCP server or gateway (the contract must make these trivial later)
- no prompt-tuned "intelligence" layered on thin archives; get the data
  out first
