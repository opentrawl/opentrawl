import CoreGraphics
import Testing

@testable import PermissionGuide

private func window(
  owner: String,
  layer: Int,
  x: CGFloat,
  y: CGFloat,
  width: CGFloat,
  height: CGFloat
) -> [String: Any] {
  [
    kCGWindowOwnerName as String: owner,
    kCGWindowLayer as String: layer,
    kCGWindowBounds as String: [
      "X": x, "Y": y, "Width": width, "Height": height,
    ],
  ]
}

@Suite struct SettingsWindowLocatorTests {
  @Test func picksLargestSettingsWindow() {
    let windows = [
      window(owner: "System Settings", layer: 0, x: 100, y: 100, width: 300, height: 200),
      window(owner: "System Settings", layer: 0, x: 500, y: 120, width: 700, height: 500),
    ]
    #expect(
      SettingsWindowLocator.bounds(in: windows)
        == CGRect(x: 500, y: 120, width: 700, height: 500)
    )
  }

  @Test func ignoresOtherOwnersAndLayers() {
    let windows = [
      window(owner: "Finder", layer: 0, x: 0, y: 0, width: 900, height: 900),
      window(owner: "System Settings", layer: 25, x: 0, y: 0, width: 800, height: 600),
      window(owner: "System Settings", layer: 0, x: 40, y: 60, width: 640, height: 480),
    ]
    #expect(
      SettingsWindowLocator.bounds(in: windows)
        == CGRect(x: 40, y: 60, width: 640, height: 480)
    )
  }

  @Test func returnsNilWhenAbsent() {
    #expect(SettingsWindowLocator.bounds(in: []) == nil)
  }
}
