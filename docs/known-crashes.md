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
- **User contract:** The command-line tool runs directly as
  `OpenTrawl.app/Contents/Helpers/trawl`. It does not need LaunchServices. The
  Mac application is a different executable and opens as an application.
- **Observed evidence:** The 3 August report names the former SwiftUI
  executable `Contents/MacOS/Trawl` and UUID
  `075AD14D-7DF6-364A-AA8E-85FC0988FEDD`. That UUID matches obsolete local
  proof builds. It does not match the current installed command-line tool or
  Mac application.
- **Model inference:** An agent or proof command probably started an obsolete
  Mac application executable as a command-line tool. The exact launch command
  was not recovered, so this is not an observed fact.
- **Josh's product decision:** The installed bundle contains two executables because
  the command-line tool and the Mac application have different jobs. Any
  normal CLI route that starts the SwiftUI executable is a product defect.
- **Repair:** The graphical executable is now named `OpenTrawlApp`. The direct
  command remains `Contents/Helpers/trawl`. Build, release and verification
  scripts use those distinct names.
- **Proof:** The signed proof build reported commit `55686d21`. Its embedded
  command completed help, status, source, media and location operations
  directly. The Mac app opened through LaunchServices, displayed the same
  external development archive and quit normally. The later report from 3
  August still names the obsolete `Contents/MacOS/Trawl` executable and has
  UUID `075AD14D-7DF6-364A-AA8E-85FC0988FEDD`. The current installed GUI is
  `Contents/MacOS/OpenTrawlApp` with a different UUID, and the current installed
  CLI is `Contents/Helpers/trawl` with another different UUID.
- **Status:** The normal routes are corrected. The installed CLI is the direct
  executable `Contents/Helpers/trawl`; it does not start SwiftUI or AppKit. The
  GUI executable has a distinct name, so a person or agent cannot mistake it
  for the command-line tool. Final proof must still detect any new crash.

Milestone acceptance must start by running the installed CLI directly, open the
GUI only as an app bundle, and compare DiagnosticReports before and after. Any
new `Trawl` or `OpenTrawlApp` report fails the milestone. The absence of a
report on a later day is supporting evidence, not proof by itself.
