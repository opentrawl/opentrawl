import Foundation

/// Production exposes the five beta apps. Local builds can expose every compiled app with
/// `OPENTRAWL_ENABLE_EXPERIMENTAL_APPS=1`; an installed build can use
/// `defaults write org.opentrawl.trawl OpenTrawlExperimentalApps -bool true`.
struct AppFeatureFlags: Equatable {
  static let betaAppOrder = [
    "contacts", "imessage", "notes", "telegram", "whatsapp",
  ]
  static let betaAppIDs = Set(betaAppOrder)
  static let comingSoonAppIDs: Set<String> = ["gmail", "twitter"]

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

  static func displayName(for appID: String) -> String {
    switch appID {
    case "contacts": "Contacts"
    case "gmail": "Gmail"
    case "imessage": "Messages"
    case "notes": "Notes"
    case "telegram": "Telegram"
    case "twitter": "X"
    case "whatsapp": "WhatsApp"
    default: appID
    }
  }
}
