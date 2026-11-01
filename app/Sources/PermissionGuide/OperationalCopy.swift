import Foundation

enum OperationalCopy {
  enum FullDiskAccessOverlay {
    static func instruction(appName: String) -> String {
      "↑  Drag \(appName) into the Full Disk Access list above"
    }

    static let dragHelp = "Drag into the Full Disk Access list"

    static func dragAccessibilityLabel(appName: String) -> String {
      "Drag \(appName) into the Full Disk Access list"
    }

    static let granted = "Full Disk Access granted"
    static let dismiss = "Dismiss"
  }
}
