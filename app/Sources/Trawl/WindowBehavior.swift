import AppKit
import SwiftUI

struct WindowBehavior: NSViewRepresentable {
  let isOnboarding: Bool

  func makeCoordinator() -> Coordinator {
    Coordinator()
  }

  func makeNSView(context _: Context) -> NSView {
    NSView(frame: .zero)
  }

  func updateNSView(_ view: NSView, context: Context) {
    guard let window = view.window else {
      DispatchQueue.main.async {
        guard let window = view.window else { return }
        context.coordinator.apply(isOnboarding: isOnboarding, to: window)
      }
      return
    }
    context.coordinator.apply(isOnboarding: isOnboarding, to: window)
  }

  @MainActor
  final class Coordinator {
    private var appliedMode: Bool?
    private var defaultTitleVisibility: NSWindow.TitleVisibility?
    private var defaultTitlebarAppearsTransparent: Bool?
    private var defaultToolbarVisibility: Bool?

    func apply(isOnboarding: Bool, to window: NSWindow) {
      let changedMode = appliedMode != isOnboarding
      appliedMode = isOnboarding

      if defaultTitleVisibility == nil {
        defaultTitleVisibility = window.titleVisibility
        defaultTitlebarAppearsTransparent = window.titlebarAppearsTransparent
        defaultToolbarVisibility = window.toolbar?.isVisible
      }

      window.level = .normal
      window.tabbingMode = .disallowed

      let zoomButton = window.standardWindowButton(.zoomButton)

      if isOnboarding {
        window.styleMask.remove(.resizable)
        window.contentMinSize = TrawlDesign.onboardingWindow
        window.contentMaxSize = TrawlDesign.onboardingWindow
        window.collectionBehavior.remove(.fullScreenPrimary)
        window.collectionBehavior.remove(.fullScreenAuxiliary)
        window.collectionBehavior.insert(.fullScreenNone)
        zoomButton?.isEnabled = false
        zoomButton?.isHidden = true
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.toolbar?.isVisible = false
        window.standardWindowButton(.toolbarButton)?.isHidden = true
        window.standardWindowButton(.documentIconButton)?.isHidden = true
        if changedMode || window.contentView?.frame.size != TrawlDesign.onboardingWindow {
          window.setContentSize(TrawlDesign.onboardingWindow)
        }
      } else {
        window.styleMask.insert(.resizable)
        window.contentMinSize = TrawlDesign.minimumWindow
        window.contentMaxSize = TrawlDesign.maximumWindow
        window.collectionBehavior.remove(.fullScreenNone)
        window.collectionBehavior.insert(.fullScreenPrimary)
        zoomButton?.isEnabled = true
        zoomButton?.isHidden = false
        window.titleVisibility = defaultTitleVisibility ?? .visible
        window.titlebarAppearsTransparent = defaultTitlebarAppearsTransparent ?? false
        if let defaultToolbarVisibility {
          window.toolbar?.isVisible = defaultToolbarVisibility
        }
        window.standardWindowButton(.toolbarButton)?.isHidden = false
        window.standardWindowButton(.documentIconButton)?.isHidden = false
        if changedMode {
          window.setContentSize(TrawlDesign.defaultWindow)
        }
      }
    }
  }
}
