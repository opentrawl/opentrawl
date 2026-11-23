import AppKit
import Foundation
import SwiftUI

enum TrawlDesign {
  static let defaultWindow = CGSize(width: 1_200, height: 820)
  static let minimumWindow = CGSize(width: 920, height: 620)
  static let maximumWindow = CGSize(width: 4_096, height: 2_560)
  static let onboardingWindow = defaultWindow

  static let sidebarWidth: CGFloat = 248
  static let contentInset: CGFloat = 24
  static let onboardingFooterHeight: CGFloat = 72
  static let onboardingTopInset: CGFloat = 76
  static let onboardingPageWidth: CGFloat = 920
  static let commandDemoPageWidth: CGFloat =
    onboardingWindow.width - 30 * 2
  static let commandDemoTerminalHeight: CGFloat = 668
  static let commandDemoTerminalContentInset: CGFloat = 18
  static let commandDemoOutputFontSize: CGFloat = 18
  static let onboardingReadingWidth: CGFloat = onboardingPageWidth
  static let onboardingCopyWidth: CGFloat = onboardingPageWidth
  static let returningFooterHeight: CGFloat = 28
  static let onboardingHeroIcon: CGFloat = 96
  static let onboardingIntroSpacing: CGFloat = 14
  static let onboardingElementSpacing: CGFloat = 12
  static let onboardingSectionSpacing: CGFloat = 24
  static let onboardingBlockSpacing: CGFloat = 48
  static let onboardingSubgroupSpacing: CGFloat = 18
  static let permissionStateSlotHeight: CGFloat = 56
  static let onboardingRowHeight: CGFloat = 36

  static let searchResultsMinimumWidth: CGFloat = 320
  static let searchRecordMinimumWidth: CGFloat = 380
  static let searchResultsMaximumWidth: CGFloat = 390
  static let searchWorkspaceMaximumWidth: CGFloat = 1_600
  static let recordReadingWidth: CGFloat = 760
  static let cardCornerRadius: CGFloat = 12
  static let searchBarCornerRadius: CGFloat = 11

  static let brandRed = Color(red: 0.902, green: 0.2, blue: 0.137)
  static let canvasBackground = Color(nsColor: .windowBackgroundColor)
  static let sidebarBackground = Color(nsColor: .underPageBackgroundColor)
  static let surfaceBackground = Color(nsColor: .controlBackgroundColor)
  static let elevatedSurface = Color(nsColor: .textBackgroundColor)
  static let hairlineBorder = Color(nsColor: .separatorColor)

  enum Typography {
    static let pageTitle = Font.system(size: 24, weight: .semibold)
    static let sectionHeader = Font.system(size: 13, weight: .semibold)
    static let body = Font.system(size: 14, weight: .regular)
    static let meta = Font.system(size: 12, weight: .regular)
  }

  static func usesCompactSearchLayout(width: CGFloat) -> Bool {
    width < searchResultsMinimumWidth + searchRecordMinimumWidth
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
