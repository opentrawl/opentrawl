import Foundation

/// Production exposes the five beta apps. Local builds can expose every compiled app with
/// `OPENTRAWL_ENABLE_EXPERIMENTAL_APPS=1`; an installed build can use
/// `defaults write org.opentrawl.trawl OpenTrawlExperimentalApps -bool true`.
struct AppFeatureFlags: Equatable {
  static let betaAppOrder = [
    "imessage", "whatsapp", "telegram", "notes", "contacts",
  ]
  static let betaAppIDs = Set(betaAppOrder)
  static let comingSoonAppOrder = ["gmail", "calendar", "photos", "twitter"]

  let enabledAppIDs: Set<String>?

  static func current(
    environment: [String: String] = ProcessInfo.processInfo.environment,
    defaults: UserDefaults = .standard
  ) -> AppFeatureFlags {
    let exposesExperimentalApps =
      environment["OPENTRAWL_ENABLE_EXPERIMENTAL_APPS"] == "1"
      || defaults.bool(forKey: "OpenTrawlExperimentalApps")
    return AppFeatureFlags(enabledAppIDs: exposesExperimentalApps ? nil : betaAppIDs)
  }

  func includes(_ appID: String) -> Bool {
    enabledAppIDs?.contains(appID) ?? true
  }

  func syncAppIDs(reportedAppIDs: [String], installedAppIDs: Set<String>) -> [String] {
    if enabledAppIDs != nil {
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
    guard enabledAppIDs == nil else { return Self.betaAppOrder }
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
