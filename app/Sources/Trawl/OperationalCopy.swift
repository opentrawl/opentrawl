import Foundation
import TrawlClient

enum OperationalCopy {
  static let back = "Back"
  static let cancel = "Stop"
  static let close = "Close"
  static let continueAction = "Continue"
  static let copyAIInstructions = "Copy instructions for your AI"
  static let copiedAIInstructions = "Instructions copied"
  static let copyAuditPrompt = "Copy audit prompt"
  static let copiedAuditPrompt = "Audit prompt copied"
  static let reviewBuild = "Review this build"
  static let openFullDiskAccess = "Open Full Disk Access"
  static let fullDiskAccess = "Full Disk Access"
  static let openTrawl = "OpenTrawl"
  static let systemSettings = "System Settings"
  static let checkAccessAgain = "Check access again"
  static let checkingAccess = "Checking access…"
  static let waitingForAccess = "Waiting for Full Disk Access…"
  static let accessConfirmed = "Full Disk Access confirmed"
  static let accessNotConfirmed = "OpenTrawl cannot confirm Full Disk Access yet."
  static let accessRecovery = "Open Full Disk Access, turn on OpenTrawl, then return here."
  static let accessNeeded = "Full Disk Access needed"

  static let waiting = "Waiting"
  static let building = "Building…"
  static let searchable = "Searchable"
  static let failed = "Could not build"
  static let notInstalled = "Not installed"
  static let comingSoon = "Coming soon"
  static let retry = "Try again"
  static let retryApp = "Try again"
  static let finishSetup = "Finish setup"
  static let syncNow = "Build now"
  static let showDetails = "Show details"
  static let hideDetails = "Hide details"
  static let moreAppsComingSoon = "More apps are coming soon"

  static let appsUnavailable = "Apps unavailable"
  static let statusCheckFailed = "OpenTrawl could not check your apps."
  static let statusCheckRecovery = "Try again. Apps that already work remain available."
  static let retrying = "Trying again…"
  static let experimentalFeaturesOn = "Experimental features on"
  static let buildIdentityHelp = "Copy this version when you report a problem"

  static func counts(_ counts: [String]) -> String {
    counts.joined(separator: " · ")
  }

  static func failureDetail(for code: SourceFailureCode, appName: String) -> String {
    switch code {
    case .permission:
      "OpenTrawl needs permission to read \(appName)."
    case .authentication:
      "OpenTrawl needs you to sign in to \(appName)."
    case .notFound:
      "OpenTrawl could not find the \(appName) data."
    case .timeout:
      "\(appName) took too long to respond. Other apps continue to work."
    case .unavailable:
      "\(appName) is not available. Other apps continue to work."
    case .alreadySyncing:
      "OpenTrawl is already building \(appName)."
    case .cancelled:
      "OpenTrawl stopped building \(appName)."
    case .invalidInput, .internalError:
      "OpenTrawl could not build \(appName). Other apps continue to work."
    }
  }
}
