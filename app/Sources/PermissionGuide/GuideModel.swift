import AppKit
import SwiftUI

/// Shared state between the guide controller and its SwiftUI overlay.
@MainActor
@Observable
final class GuideModel {
  enum Phase {
    case guiding
    case granted
  }

  var phase: Phase = .guiding
  let icon: NSImage
  let appName: String
  let dragURL: URL
  /// The docked tile stays empty while its copy flies in from the page.
  var dockedTileIsWaitingForFlight = false
  @ObservationIgnored weak var dockedTileView: NSView?
  var onClose: () -> Void = {}

  init(icon: NSImage, appName: String, dragURL: URL) {
    self.icon = icon
    self.appName = appName
    self.dragURL = dragURL
  }
}
