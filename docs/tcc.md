---
written_by: ai
---

# TCC strategy

Decision record for how OpenTrawl handles macOS privacy permissions
(TCC). Decided early because attribution rules shape the process
architecture.

The binding constraint, from the vision's engineering principles:
agents run unimpeded. TCC must never recurringly block the dev loop —
after the one-time terminal grant, every agent builds, runs, crawls
and verifies with zero humans in the loop.

## Facts that decide it

- One permission covers almost everything in v1. iMessage, Apple
  Notes, Photos (direct sqlite), WhatsApp and Telegram desktop stores
  are all readable with Full Disk Access alone.
- Apple Calendar needs no outlier treatment after all. Earlier
  research claimed modern macOS has no readable on-disk calendar
  store; verified false on a live machine (2026-07-02): Calendar.app
  keeps `Calendar.sqlitedb` in its group container, readable under
  Full Disk Access with a clean relational schema, and it contains
  every synced calendar — iCloud, Google and local. calcrawl reads it
  with the same snapshot pattern as iMessage and WhatsApp. CalDAV and
  EventKit stay unnecessary for v1.
- FDA and the App Sandbox do not compose. The app ships unsandboxed,
  Developer ID signed, notarized, outside the Mac App Store.
- TCC attributes terminal-spawned binaries to the terminal app, not
  the binary. Granting FDA to a Go binary does nothing; the terminal
  (or the app that spawned it) holds the grant.
- Signature-keyed grants die when the signature changes, and every Go
  rebuild re-signs ad hoc. Path-keyed grants and responsible-process
  inheritance survive rebuilds. Ad-hoc-signed app builds lose their
  grant on every rebuild — the app must always be signed with a stable
  identity, including dev builds.
- LaunchAgents are the least reliable TCC holders (attribution-chain
  failures). Scheduling stays inside the app's process tree: login
  item plus timer, never a LaunchAgent.
- There is no API to check FDA. The probe is a canary read of a
  protected path.

## The decision

Trawl.app holds Full Disk Access; crawlers run as its direct children
and inherit the grant. Developers and agents grant FDA to their
terminal once — the standard pattern for every tool in this space —
and everything then works from a shell too.

What this requires:

1. Stable signing from day one: a persistent self-signed certificate
   for dev builds, Developer ID plus notarization for releases. Never
   ad hoc.
2. Syncs are scheduled by the app from its own process tree (login
   item + timer), so crawler children always have an FDA ancestor.
3. `doctor` in every crawler and in `trawl` detects the permission
   failure by canary read and prints the exact remedy: grant FDA to
   Trawl (app users) or to your terminal (CLI users), with the System
   Settings deep link (`Privacy_AllFiles` pane).
4. App-side grant UX uses permiso's guided drag-into-Settings flow;
   it needs a one-line addition for the FDA panel.

## Idempotent permissions: the one-time setup

The goal is zero permission overhead after day one: grants and signing
are done once, done right, and never thought about again. Rebuilds,
new crawlers and new machines must not re-trigger permission work.
Formalised as phase 1 tasks:

1. Create the persistent dev signing certificate and check the signing
   step into the app packaging script. Every dev build signs with it;
   release builds sign with Developer ID and notarize. No build path
   exists that produces an ad-hoc-signed app.
2. Grant FDA twice per machine, ever: once to the dev terminal, once
   to the (stably signed) app. Document both grants with their
   Settings deep links in the repo, verified by `trawl doctor`.
3. Adding a crawler must require no new grants: it inherits from the
   app or the terminal by design. Any proposal that adds a per-crawler
   grant fails review.
4. CI for the app verifies the signature identity is stable across
   builds, so a packaging change cannot silently reintroduce ad-hoc
   signing.

## The inheritance spike: resolved

Run 2026-07-02 with a minimal app signed by the dev identity and
granted Full Disk Access: the app read the protected store directly,
and a spawned Go crawler binary (separately ad-hoc-signed, resolved
from a plain directory on disk) inherited the grant — its doctor
reported the source store readable. Verdict: children inherit; the app
runs crawlers from PATH, no binary bundling needed.

## Rejected

- Path-based FDA grants per crawler binary: miserable consumer UX,
  breaks on package-manager upgrades, N binaries means N grants.
  Escape hatch only.
- FDA-holding helper daemon with the CLI over RPC: no shipping product
  in this space does it, launchd attribution is the least reliable,
  and it contradicts crawlers owning their own reads.
