import AppKit
import Foundation

@MainActor
public enum PermissionGuideController {
  public static let settingsURL = URL(
    string:
      "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_AllFiles"
  )!

  public static func openSystemSettings() {
    NSWorkspace.shared.open(settingsURL)
  }
}
