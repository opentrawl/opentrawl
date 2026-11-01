import CoreGraphics

/// Finds System Settings' windows using window metadata only.
enum SettingsWindowLocator {
  static let ownerName = "System Settings"

  struct SettingsWindows {
    let mainWindowBounds: CGRect
    /// macOS asks for an administrator password in a second System Settings
    /// window over the main one when an app is added to the list.
    let passwordPromptIsShowing: Bool
  }

  static func current() -> SettingsWindows? {
    let options: CGWindowListOption = [.optionOnScreenOnly, .excludeDesktopElements]
    guard
      let raw = CGWindowListCopyWindowInfo(options, kCGNullWindowID) as? [[String: Any]]
    else {
      return nil
    }
    return settingsWindows(in: raw)
  }

  static func settingsWindows(
    in windows: [[String: Any]],
    ownerName: String = ownerName
  ) -> SettingsWindows? {
    let all = allBounds(in: windows, ownerName: ownerName)
    guard let main = all.max(by: { $0.width * $0.height < $1.width * $1.height }) else {
      return nil
    }
    return SettingsWindows(
      mainWindowBounds: main,
      passwordPromptIsShowing: all.contains { $0 != main && $0.intersects(main) }
    )
  }

  static func bounds(in windows: [[String: Any]], ownerName: String = ownerName) -> CGRect? {
    settingsWindows(in: windows, ownerName: ownerName)?.mainWindowBounds
  }

  private static func allBounds(in windows: [[String: Any]], ownerName: String) -> [CGRect] {
    windows
      .filter { ($0[kCGWindowOwnerName as String] as? String) == ownerName }
      .filter { ($0[kCGWindowLayer as String] as? Int) == 0 }
      .compactMap { rect(from: $0[kCGWindowBounds as String]) }
      .filter { $0.width > 0 && $0.height > 0 }
  }

  private static func rect(from bounds: Any?) -> CGRect? {
    guard let dictionary = bounds as? [String: Any] else { return nil }
    return CGRect(dictionaryRepresentation: dictionary as CFDictionary)
  }
}
