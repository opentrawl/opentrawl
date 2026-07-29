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
  @Test func onboardingAndMainProductUseOneFixedWindowContract() {
    let window = NSWindow(
      contentRect: NSRect(origin: .zero, size: TrawlDesign.defaultWindow),
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
    #expect(window.contentLayoutRect.size == TrawlDesign.defaultWindow)
    #expect(!window.styleMask.contains(.resizable))
    #expect(window.contentMinSize == TrawlDesign.defaultWindow)
    #expect(window.contentMaxSize == TrawlDesign.defaultWindow)
    #expect(window.collectionBehavior.contains(.fullScreenNone))
    #expect(!window.collectionBehavior.contains(.fullScreenPrimary))
    #expect(window.tabbingMode == .disallowed)
    #expect(window.level == .normal)
    #expect(window.standardWindowButton(.zoomButton)?.isHidden == true)
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

  @Test func onboardingViewsUseSemanticTypographyOnly() throws {
    let source = try String(
      contentsOf: URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .appendingPathComponent("Sources/Trawl/OnboardingView.swift"),
      encoding: .utf8
    )

    #expect(!source.contains(".font("))
    #expect(!source.contains(".weight("))
    #expect(!source.contains(".bold("))
  }

  @MainActor
  @Test func returningLaunchUsesTheSameFixedWindow() {
    let window = NSWindow(
      contentRect: NSRect(origin: .zero, size: TrawlDesign.minimumWindow),
      styleMask: [.titled, .closable, .miniaturizable, .resizable],
      backing: .buffered,
      defer: false
    )

    WindowBehavior.Coordinator().apply(isOnboarding: false, to: window)

    #expect(window.contentLayoutRect.size == TrawlDesign.defaultWindow)
    #expect(!window.styleMask.contains(.resizable))
    #expect(window.contentMinSize == TrawlDesign.defaultWindow)
    #expect(window.contentMaxSize == TrawlDesign.defaultWindow)
  }

}

@MainActor
private func containsScrollView(in view: NSView) -> Bool {
  if view is NSScrollView { return true }
  return view.subviews.contains(where: containsScrollView)
}
