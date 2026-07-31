import Foundation
import SwiftUI

enum TrawlDesign {
  static let defaultWindow = CGSize(width: 1200, height: 820)
  static let minimumWindow = defaultWindow
  static let maximumWindow = defaultWindow
  static let onboardingWindow = defaultWindow
  static let contentInset: CGFloat = 30
  static let constellationInset: CGFloat = 12
  static let onboardingFooterHeight: CGFloat = 72
  static let onboardingTopInset: CGFloat = 76
  static let onboardingPageWidth: CGFloat = 820
  static let commandDemoPageWidth: CGFloat =
    onboardingWindow.width - contentInset * 2
  static let commandDemoTerminalHeight: CGFloat = 668
  static let commandDemoTerminalContentInset: CGFloat = 18
  static let commandDemoOutputFontSize: CGFloat = 18
  static let onboardingReadingWidth: CGFloat = 680
  static let onboardingCopyWidth: CGFloat = 680
  static let returningFooterHeight: CGFloat = 28
  static let onboardingHeroIcon: CGFloat = 96
  static let onboardingIntroSpacing: CGFloat = 14
  static let onboardingElementSpacing: CGFloat = 12
  static let onboardingSectionSpacing: CGFloat = 24
  static let onboardingBlockSpacing: CGFloat = 48
  static let onboardingSubgroupSpacing: CGFloat = 18
  static let permissionStateSlotHeight: CGFloat = 56
  static let onboardingRowHeight: CGFloat = 36
  static let searchResultsMinimumWidth: CGFloat = 360
  static let searchRecordMinimumWidth: CGFloat = 400
  static let searchResultsMaximumWidth: CGFloat = 460
  static let searchWorkspaceMaximumWidth: CGFloat = 1_600
  static let recordReadingWidth: CGFloat = 760
  static let constellationMaximumWidth: CGFloat =
    maximumWindow.width - constellationInset * 2
  static let constellationMaximumHeight: CGFloat =
    maximumWindow.height - returningFooterHeight - constellationInset * 2
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

  enum Typography {
    static let pageTitle = Font.system(size: 24, weight: .semibold)
    static let sectionHeader = Font.system(size: 13, weight: .semibold)
    static let body = Font.system(size: 15, weight: .regular)
    static let meta = Font.system(size: 13, weight: .regular)
  }

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

enum TrawlTextRole {
  case pageTitle
  case sectionHeader
  case body
  case meta

  fileprivate var font: Font {
    switch self {
    case .pageTitle: TrawlDesign.Typography.pageTitle
    case .sectionHeader: TrawlDesign.Typography.sectionHeader
    case .body: TrawlDesign.Typography.body
    case .meta: TrawlDesign.Typography.meta
    }
  }
}

extension View {
  func trawlText(_ role: TrawlTextRole) -> some View {
    font(role.font)
  }
}
