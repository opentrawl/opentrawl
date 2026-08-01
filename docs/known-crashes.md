# Known app-controlled crashes

## Operating rule

When an app-controlled crash appears, stop repeating it and assign one bounded
repair. The repair must remove the demonstrated crash class, inspect the
adjacent exposure, prove the installed app through the normal human path,
record the outcome here, and stop. Add protection only for observed failures.

## PhotoKit callback inherited main-actor isolation

- **Reports:** `Trawl-2026-08-01-222427.ips` and
  `Trawl-2026-08-01-222623.ips`. Two later reports had the same crash class.
- **Signature:** `SIGTRAP` in `swift_task_checkIsolatedSwift` from PhotoKit's
  asset-resource file-I/O queue.
- **Trigger:** PhotoKit invoked the original-resource data or completion block
  on its own callback queue.
- **Root cause:** The callback closures inherited the request processor's main
  actor. Swift correctly trapped when PhotoKit invoked them elsewhere.
- **Repair:** PhotoKit request objects and request creation stay on the main
  actor. Nonisolated factories create callbacks that capture only locked,
  Sendable completion, writer, and cancellation state. One-shot completion
  makes cancellation, timeout, duplicate completion, and late callbacks safe.
  Full Swift 6 concurrency checking remains enabled.
- **Proof:** The full Swift app build passes. The unchanged signed installed
  app completed the original-resource and current-rendered callback paths once
  for a local photo and released the lease. For an iCloud-backed edited photo,
  network-disabled access returned typed `MEDIA_NOT_LOCAL`; the same photo then
  succeeded with network access and released the lease. The app stayed alive,
  left no IPC or cache residue, and produced no new crash report. Cancellation,
  timeout, and generic provider failure were not directly exercised.
- **Status:** Fixed and proved for the demonstrated crash class.

The two earlier reports from the same date were AppKit registration aborts
caused by directly executing the app binary. They are not this crash class;
the app must be launched through LaunchServices.
