import CoreGraphics

/// Finds the largest normal System Settings window using window metadata only.
enum SettingsWindowLocator {
  static let ownerName = "System Settings"

  static func currentBounds() -> CGRect? {
    let options: CGWindowListOption = [.optionOnScreenOnly, .excludeDesktopElements]
    guard
      let raw = CGWindowListCopyWindowInfo(options, kCGNullWindowID) as? [[String: Any]]
    else {
      return nil
    }
    return bounds(in: raw)
  }

  static func bounds(in windows: [[String: Any]], ownerName: String = ownerName) -> CGRect? {
    windows
      .filter { ($0[kCGWindowOwnerName as String] as? String) == ownerName }
      .filter { ($0[kCGWindowLayer as String] as? Int) == 0 }
      .compactMap { rect(from: $0[kCGWindowBounds as String]) }
      .filter { $0.width > 0 && $0.height > 0 }
      .max { $0.width * $0.height < $1.width * $1.height }
  }

  private static func rect(from bounds: Any?) -> CGRect? {
    guard let dictionary = bounds as? [String: Any] else { return nil }
    return CGRect(dictionaryRepresentation: dictionary as CFDictionary)
  }
}
