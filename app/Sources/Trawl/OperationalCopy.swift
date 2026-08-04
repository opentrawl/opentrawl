import Foundation

enum OperationalCopy {
  enum SharedAction {
    static let back = "Back"
    static let cancel = "Stop"
    static let close = "Close"
    static let continueAction = "Continue"
    static let retry = "Try again"
  }

  enum Trust {
    static let copyAuditPrompt = "Copy audit prompt"
    static let copiedAuditPrompt = "Audit prompt copied"
    static let reviewBuild = "Review this build"
  }

  enum FullDiskAccess {
    static let open = "Open Full Disk Access"
    static let title = "Full Disk Access"
    static let openTrawl = "OpenTrawl"
    static let systemSettings = "System Settings"
    static let checkAgain = "Check access again"
    static let checking = "Checking access…"
    static let waiting = "Waiting for Full Disk Access…"
    static let confirmed = "Full Disk Access confirmed"
    static let notConfirmed = "OpenTrawl cannot confirm Full Disk Access yet."
    static let recovery = "Open Full Disk Access, turn on OpenTrawl, then return here."
    static let needed = "Full Disk Access needed"
    static let openSettingsStep = "Open Full Disk Access"
    static let addAppStep = "Drag OpenTrawl into the list"
    static let returnStep = "Return here"
  }

  enum ArchiveBuild {
    static let copyAIInstructions = "Copy instructions for your AI"
    static let copiedAIInstructions = "Instructions copied"
    static let finishSetup = "Finish setup"
    static let startSearching = "Start searching"
    static let yourApps = "Your apps"
    static let comingSoon = "Coming soon"
    static let moreApps = "More apps"
    static let moreAppsComingSoon = "More apps are coming soon"
  }

  enum CommandDemo {
    static let copyCommand = "Copy command"
    static let copiedCommand = "Command copied"
  }

  enum AppStatus {
    static let waiting = "Waiting"
    static let building = "Building…"
    static let finalising = "Finalising…"
    static let searchable = "Searchable"
    static let failed = "Could not build"
    static let notInstalled = "Not installed"
    static let comingSoon = "Coming soon"
    static let retryApp = "Retry"
    static let appsUnavailable = "Apps unavailable"
    static let statusCheckFailed = "OpenTrawl could not check your apps."
    static let statusCheckRecovery = "Try again. Apps that already work remain available."
    static let retrying = "Trying again…"
  }

  enum Home {
    static let updateNow = "Build now"
  }

  enum BuildIdentity {
    static let experimentalFeaturesOn = "Experimental features on"
    static let help = "Copy this version when you report a problem"
  }
}
