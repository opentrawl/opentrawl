import AppKit
import Foundation
import SwiftUI

enum TrawlDesign {
  static let defaultWindow = CGSize(width: 1_360, height: 860)
  static let minimumWindow = CGSize(width: 720, height: 680)
  static let onboardingWindow = CGSize(width: 960, height: 740)
  static let compactRailBreakpoint: CGFloat = 960
  static let wideSidebarWidth: CGFloat = 224
  static let compactSidebarWidth: CGFloat = 56

  static func clampedDefaultWindowSize(for visibleFrame: CGRect?) -> CGSize {
    guard let visibleFrame else { return defaultWindow }
    let width = min(defaultWindow.width, max(minimumWindow.width, visibleFrame.width - 24))
    let height = min(defaultWindow.height, max(minimumWindow.height, visibleFrame.height - 24))
    return CGSize(width: floor(width), height: floor(height))
  }

  enum Space {
    static let xs: CGFloat = 4
    static let sm: CGFloat = 8
    static let md: CGFloat = 14
    static let lg: CGFloat = 20
    static let xl: CGFloat = 34
    static let xxl: CGFloat = 48
  }

  /// Strict two-radius system for chrome: `surface` (10pt) for cards/containers,
  /// `control` (6pt) for buttons, inputs, chips, and rows, plus macOS's 22.37%
  /// squircle for app icons.
  enum Radius {
    static let control: CGFloat = 6
    static let surface: CGFloat = 10
    static let appIconFraction: CGFloat = 0.2237
  }

  enum Shape {
    static let control = RoundedRectangle(cornerRadius: Radius.control, style: .continuous)
    static let surface = RoundedRectangle(cornerRadius: Radius.surface, style: .continuous)
  }

  enum Colors {
    static let paper = Color(nsColor: .windowBackgroundColor)
    static let surface = Color(nsColor: .controlBackgroundColor)
    static let ink = Color.primary
    static let mutedInk = Color.secondary
    static let hairline = Color(nsColor: .separatorColor)
    static let matchRed = dynamicColor(
      light: NSColor(srgbRed: 0.788, green: 0.118, blue: 0.071, alpha: 1.0),
      dark: NSColor(srgbRed: 0.941, green: 0.353, blue: 0.302, alpha: 1.0)
    )
    static let messageBubble = Color.primary.opacity(0.06)
    static let selectBlue = Color.accentColor
    static let selectBlueSurface = Color.accentColor.opacity(0.14)
    static let warningAmber = Color.orange
    static let warningAmberText = dynamicColor(
      light: NSColor(srgbRed: 0.631, green: 0.361, blue: 0.027, alpha: 1.0),
      dark: NSColor(srgbRed: 0.941, green: 0.694, blue: 0.267, alpha: 1.0)
    )
    static let warningAmberSurface = Color.orange.opacity(0.12)

    static func dynamicColor(light: NSColor, dark: NSColor) -> Color {
      Color(
        nsColor: NSColor(name: nil) { appearance in
          let match = appearance.bestMatch(from: [.darkAqua, .aqua])
          return match == .darkAqua ? dark : light
        })
    }
  }

  static let brandRed = Colors.matchRed
  static let contentInset: CGFloat = Space.lg
  static let onboardingFooterHeight: CGFloat = 72
  static let onboardingTopInset: CGFloat = 44
  static let onboardingPageWidth: CGFloat = 860
  static let commandDemoPageWidth: CGFloat =
    onboardingWindow.width - 30 * 2
  static let commandDemoTerminalHeight: CGFloat = 560
  static let commandDemoTerminalContentInset: CGFloat = 18
  static let commandDemoOutputFontSize: CGFloat = 18
  static let onboardingReadingWidth: CGFloat = onboardingPageWidth
  static let onboardingCopyWidth: CGFloat = onboardingPageWidth
  static let returningFooterHeight: CGFloat = 28
  static let onboardingHeroIcon: CGFloat = 96
  static let onboardingIntroSpacing: CGFloat = Space.md
  static let onboardingElementSpacing: CGFloat = 12
  static let onboardingSectionSpacing: CGFloat = 24
  static let onboardingBlockSpacing: CGFloat = Space.xxl
  static let onboardingSubgroupSpacing: CGFloat = 18
  static let permissionStateSlotHeight: CGFloat = 56
  static let onboardingRowHeight: CGFloat = 36

  // Archive timeline band geometry
  static let timelineRestingLaneMinimumHeight: CGFloat = 34
  static let timelineRestingLaneMaximumHeight: CGFloat = 50
  static let timelineLaneHeight: CGFloat = 26
  static let timelineSlimLaneHeight: CGFloat = 14
  static let timelineLabelColumnWidth: CGFloat = 156
  static let timelineOverviewLaneHeight: CGFloat = 34
  static let searchDateScrubberLineHeight: CGFloat = 44
  static let timelineCountColumnWidth: CGFloat = 94
  static let timelineUndatedColumnWidth: CGFloat = 28
  static let timelineFutureGutterWidth: CGFloat = 34
  static let timelineAxisHeight: CGFloat = 28
  static let timelineMinimumBottomSplitHeight: CGFloat = 400

  // Search split geometry
  static let searchResultsMinimumWidth: CGFloat = 380
  static let searchRecordMinimumWidth: CGFloat = 420
  static let searchResultsMaximumWidth: CGFloat = 490
  static let searchWorkspaceMaximumWidth: CGFloat = 1_680
  static let recordReadingWidth: CGFloat = 760

  /// One type scale in macOS point sizes: 17 and 15 for titles, 13 for body,
  /// 12 for supporting text and 11 for the smallest labels. Nothing in the
  /// main window is smaller than 11.
  enum Typography {
    static let pageTitle = Font.title2.weight(.semibold)
    static let itemTitle = Font.title3.weight(.semibold)
    static let sectionHeader = Font.headline
    static let body = Font.body
    static let bodyEmphasis = Font.body.weight(.semibold)
    static let meta = Font.callout
    static let smallLabel = Font.subheadline
    static let smallNumber = Font.subheadline.monospacedDigit()
    static let laneLabel = Font.body
    static let monoSmall = Font.callout.monospacedDigit()
  }

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
