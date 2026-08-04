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
    static let appsTitle = "OpenTrawl searches these apps"
  }

  enum FullDiskAccess {
    static let title = "OpenTrawl needs Full Disk Access."
    static let body = "Your apps (e.g. Messages, Notes, Contacts, Calendar) store their data in small databases on your Mac. We need Full Disk Access to read these databases, build your archive, and let you search it."
    static let purpose = "The OpenTrawl app doesn't send your data anywhere. We don't change your apps. It's all read-only."
    static let trustGroupTitle = "Check for yourself"
    static let trustGroupBody = "Opentrawl is Open Source software. You can read the code yourself to check what's going on, or you can copy a prompt and ask your AI (Codex, Claude, etc) to check it for you."
    static let readCodeAction = "Read the code on GitHub"
    static let copyAuditPromptAction = "Click to copy AI review prompt"
    static let copiedAuditPromptAction = "Prompt copied"
    static let dragAccessibilityLabel = "Drag the OpenTrawl icon onto Full Disk Access in Settings"
    static let openAction = "Open the settings"
    static let openTrawlLabel = "OpenTrawl"
    static let systemSettingsLabel = "Settings: Full Disk Access"
    static let addAppStep = "Drag OpenTrawl into the list of apps in Settings"
  }

  enum ArchiveBuild {
    static let title = "We're building your local archive."
    static let body = "OpenTrawl is extracting your data from your apps. This should take a few minutes. Your data is stored in your home folder, ~/.opentrawl. If you want, connect an AI (e.g. Codex, Claude) while you wait."
    static let readyTitle = "Your archive is ready."
    static let readyBody = "Nice!"
    static let failureTitle = ""
    static let startSearchingAction = "Start searching"
    static let yourAppsTitle = "Your apps"
    static let moreAppsTitle = "Still cooking"
    static let progressFormat = "%1$d of %2$d apps ready"
  }

  enum ConnectAI {
    static let title = "Connect your AI"
    static let body =
      "Click here to copy a prompt that tells your AI how to use OpenTrawl. It won't modify your AGENTS.md, your skills, or your $PATH - unless you ask your AI to do so."
    static let copyAction = "Copy prompt"
  }

  enum SharedAction {
    static let back = "Back"
    static let tryAgain = "Try again"
    static let stepProgressFormat = "Step %1$d of %2$d"
  }

  enum AppStatus {
    static let failed = "Something went wrong"
    static let notInstalled = "Not installed"
    static let comingSoon = "Coming soon"
    static let retry = "Try again"
  }

  enum Home {
    static let updateArchiveAction = "Update your archive"
  }

  enum BuildIdentity {
    static let experimentalFeaturesOn = "Developer mode is enabled"
  }
}
