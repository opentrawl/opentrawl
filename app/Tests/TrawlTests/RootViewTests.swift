import AppKit
import PermissionGuide
import SwiftUI
import Testing

@testable import Trawl
@testable import TrawlClient
@testable import TrawlCore

@Suite(.serialized)
struct RootViewTests {
  @MainActor
  @Test func onboardingStaysFixedAndMainWindowIsResizable() {
    let window = NSWindow(
      contentRect: NSRect(origin: .zero, size: TrawlDesign.onboardingWindow),
      styleMask: [.titled, .closable, .miniaturizable, .resizable],
      backing: .buffered,
      defer: false
    )
    window.collectionBehavior = [.fullScreenPrimary]
    window.tabbingMode = .preferred
    let coordinator = WindowBehavior.Coordinator()
    coordinator.apply(isOnboarding: true, to: window)
    #expect(window.contentLayoutRect.size == TrawlDesign.onboardingWindow)
    #expect(!window.styleMask.contains(.resizable))
    #expect(window.contentMinSize == TrawlDesign.onboardingWindow)
    #expect(window.contentMaxSize == TrawlDesign.onboardingWindow)
    #expect(window.collectionBehavior.contains(.fullScreenNone))
    #expect(!window.collectionBehavior.contains(.fullScreenPrimary))
    #expect(window.tabbingMode == .disallowed)
    #expect(window.standardWindowButton(.zoomButton)?.isHidden == true)
    #expect(window.titleVisibility == .hidden)
    #expect(window.titlebarAppearsTransparent)
    if let toolbarButton = window.standardWindowButton(.toolbarButton) {
      #expect(toolbarButton.isHidden)
    }
    #expect(window.level == .normal)

    coordinator.apply(isOnboarding: false, to: window)
    #expect(window.styleMask.contains(.resizable))
    #expect(window.contentMinSize == TrawlDesign.minimumWindow)
    #expect(window.contentMaxSize.width > TrawlDesign.defaultWindow.width)
    #expect(window.contentMaxSize.height > TrawlDesign.defaultWindow.height)
    #expect(window.collectionBehavior.contains(.fullScreenPrimary))
    #expect(!window.collectionBehavior.contains(.fullScreenNone))
    #expect(window.tabbingMode == .disallowed)
    #expect(window.level == .normal)
    #expect(window.standardWindowButton(.zoomButton)?.isHidden == false)
    #expect(window.titleVisibility == .visible)
    #expect(!window.titlebarAppearsTransparent)
  }

  @MainActor
  @Test func onboardingScaffoldDoesNotCreateAHiddenScrollView() {
    let host = NSHostingView(
      rootView: TrawlFlowScaffold(page: .welcome) {
        Color.clear
      } actions: {
        EmptyView()
      }
      .frame(
        width: TrawlDesign.onboardingWindow.width,
        height: TrawlDesign.onboardingWindow.height
      )
    )
    host.frame.size = TrawlDesign.onboardingWindow
    host.layoutSubtreeIfNeeded()

    #expect(!containsScrollView(in: host))
  }

  @MainActor
  @Test func returningLaunchUsesResizableWindowWithMinimum720By680() {
    let window = NSWindow(
      contentRect: NSRect(origin: .zero, size: TrawlDesign.minimumWindow),
      styleMask: [.titled, .closable, .miniaturizable],
      backing: .buffered,
      defer: false
    )

    WindowBehavior.Coordinator().apply(isOnboarding: false, to: window)

    #expect(window.styleMask.contains(.resizable))
    #expect(window.contentMinSize == TrawlDesign.minimumWindow)
    #expect(window.contentLayoutRect.size.width >= TrawlDesign.minimumWindow.width)
    #expect(window.contentLayoutRect.size.height >= TrawlDesign.minimumWindow.height)
  }

}

@MainActor
private func containsScrollView(in view: NSView) -> Bool {
  if view is NSScrollView { return true }
  return view.subviews.contains(where: containsScrollView)
}
