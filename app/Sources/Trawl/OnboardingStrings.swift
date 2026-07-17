import Foundation
import PermissionGuide

/// All first-run copy lives here so product wording can change without touching view structure.
enum OnboardingStrings {
  static let welcomeStep = "01  TAKE BACK YOUR DATA"
  static let trustStep = "02  READ THE CODE"
  static let permissionStep = "03  FULL DISK ACCESS"
  static let syncStep = "04  BUILD YOUR ARCHIVE"
  static let readyStep = "05  USE IT"
  static let agentStep = "05  CONNECT YOUR AI"

  static let welcomeTitle = "Take back your data."
  static let welcomeBody =
    "OpenTrawl reads Messages, WhatsApp, Telegram, Notes and Contacts and builds a searchable archive on your Mac, ready for you and your AIs."
  static let archiveLocation =
    "Each app gets its own SQLite archive under ~/.opentrawl."
  static let archiveStaysLocal = "Your archive never leaves your Mac."
  static let originalsStayUntouched = "OpenTrawl never writes to your apps."
  static let openSource = "Open Source, MIT licensed. Read the code."
  static let start = "Build my archive"

  static let trustTitle = "Read the code first."
  static let trustBody =
    "Full Disk Access lets OpenTrawl read the local databases behind your apps. Because that permission is broad, you can ask your coding agent to check the code for this build before continuing."
  static let trustAction = "Copy prompt for your coding agent"
  static let trustContinue = "Continue"
  static let codeLink = "View this build's code on GitHub"

  static func auditPrompt(for identity: BuildIdentity) -> String {
    let source =
      identity.sourceURL?.absoluteString
      ?? "https://github.com/opentrawl/opentrawl"
    let buildDescription =
      if identity.hasLocalChanges {
        "This local build is based on Git commit \(identity.gitCommit) and includes uncommitted changes. The link shows the base code:"
      } else {
        "This is OpenTrawl \(identity.version), built from Git commit \(identity.gitCommit):"
      }
    let auditTarget =
      if identity.hasLocalChanges {
        "Check those claims against the production Mac app and the base commit above. The link does not show this build's local changes."
      } else {
        "Check those claims against the production Mac app at the exact commit above."
      }
    return """
      Help me check OpenTrawl before I give it Full Disk Access.

      \(buildDescription)
      \(source)

      OpenTrawl says that its production beta:
      - reads local data from Messages, WhatsApp, Telegram, Notes and Contacts;
      - writes separate search archives under ~/.opentrawl;
      - keeps those archives and my searches on my Mac;
      - has no telemetry or analytics;
      - does not run servers that receive my archive, searches or usage data;
      - keeps source syncing on my Mac by default. The app checks GitHub for updates. If I ask OpenTrawl to download missing Telegram media, it requests that media from Telegram.

      \(auditTarget) Explain in plain English what OpenTrawl reads, what leaves my Mac, which network requests happen automatically or when I ask for them, whether OpenTrawl receives any of my personal data, and whether the app has telemetry or analytics.

      When deciding whether the production beta matches these claims, ignore disabled or feature-flagged pre-release features, tests, debug tools, unfinished work, future code and standalone crawler commands the beta does not offer. If any of that is worth mentioning, put it in a separate section called "Not part of the production beta".

      If you can inspect the installed app, check that its GitCommit is \(identity.gitCommit)\(identity.hasLocalChanges ? " and note that this local build includes uncommitted changes" : ""). If you cannot check the installed app, continue with the source review and simply say that the installed build was not independently checked. Do not treat that alone as a privacy problem.

      Finish by telling me whether OpenTrawl's privacy claims are accurate and whether giving this build Full Disk Access is reasonable. Do not access my personal data or change anything.
      """
  }

  static let permissionTitle = "Add OpenTrawl to Full Disk Access"
  static let permissionBody =
    "Drag OpenTrawl into the Full Disk Access list, then turn it on. If this Mac has nothing OpenTrawl can check yet, use the button below."
  static let waitingForPermission = "Waiting for Full Disk Access…"
  static let permissionContinue = "I've turned it on"
  static let permissionDragAccessibilityLabel = "Drag OpenTrawl to Full Disk Access"

  static var permissionGuideCopy: PermissionGuideCopy {
    PermissionGuideCopy(
      title: permissionTitle,
      instructions: permissionBody,
      waiting: waitingForPermission,
      continueButton: permissionContinue,
      dragAccessibilityLabel: permissionDragAccessibilityLabel
    )
  }

  static let syncTitle = "Building your archive"
  static let syncBody = "Each app becomes searchable when it finishes."
  static let waiting = "Waiting"
  static let syncing = "Reading…"
  static let finished = "Finished"
  static let retry = "Try again"
  static let continueToSearch = "Continue"
  static let showInFinder = "Show in Finder"

  static let readyTitle = "Your archive is ready."
  static let readyBody = "Search it yourself, or connect your coding agent."
  static let search = "Search"
  static let connectAgent = "Connect coding agent"
  static let copyAgentInstruction = "Copy instruction"
  static let copied = "Copied"
  static let back = "Back"
  static let agentTitle = "Connect your coding agent"
  static let agentBody =
    "If you connect a model, you are trusting it with anything it asks OpenTrawl to read. Choose a model and permissions appropriate to your personal threat model."
  static func agentInstruction(helperCommand: String) -> String {
    "Use \(helperCommand) to search and open my local OpenTrawl archives. Run it with no arguments for a short introduction and with --help for the complete current interface. Prefer normal text output. Use --json only when writing a script."
  }
  static let agentDoesNotInstall =
    "This copies text only. OpenTrawl does not install a skill, change PATH or edit your agent configuration."

  // TODO: Add a separate prompt that asks the agent to persist this instruction
  // using the idiomatic mechanism for its harness.

  static let comingSoon = "Coming soon"
  static let notInstalled = "Not installed"
  static let syncNow = "Sync Now"
  static let cancelSync = "Stop"
  static let syncFailed = "Sync failed"

  static func counts(_ counts: [String]) -> String {
    counts.joined(separator: " · ")
  }
}
