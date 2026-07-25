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
  var pointerAngle: Angle?
  var onClose: () -> Void = {}

  init(icon: NSImage, appName: String, dragURL: URL, pointerAngle: Angle?) {
    self.icon = icon
    self.appName = appName
    self.dragURL = dragURL
    self.pointerAngle = pointerAngle
  }
}
