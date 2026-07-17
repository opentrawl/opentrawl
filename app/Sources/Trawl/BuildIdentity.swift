import Foundation
import SwiftUI

struct BuildIdentity: Equatable, Sendable {
  let version: String
  let gitCommit: String
  let hasLocalChanges: Bool

  static let current = BuildIdentity(bundle: .main)

  init(bundle: Bundle) {
    self.init(
      version: bundle.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String,
      gitCommit: bundle.object(forInfoDictionaryKey: "GitCommit") as? String,
      hasLocalChanges: bundle.object(forInfoDictionaryKey: "GitDirty") as? Bool ?? false
    )
  }

  init(
    version: String?,
    gitCommit: String?,
    hasLocalChanges: Bool = false
  ) {
    self.version = Self.present(version, fallback: "development")
    self.gitCommit = Self.present(gitCommit, fallback: "unknown")
    self.hasLocalChanges = hasLocalChanges
  }

  var shortCommit: String {
    String(gitCommit.prefix(7))
  }

  var displayName: String {
    let suffix = hasLocalChanges ? "+changes" : ""
    return "OpenTrawl \(version) · \(shortCommit)\(suffix)"
  }

  var sourceURL: URL? {
    guard gitCommit.count == 40, gitCommit.allSatisfy(\.isHexDigit) else { return nil }
    return URL(string: "https://github.com/opentrawl/opentrawl/tree/\(gitCommit)")
  }

  private static func present(_ value: String?, fallback: String) -> String {
    guard let value, !value.isEmpty else { return fallback }
    return value
  }
}

struct BuildIdentityBadge: View {
  let identity: BuildIdentity
  let isExperimental: Bool

  var body: some View {
    HStack(spacing: 8) {
      if isExperimental {
        Text("Experimental features on")
          .font(.caption.weight(.semibold))
          .foregroundStyle(TrawlDesign.brandRed)
      }
      if let sourceURL = identity.sourceURL {
        Link(destination: sourceURL) {
          badgeLabel
        }
        .help("Open the exact source code for this build")
      } else {
        badgeLabel
      }
    }
    .padding(.horizontal, 10)
    .padding(.vertical, 6)
    .background(.thinMaterial, in: .capsule)
  }

  private var badgeLabel: some View {
    Text(identity.displayName)
      .font(.caption.monospaced())
      .foregroundStyle(.secondary)
  }
}
