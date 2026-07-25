import AppKit
import Foundation
import Testing

@testable import PermissionGuide

@Suite struct GuideContractTests {
  @Test @MainActor func deepLinksDirectlyToFullDiskAccess() {
    #expect(
      PermissionGuideController.settingsURL.absoluteString
        == "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_AllFiles"
    )
  }

  @Test @MainActor func modelCarriesExactBundleURLAsDragPayload() {
    let expected = URL(fileURLWithPath: "/Applications/OpenTrawl.app")
    let model = GuideModel(
      icon: NSImage(size: NSSize(width: 1, height: 1)),
      appName: "OpenTrawl",
      dragURL: expected,
      pointerAngle: nil
    )
    #expect(model.dragURL == expected)
  }
}
