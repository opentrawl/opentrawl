import Foundation
import SwiftUI

enum TrawlDesign {
  static let minimumWindow = CGSize(width: 760, height: 560)
  static let defaultWindow = CGSize(width: 1040, height: 720)
  static let contentInset: CGFloat = 28
  static let searchResultsMinimumWidth: CGFloat = 360
  static let searchRecordMinimumWidth: CGFloat = 400
  static let searchResultsMaximumWidth: CGFloat = 460
  static let searchWorkspaceMaximumWidth: CGFloat = 1_600
  static let recordReadingWidth: CGFloat = 760
  static let constellationMaximumWidth: CGFloat = 2_800
  static let constellationMaximumHeight: CGFloat = 1_200
  static let constellationMaximumAspectRatio: CGFloat = 2.4
  static let panelCornerRadius: CGFloat = 22
  static let backgroundContentOpacity = 0.42
  static let backgroundContentBlur: CGFloat = 4
  static let modalVeilOpacity = 0.68
  static let centreSize: CGFloat = 104
  static let sourceGraphAnchorOffset: CGFloat = 27

  static let brandRed = Color(red: 0.902, green: 0.2, blue: 0.137)
  static let net = Color.primary.opacity(0.1)
  static let spoke = Color.primary.opacity(0.18)

  static let meshSeed: UInt64 = {
    let identity =
      Bundle.main.object(forInfoDictionaryKey: "GitCommit") as? String
      ?? "opentrawl"
    return identity.utf8.reduce(0xcbf2_9ce4_8422_2325) { hash, byte in
      (hash ^ UInt64(byte)) &* 0x100_0000_01b3
    }
  }()

  static func usesCompactSearchLayout(width: CGFloat) -> Bool {
    width < contentInset * 2 + searchResultsMinimumWidth + searchRecordMinimumWidth
  }
}
