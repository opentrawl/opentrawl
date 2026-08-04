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

## AppKit registration abort outside LaunchServices

- **Reports:** `Trawl-2026-08-03-131655.000.ips` and
  `Trawl-2026-08-03-131655.ips`. These reports name the former GUI executable,
  `Contents/MacOS/Trawl`.
- **Signature:** `SIGABRT` while SwiftUI initialises `NSApplication`, through
  `_RegisterApplication` in HIServices.
- **Josh's product decision:** OpenTrawl has one installed application identity
  and a normal command-line tool. The command-line tool must run directly.
- **Observed trigger:** The then-named SwiftUI application executable was run
  directly from a Codex process. It aborted while registering with AppKit.
  Earlier task notes say that it was run with `--version`, but the original
  launch record is no longer available. Treat that argument as an inference,
  not an observed fact.
- **Implementation decision:** The command-line tool runs directly as
  `OpenTrawl.app/Contents/Helpers/trawl`. It does not use LaunchServices. The
  graphical executable has a different name and opens as an application.
- **Repair:** The graphical executable is now named `OpenTrawlApp`. The direct
  command remains `Contents/Helpers/trawl`. Build, release and verification
  scripts use those distinct names.
- **Current evidence:** The installed CLI at `Contents/Helpers/trawl` starts
  directly and completes its version, help and introductory commands. It does
  not link AppKit or HIServices. The current source and packaging scripts do not
  execute the GUI binary as a command. The installed build is older than the
  current Photos candidate, and its normal Photos archive is not yet available
  from the CLI.
- **Status:** The implementation removes the demonstrated executable-name
  confusion. The crash class is not accepted as fixed for Milestone 2 until the
  exact signed candidate completes the real archive path through the direct CLI
  and produces no new crash report.

Milestone acceptance must start by running the installed CLI directly, open the
GUI only as an app bundle, and compare DiagnosticReports before and after. Any
new `Trawl` or `OpenTrawlApp` report fails the milestone. The absence of a
report on a later day is supporting evidence, not proof by itself.
