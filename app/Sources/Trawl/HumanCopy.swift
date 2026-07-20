import Foundation

// IMPORTANT: JOSH OWNS EVERY STRING IN THIS FILE.
// AGENTS MUST NEVER EDIT THESE STRINGS UNLESS JOSH DIRECTLY SUPPLIES OR
// APPROVES THE EXACT REPLACEMENT. IMPLEMENTATION AUTHORITY, COPY REVIEW,
// GRAMMAR CORRECTION, TEST REPAIR AND REQUESTS TO "MAKE IT CLEARER" DO NOT
// AUTHORISE CHANGES. THIS FILE MUST ALWAYS REMAIN TRACKED AND COMMITTED.
enum HumanCopy {
  static let welcomeStep = "01  TAKE BACK YOUR DATA"
  static let permissionStep = "02  FULL DISK ACCESS"
  static let buildStep = "03  BUILD YOUR ARCHIVE"

  static let welcomeTitle = "Take back your data."
  static let welcomeBody =
    "OpenTrawl reads Messages, WhatsApp, Telegram, Notes and Contacts and builds a searchable archive on your Mac, ready for your AI."
  static let archiveLocation =
    "Each app gets its own SQLite archive under ~/.opentrawl."
  static let archiveStaysLocal = "Your archive never leaves your Mac."
  static let originalsStayUntouched = "OpenTrawl never writes to your apps."
  static let openSource = "Open Source, MIT licensed. Read the code."
  static let start = "Build my archive"

  static let permissionTitle = "Add OpenTrawl to Full Disk Access"
  static let permissionBody =
    "Drag OpenTrawl into the Full Disk Access list, then turn it on."
  static let permissionDragAccessibilityLabel = "Drag OpenTrawl to Full Disk Access"

  static let buildTitle = "Building your archive"
  static let buildBody = "Each app becomes searchable when it finishes."

  static let aiTitle = "Connect your AI"
  static let aiBody =
    "If you connect a model, you are trusting it with anything it asks OpenTrawl to read. Choose a model and permissions appropriate to your personal threat model."
  static let aiDoesNotInstall =
    "This copies text only. OpenTrawl does not install a skill, change PATH or edit your AI configuration."
}
