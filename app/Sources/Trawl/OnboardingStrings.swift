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
    "Full Disk Access is broad. OpenTrawl needs it to read the databases behind your apps. This is the best option MacOS gives us."
  // TODO: Phrase this around the user's own AI, rather than any AI.
  static let trustAction = "Copy audit prompt for your AI"
  static let trustContinue = "Continue"
  static let codeLink = "Open OpenTrawl on GitHub"
  static let auditPrompt =
    "Audit the current OpenTrawl code at https://github.com/opentrawl/opentrawl. For Messages, WhatsApp, Telegram, Notes and Contacts, explain exactly which files OpenTrawl reads, which files it writes, and every network request it can make. Separate automatic behaviour, explicit commands, tests and unfinished code. Do not change anything."

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
  static let agentInstruction =
    "Use /Applications/OpenTrawl.app/Contents/Helpers/trawl to search and open my local OpenTrawl archives. Run it with no arguments for a short introduction and with --help for the complete current interface. Prefer normal text output. Use --json only when writing a script."
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
