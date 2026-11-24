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

  @MainActor
  @Test func failureAndSearchOutcomeButtonsDoNotTruncate() {
    let errorDetail = TrawlClientError.remoteServerUnreachable(
      "Could not connect to the server."
    ).localizedDescription

    let failureHost = hostedView(
      FailureView(
        detail: errorDetail,
        onOpenConnection: {},
        retry: {}
      ),
      size: TrawlDesign.minimumWindow
    )
    assertNoTruncatedButtons(in: failureHost, expectedMinimumButtonCount: 2)

    let searchOutcomeHost = hostedView(
      SearchOutcome(
        phase: .failed(errorDetail),
        isScoped: true,
        timedOutLocally: false,
        onRetry: {},
        onOpenConnection: {}
      ),
      size: TrawlDesign.minimumWindow
    )
    assertNoTruncatedButtons(in: searchOutcomeHost, expectedMinimumButtonCount: 2)
  }

  @MainActor
  @Test func settingsWindowMountsEveryPaneWithoutCrashingOrTruncatingButtons() {
    let runtimeConfiguration = TrawlRuntimeConfiguration()
    let connectionStore = ArchiveConnectionStore(
      runtimeConfiguration: runtimeConfiguration,
      defaults: isolatedUserDefaults(),
      environment: [:]
    )
    let model = AppModel(client: connectionStore.switchableClient)
    let iconStore = TrawlerIconStore()

    for pane in SettingsPane.allCases {
      let host = hostedView(
        OpenTrawlSettingsView(
          model: model,
          connectionStore: connectionStore,
          runtimeConfiguration: runtimeConfiguration,
          appInstallations: MacAppInstallations(),
          buildIdentity: BuildIdentity(version: "0.1.0", gitCommit: nil, isDevelopmentBuild: true),
          aiInstruction: AgentPrompts.connectAI,
          selectedPane: .constant(pane),
          keepRunningInMenuBar: .constant(true),
          onConnectionChanged: {},
          onOpenFullDiskAccess: {},
          onSignInGmail: {},
          onSignOutGmail: {},
          onSignInX: {},
          onSignOutX: {},
          onOpenSourceApp: { _ in }
        )
        .environment(iconStore),
        size: CGSize(width: 600, height: 500)
      )
      assertNoTruncatedButtons(in: host, expectedMinimumButtonCount: 0)
    }

    // Also mount TrawlerIconView directly with the shared environment to ensure
    // the Settings → Apps icon path never hits a missing Environment assertion.
    let iconHost = hostedView(
      TrawlerIconView(
        registeredTrawler: RegisteredTrawlerIdentity(registeredTrawlerIdentity: "imessage"),
        size: 22
      )
      .environment(iconStore),
      size: CGSize(width: 44, height: 44)
    )
    #expect(iconHost.fittingSize.width >= 22)
  }

  @MainActor
  @Test func missingSavedRemoteAccessTokenStaysRemoteAndFailsLoudly() async {
    let defaults = isolatedUserDefaults()
    defaults.set("remote", forKey: "OpenTrawlArchiveConnectionMode")
    defaults.set("http://archive-mac.tailnet.ts.net:7391", forKey: "OpenTrawlRemoteArchiveURL")

    let emptyTokenDir = FileManager.default.temporaryDirectory
      .appending(path: "opentrawl-missing-token-\(UUID().uuidString)", directoryHint: .isDirectory)
    defer { try? FileManager.default.removeItem(at: emptyTokenDir) }

    let store = ArchiveConnectionStore(
      runtimeConfiguration: TrawlRuntimeConfiguration(),
      defaults: defaults,
      environment: [:],
      accessTokenStorage: .privateFiles(directory: emptyTokenDir)
    )

    #expect(store.isRemote)
    #expect(!store.hasSavedBearerToken)
    #expect(store.activeHostShortDisplayName == "archive-mac")

    do {
      _ = try await store.switchableClient.status()
      Issue.record("expected status() to fail loudly when the saved remote access token is missing")
    } catch let error as TrawlClientError {
      #expect(error == .remoteSavedAccessTokenMissing(hostDisplayName: "archive-mac"))
    } catch {
      Issue.record("unexpected error: \(error)")
    }
  }

  @Test func matchedWordsAreHighlightedInsideOpenedMessageBubbles() {
    let segments = HighlightedMessageText.segments(
      in: "Meet at the cafe tomorrow, Cafe Central is open.",
      highlighting: ["cafe"]
    )
    #expect(
      segments == [
        .init(text: "Meet at the ", isMatch: false),
        .init(text: "cafe", isMatch: true),
        .init(text: " tomorrow, ", isMatch: false),
        .init(text: "Cafe", isMatch: true),
        .init(text: " Central is open.", isMatch: false),
      ]
    )
  }

}

@MainActor
private func hostedView<V: View>(_ view: V, size: CGSize) -> NSHostingView<V> {
  let window = NSWindow(
    contentRect: NSRect(origin: .zero, size: size),
    styleMask: [.titled, .closable, .resizable],
    backing: .buffered,
    defer: false
  )
  let host = NSHostingView(rootView: view)
  host.frame = NSRect(origin: .zero, size: size)
  window.contentView = host
  window.layoutIfNeeded()
  host.layoutSubtreeIfNeeded()
  return host
}

@MainActor
private func assertNoTruncatedButtons(in root: NSView, expectedMinimumButtonCount: Int) {
  let buttons = allButtons(in: root).filter { !$0.isHidden && !$0.title.isEmpty }
  #expect(buttons.count >= expectedMinimumButtonCount)
  for button in buttons {
    let requiredWidth = button.fittingSize.width
    let actualWidth = button.bounds.width
    #expect(
      actualWidth >= requiredWidth - 1.0,
      "Button '\(button.title)' truncated: actual width \(actualWidth)pt < fitting width \(requiredWidth)pt"
    )
  }
}

@MainActor
private func allButtons(in view: NSView) -> [NSButton] {
  var result: [NSButton] = []
  if let button = view as? NSButton {
    result.append(button)
  }
  for subview in view.subviews {
    result.append(contentsOf: allButtons(in: subview))
  }
  return result
}

private func isolatedUserDefaults() -> UserDefaults {
  let suiteName = "org.opentrawl.tests.\(UUID().uuidString)"
  let defaults = UserDefaults(suiteName: suiteName)!
  defaults.removePersistentDomain(forName: suiteName)
  return defaults
}

@MainActor
private func containsScrollView(in view: NSView) -> Bool {
  if view is NSScrollView { return true }
  return view.subviews.contains(where: containsScrollView)
}
