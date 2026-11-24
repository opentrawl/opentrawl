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

  /// Three custom radii only: macOS draws window corners, native Capsule draws
  /// filter chips, and 22.37% squircles draw app icons. Rows, rules, and match
  /// diamonds stay square (0).
  enum Radius {
    static let control: CGFloat = 6
    static let surface: CGFloat = 12
    static let panel: CGFloat = 20
    static let appIconFraction: CGFloat = 0.2237
  }

  enum Colors {
    static let paper = dynamicColor(
      light: NSColor(srgbRed: 1.0, green: 1.0, blue: 1.0, alpha: 1.0),
      dark: NSColor(srgbRed: 0.043, green: 0.055, blue: 0.075, alpha: 1.0)
    )
    static let surface = dynamicColor(
      light: NSColor(srgbRed: 0.969, green: 0.969, blue: 0.969, alpha: 1.0),
      dark: NSColor(srgbRed: 0.075, green: 0.094, blue: 0.125, alpha: 1.0)
    )
    static let ink = dynamicColor(
      light: NSColor(srgbRed: 0.063, green: 0.063, blue: 0.063, alpha: 1.0),
      dark: NSColor(srgbRed: 0.949, green: 0.957, blue: 0.973, alpha: 1.0)
    )
    static let mutedInk = dynamicColor(
      light: NSColor(srgbRed: 0.435, green: 0.435, blue: 0.435, alpha: 1.0),
      dark: NSColor(srgbRed: 0.569, green: 0.600, blue: 0.659, alpha: 1.0)
    )
    static let hairline = dynamicColor(
      light: NSColor(srgbRed: 0.863, green: 0.863, blue: 0.863, alpha: 1.0),
      dark: NSColor(srgbRed: 0.145, green: 0.173, blue: 0.220, alpha: 1.0)
    )
    static let matchRed = dynamicColor(
      light: NSColor(srgbRed: 0.902, green: 0.200, blue: 0.137, alpha: 1.0),
      dark: NSColor(srgbRed: 0.941, green: 0.306, blue: 0.251, alpha: 1.0)
    )
    static let selectBlue = dynamicColor(
      light: NSColor(srgbRed: 0.110, green: 0.275, blue: 0.839, alpha: 1.0),
      dark: NSColor(srgbRed: 0.310, green: 0.494, blue: 1.0, alpha: 1.0)
    )
    static let selectBlueSurface = dynamicColor(
      light: NSColor(srgbRed: 0.110, green: 0.275, blue: 0.839, alpha: 0.10),
      dark: NSColor(srgbRed: 0.310, green: 0.494, blue: 1.0, alpha: 0.18)
    )
    static let warningAmber = dynamicColor(
      light: NSColor(srgbRed: 0.769, green: 0.482, blue: 0.0, alpha: 1.0),
      dark: NSColor(srgbRed: 0.941, green: 0.659, blue: 0.188, alpha: 1.0)
    )
    static let warningAmberSurface = dynamicColor(
      light: NSColor(srgbRed: 0.769, green: 0.482, blue: 0.0, alpha: 0.08),
      dark: NSColor(srgbRed: 0.941, green: 0.659, blue: 0.188, alpha: 0.14)
    )

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
  static let panelCornerRadius: CGFloat = Radius.panel

  enum Typography {
    static let pageTitle = Font.system(size: 24, weight: .semibold)
    static let sectionHeader = Font.system(size: 13, weight: .semibold)
    static let body = Font.system(size: 14, weight: .regular)
    static let meta = Font.system(size: 12, weight: .regular)
    static let caption = Font.system(size: 11, weight: .medium)
    static let laneLabel = Font.system(size: 12.5, weight: .medium)
    static let monoSmall = Font.system(size: 11.5, weight: .regular, design: .monospaced)
    static let monoAxis = Font.system(size: 10.5, weight: .medium, design: .monospaced)
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
