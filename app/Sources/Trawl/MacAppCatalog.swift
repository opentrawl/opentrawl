import AppKit
import Observation

struct MacAppDescriptor: Equatable {
  let appID: String
  let bundleIdentifier: String
}

enum MacAppCatalog {
  static let apps = [
    MacAppDescriptor(appID: "calendar", bundleIdentifier: "com.apple.iCal"),
    MacAppDescriptor(appID: "contacts", bundleIdentifier: "com.apple.AddressBook"),
    MacAppDescriptor(appID: "discord", bundleIdentifier: "com.hnc.Discord"),
    MacAppDescriptor(appID: "imessage", bundleIdentifier: "com.apple.MobileSMS"),
    MacAppDescriptor(appID: "notes", bundleIdentifier: "com.apple.Notes"),
    MacAppDescriptor(appID: "photos", bundleIdentifier: "com.apple.Photos"),
    MacAppDescriptor(appID: "telegram", bundleIdentifier: "ru.keepcoder.Telegram"),
    MacAppDescriptor(appID: "whatsapp", bundleIdentifier: "net.whatsapp.WhatsApp"),
  ]

  static func bundleIdentifier(for appID: String) -> String? {
    apps.first(where: { $0.appID == appID })?.bundleIdentifier
  }
}

@MainActor
@Observable
final class MacAppInstallations {
  static let absentAppIDsEnvironmentKey = "OPENTRAWL_SIMULATE_ABSENT_APPS"

  private let applicationIsInstalled: (String) -> Bool
  private let simulatedAbsentAppIDs: Set<String>

  private(set) var installedAppIDs: Set<String> = []

  init(
    environment: [String: String] = ProcessInfo.processInfo.environment,
    applicationIsInstalled: @escaping (String) -> Bool = {
      NSWorkspace.shared.urlForApplication(withBundleIdentifier: $0) != nil
    }
  ) {
    self.applicationIsInstalled = applicationIsInstalled
    #if DEBUG
      simulatedAbsentAppIDs = Self.parseAppIDs(
        environment[Self.absentAppIDsEnvironmentKey] ?? "")
    #else
      simulatedAbsentAppIDs = []
    #endif
    refresh()
  }

  func refresh() {
    installedAppIDs = Set(
      MacAppCatalog.apps.compactMap { app in
        guard !simulatedAbsentAppIDs.contains(app.appID),
          applicationIsInstalled(app.bundleIdentifier)
        else { return nil }
        return app.appID
      })
  }

  func isInstalled(_ appID: String) -> Bool {
    installedAppIDs.contains(appID)
  }

  /// Online and other non-Mac-app integrations do not require a local bundle.
  func isAvailable(_ appID: String) -> Bool {
    MacAppCatalog.bundleIdentifier(for: appID) == nil || isInstalled(appID)
  }

  private static func parseAppIDs(_ value: String) -> Set<String> {
    Set(
      value.split(separator: ",").map {
        $0.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
      }.filter { !$0.isEmpty })
  }
}
