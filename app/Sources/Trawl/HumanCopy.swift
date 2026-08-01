import Foundation

// IMPORTANT: JOSH OWNS EVERY STRING IN THIS FILE.
// AGENTS MUST NEVER EDIT THESE STRINGS UNLESS JOSH DIRECTLY SUPPLIES OR
// APPROVES THE EXACT REPLACEMENT. IMPLEMENTATION AUTHORITY, COPY REVIEW,
// GRAMMAR CORRECTION, TEST REPAIR AND REQUESTS TO "MAKE IT CLEARER" DO NOT
// AUTHORISE CHANGES. THIS FILE MUST ALWAYS REMAIN TRACKED AND COMMITTED.
enum HumanCopy {
  enum Welcome {
    static let title = "Meet OpenTrawl."
    static let body = "OpenTrawl lets your apps work for you. Search your apps, and let your AI search them too (if you want)."
    static let privacy = "Your data stays on your Mac. The OpenTrawl app doesn't send your data anywhere."
    static let primaryAction = "Continue"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let appsTitle = "OpenTrawl searches these apps"
  }

  enum FullDiskAccess {
    static let title = "OpenTrawl needs Full Disk Access."
    static let body = "Your apps (e.g. Messages, Notes, Contacts, Calendar) store their data in small databases on your Mac. We need Full Disk Access to read these databases, build your archive, and let you search it."
    static let purpose = "The OpenTrawl app doesn't send your data anywhere. We don't change your apps. It's all read-only."
    static let trustGroupTitle = "Check for yourself"
    // AI-authored edit to Josh's original string. Delete this comment after reviewing it.
    static let trustGroupBody = "Opentrawl is Open Source software. You can read the code yourself to check what's going on, or you can copy a prompt and ask your AI (Codex, Claude, etc) to check it for you."
    static let readCodeAction = "Read the code on GitHub"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let copyAuditPromptAction = "Click to copy AI review prompt"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let copiedAuditPromptAction = "Prompt copied"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let dragAccessibilityLabel = "Drag the OpenTrawl icon onto Full Disk Access in Settings"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let openAction = "Open the settings"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let openTrawlLabel = "OpenTrawl"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let systemSettingsLabel = "Settings: Full Disk Access"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let addAppStep = "Drag OpenTrawl into the list of apps in Settings"
  }

  enum ArchiveBuild {
    // AI-authorship is uncertain. Delete this comment after reviewing the exact string below.
    static let title = "We're building your local archive."
    // AI-authorship is uncertain. Delete this comment after reviewing the exact string below.
    static let body = "OpenTrawl is extracting your data from your apps. This should take a few minutes. Your data is stored in your home folder, ~/.opentrawl. If you want, connect an AI (e.g. Codex, Claude) while you wait."
    // AI-authorship is uncertain. Delete this comment after reviewing the exact string below.
    static let readyTitle = "Your archive is ready."
    static let readyBody = "Nice!"
    static let failureTitle = ""
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let startSearchingAction = "Start searching"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let yourAppsTitle = "Your apps"
    // AI-authorship is uncertain. Delete this comment after reviewing the exact string below.
    static let moreAppsTitle = "Still cooking"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let progressFormat = "%1$d of %2$d apps ready"
  }

  enum ConnectAI {
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let title = "Connect your AI"
    static let body =
      "Click here to copy a prompt that tells your AI how to use OpenTrawl. It won't modify your AGENTS.md, your skills, or your $PATH - unless you ask your AI to do so."
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let copyAction = "Copy prompt"
  }

  enum SharedAction {
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let back = "Back"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let tryAgain = "Try again"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let stepProgressFormat = "Step %1$d of %2$d"
  }

  enum AppStatus {
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let failed = "Something went wrong"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let notInstalled = "Not installed"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let comingSoon = "Coming soon"
    // AI-authored. Delete this comment after reviewing the exact string below.
    static let retry = "Try again"
  }

  enum Home {
    static let updateArchiveAction = "Update your archive"
  }

  enum BuildIdentity {
    static let experimentalFeaturesOn = "Developer mode is enabled"
  }
}
