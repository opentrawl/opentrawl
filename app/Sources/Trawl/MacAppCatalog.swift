import AppKit
import Observation
import TrawlClient

@MainActor
@Observable
final class MacAppInstallations {
  static let absentAppIDsEnvironmentKey = "OPENTRAWL_SIMULATE_ABSENT_APPS"

  private let applicationIsInstalled: (String) -> Bool
  private let simulatedAbsentTrawlers: Set<RegisteredTrawlerIdentity>
  private var bundleIdentifiers: [RegisteredTrawlerIdentity: String] = [:]

  private(set) var installedTrawlers: Set<RegisteredTrawlerIdentity> = []
  private(set) var unavailableTrawlers: Set<RegisteredTrawlerIdentity> = []

  init(
    environment: [String: String] = ProcessInfo.processInfo.environment,
    applicationIsInstalled: @escaping (String) -> Bool = {
      NSWorkspace.shared.urlForApplication(withBundleIdentifier: $0) != nil
    }
  ) {
    self.applicationIsInstalled = applicationIsInstalled
    #if DEBUG
      simulatedAbsentTrawlers = Self.parseRegisteredTrawlers(
        environment[Self.absentAppIDsEnvironmentKey] ?? "")
    #else
      simulatedAbsentTrawlers = []
    #endif
  }

  func refresh(registeredTrawlerCatalog: [RegisteredTrawlerCatalogEntry]) {
    bundleIdentifiers = Dictionary(
      uniqueKeysWithValues: registeredTrawlerCatalog.compactMap { entry in
        guard entry.registeredTrawlerReleaseState == .available,
          let bundleIdentifier =
            entry.registeredTrawlerManifest.trawlerBranding?.bundleIdentifier,
          !bundleIdentifier.isEmpty
        else { return nil }
        return (entry.id, bundleIdentifier)
      })
    refresh()
  }

  func refresh() {
    installedTrawlers = Set(
      bundleIdentifiers.compactMap { registeredTrawler, bundleIdentifier in
        guard !simulatedAbsentTrawlers.contains(registeredTrawler),
          applicationIsInstalled(bundleIdentifier)
        else { return nil }
        return registeredTrawler
      })
    unavailableTrawlers = Set(bundleIdentifiers.keys).subtracting(installedTrawlers)
  }

  func isInstalled(_ registeredTrawler: RegisteredTrawlerIdentity) -> Bool {
    installedTrawlers.contains(registeredTrawler)
  }

  /// Online and other non-Mac-app integrations do not require a local bundle.
  func isAvailable(_ registeredTrawler: RegisteredTrawlerIdentity) -> Bool {
    !unavailableTrawlers.contains(registeredTrawler)
  }

  func availableRegisteredTrawlers(
    reportedByTrawlHelper registeredTrawlers: [RegisteredTrawlerIdentity]
  ) -> [RegisteredTrawlerIdentity] {
    registeredTrawlers.filter(isAvailable)
  }

  private static func parseRegisteredTrawlers(
    _ value: String
  ) -> Set<RegisteredTrawlerIdentity> {
    Set(
      value.split(separator: ",").map {
        RegisteredTrawlerIdentity(
          registeredTrawlerIdentity:
            $0.trimmingCharacters(in: .whitespacesAndNewlines).lowercased())
      }.filter { !$0.registeredTrawlerIdentity.isEmpty })
  }
}
