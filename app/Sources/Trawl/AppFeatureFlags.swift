import Foundation
import TrawlClient

/// The Go helper owns app eligibility and ordering. Swift only records whether the helper
/// was launched with the explicit all-app development override.
struct AppFeatureFlags: Equatable {
  enum Mode: Equatable {
    case beta
    case experimental
  }

  let mode: Mode

  var isExperimental: Bool { mode == .experimental }

  init(mode: Mode) {
    self.mode = mode
  }

  static func current(
    environment: [String: String] = ProcessInfo.processInfo.environment,
    defaults _: UserDefaults = .standard
  ) -> AppFeatureFlags {
    let exposesExperimentalApps =
      environment["OPENTRAWL_ALL_SOURCES"] == "1"
    return AppFeatureFlags(mode: exposesExperimentalApps ? .experimental : .beta)
  }

  func includes(_: RegisteredTrawlerIdentity) -> Bool {
    true
  }

  func trawlersToSync(
    reportedTrawlers: [RegisteredTrawlerIdentity],
    unavailableTrawlers: Set<RegisteredTrawlerIdentity>
  ) -> [RegisteredTrawlerIdentity] {
    reportedTrawlers.reduce(into: []) { trawlersToSync, registeredTrawler in
      if !unavailableTrawlers.contains(registeredTrawler),
        !trawlersToSync.contains(registeredTrawler)
      {
        trawlersToSync.append(registeredTrawler)
      }
    }
  }
}
