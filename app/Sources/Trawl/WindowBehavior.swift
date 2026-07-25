import AppKit
import SwiftUI

struct WindowBehavior: NSViewRepresentable {
  let isOnboarding: Bool
  let keepsPermissionGuideVisible: Bool

  func makeCoordinator() -> Coordinator {
    Coordinator()
  }

  func makeNSView(context _: Context) -> NSView {
    NSView(frame: .zero)
  }

  func updateNSView(_ view: NSView, context: Context) {
    DispatchQueue.main.async {
      guard let window = view.window else { return }
      context.coordinator.apply(
        isOnboarding: isOnboarding,
        keepsPermissionGuideVisible: keepsPermissionGuideVisible,
        to: window
      )
    }
  }

  @MainActor
  final class Coordinator {
    private var appliedMode: Mode?

    func apply(
      isOnboarding: Bool,
      keepsPermissionGuideVisible: Bool = false,
      to window: NSWindow
    ) {
      let mode = Mode(
        isOnboarding: isOnboarding,
        keepsPermissionGuideVisible: keepsPermissionGuideVisible
      )
      guard appliedMode != mode else { return }
      let isInitialConfiguration = appliedMode == nil
      appliedMode = mode
      window.level = keepsPermissionGuideVisible ? .floating : .normal

      if isOnboarding {
        window.styleMask.remove(.resizable)
        window.minSize = TrawlDesign.onboardingWindow
        window.maxSize = TrawlDesign.onboardingWindow
        window.setContentSize(TrawlDesign.onboardingWindow)
        window.standardWindowButton(.zoomButton)?.isEnabled = false
        if isInitialConfiguration { window.center() }
      } else {
        window.styleMask.insert(.resizable)
        window.minSize = TrawlDesign.minimumWindow
        window.maxSize = NSSize(
          width: CGFloat.greatestFiniteMagnitude,
          height: CGFloat.greatestFiniteMagnitude
        )
        window.standardWindowButton(.zoomButton)?.isEnabled = true
        if !isInitialConfiguration {
          window.setContentSize(TrawlDesign.defaultWindow)
        }
      }
    }

    private struct Mode: Equatable {
      let isOnboarding: Bool
      let keepsPermissionGuideVisible: Bool
    }
  }
}
