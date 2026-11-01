import AppKit
import SwiftUI

/// Marks where the app tile sits on the onboarding page, so the guide can fly
/// the tile from there into System Settings.
public struct ApplicationTileFlightOrigin: NSViewRepresentable {
  public init() {}

  public func makeNSView(context: Context) -> NSView {
    let view = NSView()
    ApplicationTileFlight.pageTileView = view
    return view
  }

  public func updateNSView(_ view: NSView, context: Context) {}
}

/// Carries a picture of the app tile from the onboarding page to the docked
/// guide's slot, so the person sees that the tile in System Settings is the
/// one the page showed them.
@MainActor
final class ApplicationTileFlight {
  weak static var pageTileView: NSView?

  private static let duration: TimeInterval = 0.6
  private static let lift: CGFloat = 10
  /// System Settings that has not opened by then gets no flight.
  private static let waitForSettingsWindow: Duration = .seconds(4)

  private let window: NSPanel
  private var landed = false

  /// The page tile's frame on screen, if it is visible and motion is allowed.
  static func takeOffFrame() -> CGRect? {
    guard !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion,
      let view = pageTileView, let window = view.window, window.isVisible
    else { return nil }
    return window.convertToScreen(view.convert(view.bounds, to: nil))
  }

  /// Shows the tile lifted off the page at once, while System Settings opens.
  init(tileImage: NSImage, takeOffFrame: CGRect, landingSize: CGSize) {
    let scale = takeOffFrame.width / landingSize.width
    let size = CGSize(width: landingSize.width * scale, height: landingSize.height * scale)
    let start = CGRect(
      x: takeOffFrame.midX - size.width / 2,
      y: takeOffFrame.midY - size.height / 2 + Self.lift,
      width: size.width,
      height: size.height
    )
    window = NSPanel(
      contentRect: start,
      styleMask: [.borderless, .nonactivatingPanel],
      backing: .buffered,
      defer: false
    )
    let imageView = NSImageView(image: tileImage)
    imageView.imageScaling = .scaleAxesIndependently
    window.contentView = imageView
    window.isOpaque = false
    window.backgroundColor = .clear
    window.hasShadow = true
    window.ignoresMouseEvents = true
    window.level = NSWindow.Level(rawValue: NSWindow.Level.floating.rawValue + 1)
    window.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
    window.orderFrontRegardless()

    Task { @MainActor [weak self] in
      try? await Task.sleep(for: Self.waitForSettingsWindow)
      self?.cancel()
    }
  }

  /// Returns false when the flight was already given up, so the caller shows
  /// the docked tile straight away.
  func fly(to landingFrame: CGRect, completion: @escaping @MainActor () -> Void) -> Bool {
    guard !landed else { return false }
    landed = true
    NSAnimationContext.runAnimationGroup { context in
      context.duration = Self.duration
      context.timingFunction = CAMediaTimingFunction(name: .easeInEaseOut)
      window.animator().setFrame(landingFrame, display: true)
    } completionHandler: { [window] in
      MainActor.assumeIsolated {
        window.orderOut(nil)
        completion()
      }
    }
    return true
  }

  func cancel() {
    guard !landed else { return }
    landed = true
    window.orderOut(nil)
  }
}
