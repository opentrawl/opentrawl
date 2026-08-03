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
- **User contract:** Run
  `OpenTrawl.app/Contents/Helpers/trawl` as the command-line tool. Open
  `OpenTrawl.app` as a normal Mac application. Users must never run
  `OpenTrawl.app/Contents/MacOS/OpenTrawlApp` as a command-line tool.
- **Trigger:** A Codex shell ran the then-named SwiftUI application executable
  twice as if it were a command-line tool. macOS aborted while the process
  tried to become a graphical application from that restricted shell.
- **Product decision:** The installed bundle contains two executables because
  the command-line tool and the Mac application have different jobs. Any
  normal CLI route that starts the SwiftUI executable is a product defect.
- **Repair:** The graphical executable is now named `OpenTrawlApp`. The direct
  command remains `Contents/Helpers/trawl`. Build, release and verification
  scripts use those distinct names.
- **Proof:** The signed proof build reported commit `55686d21`. Its embedded
  command completed help, status, source, media and location operations
  directly. The Mac app opened through LaunchServices, displayed the same
  external development archive and quit normally. No new crash report appeared.
- **Status:** Fixed and proved for the demonstrated crash class.
