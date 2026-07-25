import CoreGraphics
import Testing

@testable import PermissionGuide

@Suite struct OverlayPlacementTests {
  let panel = CGSize(width: 300, height: 240)
  let primary = CGRect(x: 0, y: 0, width: 1440, height: 900)

  @Test func centersWithoutPointerWhenSettingsMissing() {
    let output = OverlayPlacement.compute(
      settings: nil,
      panelSize: panel,
      primaryScreen: primary,
      screens: [primary]
    )
    #expect(output.pointer == nil)
    #expect(output.origin == CGPoint(x: 570, y: 330))
  }

  @Test func placesLeftOfSettingsWhenRoom() {
    let settings = CGRect(x: 600, y: 200, width: 700, height: 500)
    let output = OverlayPlacement.compute(
      settings: settings,
      panelSize: panel,
      primaryScreen: primary,
      screens: [primary]
    )
    #expect(abs(output.origin.x - (600 - 300 - 24)) < 0.001)
    #expect(abs(output.pointer?.radians ?? .nan) < 0.5)
  }

  @Test func placesRightWhenThereIsNoRoomLeft() {
    let settings = CGRect(x: 100, y: 200, width: 700, height: 500)
    let output = OverlayPlacement.compute(
      settings: settings,
      panelSize: panel,
      primaryScreen: primary,
      screens: [primary]
    )
    #expect(abs(output.origin.x - (100 + 700 + 24)) < 0.001)
  }

  @Test func convertsQuartzTopLeftToAppKitBottomLeft() {
    let settings = CGRect(x: 600, y: 0, width: 400, height: 200)
    let output = OverlayPlacement.compute(
      settings: settings,
      panelSize: panel,
      primaryScreen: primary,
      screens: [primary]
    )
    #expect(abs(output.origin.y - 660) < 0.001)
  }

  @Test func staysOnNegativeOriginSecondaryDisplay() {
    let secondary = CGRect(x: -1440, y: 0, width: 1440, height: 900)
    let settings = CGRect(x: -300, y: 200, width: 250, height: 500)
    let output = OverlayPlacement.compute(
      settings: settings,
      panelSize: panel,
      primaryScreen: primary,
      screens: [primary, secondary]
    )
    #expect(abs(output.origin.x - (-300 - 300 - 24)) < 0.001)
    #expect(secondary.contains(output.origin))
  }

  @Test func staysOnVerticallyOffsetSecondaryDisplay() {
    let upper = CGRect(x: 0, y: 900, width: 1440, height: 900)
    // Quartz y is negative for a display above the main display.
    let settings = CGRect(x: 500, y: -760, width: 600, height: 500)
    let output = OverlayPlacement.compute(
      settings: settings,
      panelSize: panel,
      primaryScreen: primary,
      screens: [primary, upper]
    )
    #expect(upper.contains(output.origin))
    #expect(output.origin.y >= upper.minY)
    #expect(output.origin.y + panel.height <= upper.maxY)
  }

  @Test func clampsPanelToTheSettingsDisplay() {
    let narrow = CGRect(x: 1440, y: 0, width: 500, height: 900)
    let settings = CGRect(x: 1500, y: 100, width: 400, height: 700)
    let output = OverlayPlacement.compute(
      settings: settings,
      panelSize: panel,
      primaryScreen: primary,
      screens: [primary, narrow]
    )
    #expect(output.origin.x >= narrow.minX)
    #expect(output.origin.x + panel.width <= narrow.maxX)
  }
}
