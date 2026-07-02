---
written_by: ai
---

# Observability

Goal, in the owner's words: someone who has the code and a running
service — without being skilled in the code or in steering an agent —
can figure out what the hell is going on from the logs, and can run
one command that dispatches an error report with enough context that a
maintainer is not flying blind. Agents own the entire dev lifecycle
and operating loop without human intervention; the logs must be good
enough to support that. The software ideal is crash-only: it works or
it fails clearly. Nothing here to diagnose by tweaking.

Prior art for the report bundle: how Codex and Claude package session
context for debugging — inspiration, not a copy, and not distributed
tracing; this is one machine.

## What every crawler gets from crawlkit

One plain text log per crawler, human-readable line by line:

    2026-07-02 22:41:03 ERROR sync gog_backup_failed: backup fetch
    exited early. remedy: run gogcrawl doctor (run 019f23a1)

Timestamp, level, command, event, message, remedy when there is one,
run id last. Plain lines because a person under stress reads them
raw with tail and grep; no JSON wrapper to see through. The stable
line shape is still machine-parseable for trawl and agents.

Every command logs — not just sync. Start, outcome, and every error
with its remedy, across the whole operating loop: discovery, auth,
sync, reads, doctor. A failure that never reached a log line is a bug.

Bounded: fixed-size rotation per crawler, a few MB, oldest lines
dropped. No settings for path, size, level or retention. Levels are
info, warn, error; debug is a runtime switch (the app's debug mode, a
dev shell), never persisted config.

Sync must feel alive: first progress line immediately, then steady
heartbeats; a long opaque stage (a backup fetch) heartbeats with
elapsed time. Progress goes to stderr live and to the log.

## The operator surface

No new commands. `trawl status <source>` and `trawl doctor` read the
recent log tail: last run, outcome, most recent error with remedy.
Operating the suite starts and usually ends there.

The dispatchable error report is prepared for, not built: because log
lines are content-safe by construction (never bodies, subjects,
contacts or secrets) and status and doctor already expose versions
and outcomes, a future report bundle is an afternoon of assembly the
day real pain justifies it — not a design problem, and not a verb
today.

## Out of scope, deliberately

Metrics, SLOs, dashboards, remote telemetry, tracing infrastructure.
One machine, local first. The horse is: every failure lands in a
readable log with a remedy, and one command packages the evidence.

## Conformance

- every command logs start and outcome; every error logs with remedy
- sync emits progress immediately and heartbeats through long stages
- log lines match the stable shape and never contain content or secrets
- rotation holds the size bound
- status and doctor surface the most recent error and remedy
