import AppKit
import Observation
import TrawlClient

@MainActor
@Observable
final class MacAppInstallations {
  static let absentAppIDsEnvironmentKey = "OPENTRAWL_SIMULATE_ABSENT_APPS"

  private let applicationIsInstalled: (String) -> Bool
  private let simulatedAbsentAppIDs: Set<String>
  private var bundleIdentifiers: [String: String] = [:]

  private(set) var installedAppIDs: Set<String> = []
  private(set) var unavailableAppIDs: Set<String> = []

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
  }

  func refresh(manifests: [SourceManifest]) {
    bundleIdentifiers = Dictionary(
      uniqueKeysWithValues: manifests.compactMap { manifest in
        guard let bundleIdentifier = manifest.branding?.bundleIdentifier,
          !bundleIdentifier.isEmpty
        else { return nil }
        return (manifest.sourceID, bundleIdentifier)
      })
    refresh()
  }

  func refresh() {
    installedAppIDs = Set(
      bundleIdentifiers.compactMap { appID, bundleIdentifier in
        guard !simulatedAbsentAppIDs.contains(appID), applicationIsInstalled(bundleIdentifier)
        else { return nil }
        return appID
      })
    unavailableAppIDs = Set(bundleIdentifiers.keys).subtracting(installedAppIDs)
  }

  func isInstalled(_ appID: String) -> Bool {
    installedAppIDs.contains(appID)
  }

  /// Online and other non-Mac-app integrations do not require a local bundle.
  func isAvailable(_ appID: String) -> Bool {
    !unavailableAppIDs.contains(appID)
  }

  private static func parseAppIDs(_ value: String) -> Set<String> {
    Set(
      value.split(separator: ",").map {
        $0.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
      }.filter { !$0.isEmpty })
  }
}
