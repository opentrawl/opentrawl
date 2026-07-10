import CoreGraphics
import Foundation

public struct DisplayGeometry: Sendable, Equatable {
  public let quartzFrame: CGRect
  public let appKitFrame: CGRect
  public let visibleFrame: CGRect

  public init(quartzFrame: CGRect, appKitFrame: CGRect, visibleFrame: CGRect) {
    self.quartzFrame = quartzFrame
    self.appKitFrame = appKitFrame
    self.visibleFrame = visibleFrame
  }
}

public struct ConvertedWindow: Sendable, Equatable {
  public let frame: CGRect
  public let display: DisplayGeometry

  public init(frame: CGRect, display: DisplayGeometry) {
    self.frame = frame
    self.display = display
  }
}

public enum GuidePlacement {
  public static func convertFromQuartz(
    _ window: CGRect,
    displays: [DisplayGeometry]
  ) -> ConvertedWindow? {
    guard
      let display = displays.max(by: {
        intersectionArea(window, $0.quartzFrame) < intersectionArea(window, $1.quartzFrame)
      })
    else {
      return nil
    }

    let localX = window.minX - display.quartzFrame.minX
    let localY = window.minY - display.quartzFrame.minY
    let frame = CGRect(
      x: display.appKitFrame.minX + localX,
      y: display.appKitFrame.maxY - localY - window.height,
      width: window.width,
      height: window.height
    )
    return ConvertedWindow(frame: frame, display: display)
  }

  public static func panelFrame(
    beside settingsFrame: CGRect,
    on display: DisplayGeometry,
    panelSize: CGSize,
    gap: CGFloat = 16
  ) -> CGRect {
    let visible = display.visibleFrame
    let centredY = min(
      max(settingsFrame.midY - panelSize.height / 2, visible.minY),
      visible.maxY - panelSize.height
    )
    let leftX = settingsFrame.minX - gap - panelSize.width
    if leftX >= visible.minX {
      return CGRect(x: leftX, y: centredY, width: panelSize.width, height: panelSize.height)
    }

    let rightX = settingsFrame.maxX + gap
    if rightX + panelSize.width <= visible.maxX {
      return CGRect(x: rightX, y: centredY, width: panelSize.width, height: panelSize.height)
    }

    return CGRect(
      x: visible.maxX - panelSize.width,
      y: visible.maxY - panelSize.height,
      width: panelSize.width,
      height: panelSize.height
    )
  }

  private static func intersectionArea(_ lhs: CGRect, _ rhs: CGRect) -> CGFloat {
    let intersection = lhs.intersection(rhs)
    guard !intersection.isNull else { return 0 }
    return intersection.width * intersection.height
  }
}
