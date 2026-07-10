import CoreGraphics
import Testing

@testable import PermissionGuide

@Test func convertsAWindowOnTheDisplayAboveTheMainDisplay() throws {
  let main = DisplayGeometry(
    quartzFrame: CGRect(x: 0, y: 0, width: 1920, height: 1080),
    appKitFrame: CGRect(x: 0, y: 0, width: 1920, height: 1080),
    visibleFrame: CGRect(x: 0, y: 0, width: 1920, height: 1050)
  )
  let upper = DisplayGeometry(
    quartzFrame: CGRect(x: 0, y: -1200, width: 1920, height: 1200),
    appKitFrame: CGRect(x: 0, y: 1080, width: 1920, height: 1200),
    visibleFrame: CGRect(x: 0, y: 1080, width: 1920, height: 1170)
  )

  let converted = try #require(
    GuidePlacement.convertFromQuartz(
      CGRect(x: 200, y: -1100, width: 800, height: 600),
      displays: [main, upper]
    )
  )

  #expect(converted.display == upper)
  #expect(converted.frame == CGRect(x: 200, y: 1580, width: 800, height: 600))
}

@Test func keepsTheGuideOnTheSettingsDisplay() {
  let display = DisplayGeometry(
    quartzFrame: CGRect(x: 1920, y: 0, width: 2560, height: 1440),
    appKitFrame: CGRect(x: 1920, y: 0, width: 2560, height: 1440),
    visibleFrame: CGRect(x: 1920, y: 0, width: 2560, height: 1410)
  )
  let settings = CGRect(x: 2200, y: 300, width: 1000, height: 760)

  let panel = GuidePlacement.panelFrame(
    beside: settings,
    on: display,
    panelSize: CGSize(width: 280, height: 240)
  )

  #expect(display.visibleFrame.contains(panel))
  #expect(!panel.intersects(settings))
}
