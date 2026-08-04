import Foundation

enum OperationalCopy {
  enum FullDiskAccessOverlay {
    static let title = "Full Disk Access"

    static func instruction(appName: String) -> String {
      "Drag \(appName) into the Full Disk Access list, then turn it on."
    }

    static let dragHelp = "Drag into the Full Disk Access list"

    static func dragAccessibilityLabel(appName: String) -> String {
      "Drag \(appName) into the Full Disk Access list"
    }

    static let granted = "Full Disk Access granted"
    static let dismiss = "Dismiss"
  }
}
