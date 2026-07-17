import Foundation

/// Production exposes the five beta apps. Local builds can expose every compiled app with
/// `OPENTRAWL_ENABLE_EXPERIMENTAL_APPS=1`; an installed build can use
/// `defaults write org.opentrawl.trawl OpenTrawlExperimentalApps -bool true`.
struct AppFeatureFlags: Equatable {
  enum Mode: Equatable {
    case beta
    case experimental
  }

  static let betaAppOrder = [
    "imessage", "whatsapp", "telegram", "notes", "contacts",
  ]
  static let betaAppIDs = Set(betaAppOrder)
  static let comingSoonAppOrder = ["gmail", "calendar", "photos", "twitter"]

  let mode: Mode

  var isExperimental: Bool { mode == .experimental }

  init(mode: Mode) {
    self.mode = mode
  }

  static func current(
    environment: [String: String] = ProcessInfo.processInfo.environment,
    defaults: UserDefaults = .standard
  ) -> AppFeatureFlags {
    let exposesExperimentalApps =
      environment["OPENTRAWL_ENABLE_EXPERIMENTAL_APPS"] == "1"
      || defaults.bool(forKey: "OpenTrawlExperimentalApps")
    return AppFeatureFlags(mode: exposesExperimentalApps ? .experimental : .beta)
  }

  func includes(_ appID: String) -> Bool {
    mode == .experimental || Self.betaAppIDs.contains(appID)
  }

  func syncAppIDs(reportedAppIDs: [String], installedAppIDs: Set<String>) -> [String] {
    if mode == .beta {
      return Self.betaAppOrder.filter(installedAppIDs.contains)
    }
    return reportedAppIDs.reduce(into: []) { appIDs, appID in
      let isAvailable =
        MacAppCatalog.bundleIdentifier(for: appID) == nil
        || installedAppIDs.contains(appID)
      if isAvailable, !appIDs.contains(appID) { appIDs.append(appID) }
    }
  }

  func onboardingAppIDs(reportedAppIDs: [String]) -> [String] {
    guard mode == .experimental else { return Self.betaAppOrder }
    return reportedAppIDs.reduce(into: Self.betaAppOrder) { appIDs, appID in
      if !appIDs.contains(appID) { appIDs.append(appID) }
    }
  }

  static func displayName(for appID: String) -> String {
    switch appID {
    case "calendar": "Calendar"
    case "contacts": "Contacts"
    case "gmail": "Gmail"
    case "imessage": "Messages"
    case "notes": "Notes"
    case "photos": "Photos"
    case "telegram": "Telegram"
    case "twitter": "X"
    case "whatsapp": "WhatsApp"
    default: appID
    }
  }
}
