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
}
