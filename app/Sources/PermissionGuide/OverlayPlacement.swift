import CoreGraphics
import SwiftUI

/// Computes the panel position from Quartz window coordinates and AppKit
/// display coordinates. Pure geometry keeps multi-display behaviour testable.
enum OverlayPlacement {
  static let gap: CGFloat = 24

  static func compute(
    settings: CGRect?,
    panelSize: CGSize,
    primaryScreen: CGRect,
    screens: [CGRect]
  ) -> (origin: CGPoint, pointer: Angle?) {
    guard let settings, settings.width > 0, settings.height > 0 else {
      return (centered(panelSize: panelSize, screen: primaryScreen), nil)
    }

    let appKitSettings = CGRect(
      x: settings.minX,
      y: primaryScreen.maxY - settings.maxY,
      width: settings.width,
      height: settings.height
    )
    let targetScreen =
      screen(containingMostOf: appKitSettings, screens: screens)
      ?? primaryScreen
    let visibleFrame = targetScreen
    let originY = min(
      max(appKitSettings.midY - panelSize.height / 2, visibleFrame.minY),
      visibleFrame.maxY - panelSize.height
    )

    let leftX = appKitSettings.minX - panelSize.width - gap
    let rightX = appKitSettings.maxX + gap
    let originX: CGFloat
    if leftX >= visibleFrame.minX {
      originX = leftX
    } else if rightX + panelSize.width <= visibleFrame.maxX {
      originX = rightX
    } else {
      originX = min(
        max(appKitSettings.midX - panelSize.width / 2, visibleFrame.minX),
        visibleFrame.maxX - panelSize.width
      )
    }

    let panelCenter = CGPoint(
      x: originX + panelSize.width / 2,
      y: originY + panelSize.height / 2
    )
    let angle = Angle(
      radians: atan2(
        appKitSettings.midY - panelCenter.y,
        appKitSettings.midX - panelCenter.x
      )
    )
    return (CGPoint(x: originX, y: originY), angle)
  }

  private static func screen(
    containingMostOf frame: CGRect,
    screens: [CGRect]
  ) -> CGRect? {
    guard
      let screen = screens.max(by: {
        intersectionArea(frame, $0) < intersectionArea(frame, $1)
      }), intersectionArea(frame, screen) > 0
    else {
      return nil
    }
    return screen
  }

  private static func intersectionArea(_ lhs: CGRect, _ rhs: CGRect) -> CGFloat {
    let intersection = lhs.intersection(rhs)
    guard !intersection.isNull else { return 0 }
    return intersection.width * intersection.height
  }

  private static func centered(panelSize: CGSize, screen: CGRect) -> CGPoint {
    CGPoint(
      x: screen.midX - panelSize.width / 2,
      y: screen.midY - panelSize.height / 2
    )
  }
}
