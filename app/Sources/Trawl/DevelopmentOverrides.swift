import Foundation

struct DevelopmentOverrides: Equatable {
  static let allSourcesEnvironmentKey = "OPENTRAWL_ALL_SOURCES"

  let exposesAllSources: Bool

  static func current(
    environment: [String: String] = ProcessInfo.processInfo.environment
  ) -> DevelopmentOverrides {
    DevelopmentOverrides(exposesAllSources: environment[allSourcesEnvironmentKey] == "1")
  }
}
