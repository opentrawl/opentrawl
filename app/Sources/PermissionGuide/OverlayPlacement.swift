import CoreGraphics

/// Docks the guide inside the bottom-right corner of System Settings, over
/// the Full Disk Access list, the way the list's own rows sit. Pure geometry
/// keeps multi-display behaviour testable.
enum OverlayPlacement {
  static let insetInsideSettingsWindow: CGFloat = 16

  /// `settings` is in Quartz window coordinates (top-left origin of the
  /// primary display); the result is an AppKit origin. `transparentMargin` is
  /// the clear border around the panel's card that leaves room for its shadow.
  static func dockedPanelOrigin(
    settings: CGRect,
    panelSize: CGSize,
    transparentMargin: CGFloat,
    primaryScreen: CGRect,
    visibleFrames: [CGRect]
  ) -> CGPoint {
    let appKitSettings = CGRect(
      x: settings.minX,
      y: primaryScreen.maxY - settings.maxY,
      width: settings.width,
      height: settings.height
    )
    let x = appKitSettings.maxX - insetInsideSettingsWindow - panelSize.width + transparentMargin
    let y = appKitSettings.minY + insetInsideSettingsWindow - transparentMargin
    guard
      let visible = visibleFrames.first(where: { $0.intersects(appKitSettings) })
    else {
      return CGPoint(x: x, y: y)
    }
    return CGPoint(
      x: min(max(x, visible.minX - transparentMargin), visible.maxX - panelSize.width + transparentMargin),
      y: min(max(y, visible.minY - transparentMargin), visible.maxY - panelSize.height + transparentMargin)
    )
  }
}
