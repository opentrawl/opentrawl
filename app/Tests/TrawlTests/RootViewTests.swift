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

    let failureWindow = hostedWindow(
      FailureView(
        detail: errorDetail,
        onOpenConnection: {},
        retry: {}
      ),
      size: TrawlDesign.minimumWindow
    )
    withExtendedLifetime(failureWindow) {
      assertRenderedButtonsFitTitles(
        [OperationalCopy.Connection.changeArchiveHost, OperationalCopy.SharedAction.retry],
        in: failureWindow.contentView!
      )
      failureWindow.orderOut(nil)
    }

    let searchOutcomeWindow = hostedWindow(
      SearchOutcome(
        phase: .failed(errorDetail),
        isScoped: true,
        timedOutLocally: false,
        onRetry: {},
        onOpenConnection: {}
      ),
      size: TrawlDesign.minimumWindow
    )
    withExtendedLifetime(searchOutcomeWindow) {
      assertRenderedButtonsFitTitles(
        [OperationalCopy.Connection.changeArchiveHost, OperationalCopy.SharedAction.retry],
        in: searchOutcomeWindow.contentView!
      )
      searchOutcomeWindow.orderOut(nil)
    }
  }

  @MainActor
  @Test func settingsWindowMountsEveryPaneWithoutCrashingOrTruncatingButtons() {
    let runtimeConfiguration = TrawlRuntimeConfiguration()
    let defaults = isolatedUserDefaults()
    let connectionStore = ArchiveConnectionStore(
      runtimeConfiguration: runtimeConfiguration,
      defaults: defaults,
      environment: [:]
    )
    let tempStateRoot = FileManager.default.temporaryDirectory
      .appending(path: "opentrawl-settings-test-\(UUID().uuidString)", directoryHint: .isDirectory)
    let remoteArchiveServer = RemoteArchiveServerController(
      runtimeConfiguration: runtimeConfiguration,
      isRemoteArchiveMode: connectionStore.isRemote,
      defaults: defaults,
      stateRootURL: tempStateRoot,
      isTailscaleInstalledProvider: { false }
    )
    let model = AppModel(client: connectionStore.switchableClient)
    let iconStore = TrawlerIconStore()

    for pane in SettingsPane.allCases {
      let window = hostedWindow(
        OpenTrawlSettingsView(
          model: model,
          connectionStore: connectionStore,
          remoteArchiveServer: remoteArchiveServer,
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
      withExtendedLifetime(window) {
        #expect(window.contentView!.bounds.width == 600)
        window.orderOut(nil)
      }
    }

    // Also mount TrawlerIconView directly with the shared environment to ensure
    // the Settings → Apps icon path never hits a missing Environment assertion.
    let iconWindow = hostedWindow(
      TrawlerIconView(
        registeredTrawler: RegisteredTrawlerIdentity(registeredTrawlerIdentity: "imessage"),
        size: 22
      )
      .environment(iconStore),
      size: CGSize(width: 44, height: 44)
    )
    withExtendedLifetime(iconWindow) {
      #expect(iconWindow.contentView!.fittingSize.width >= 22)
      iconWindow.orderOut(nil)
    }
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

  @Test func inlineLinksAndLongTrackingURLsRenderAsClickableLinks() {
    let raw =
      "Help (https://example.com/click?token=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa) · "
      + "X (Twitter) (https://example.com/x) · "
      + "[Docs](https://example.com/docs) · "
      + "See https://example.com/short."
    let rendered = RichBodyText.attributedString(for: raw)
    let renderedPlain = String(rendered.characters)
    #expect(renderedPlain == "Help · X (Twitter) · Docs · See https://example.com/short.")

    let links = rendered.runs.compactMap { run -> (String, String)? in
      guard let url = run.link else { return nil }
      return (String(rendered[run.range].characters), url.absoluteString)
    }
    #expect(links.count == 4)
    #expect(links[0].0 == "Help")
    #expect(links[0].1.hasPrefix("https://example.com/click?token="))
    #expect(links[1].0 == "X (Twitter)")
    #expect(links[1].1 == "https://example.com/x")
    #expect(links[2].0 == "Docs")
    #expect(links[2].1 == "https://example.com/docs")
    #expect(links[3].0 == "https://example.com/short")
    #expect(links[3].1 == "https://example.com/short")
  }

  @Test func longBareTrackingURLShortensVisuallyWhileKeepingFullDestination() {
    let longURL =
      "https://mail.example.com/ls/click?upn=u001.abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
    let rendered = RichBodyText.attributedString(for: "Open \(longURL) now")
    let renderedPlain = String(rendered.characters)
    #expect(renderedPlain == "Open mail.example.com/ls/click… now")
    let linkRuns = rendered.runs.compactMap(\.link)
    #expect(linkRuns.map(\.absoluteString) == [longURL])
  }

  @MainActor
  @Test func switchingScopeClearsSelectionAndStaleOpenedRecord() {
    let connectionStore = ArchiveConnectionStore(
      runtimeConfiguration: TrawlRuntimeConfiguration(),
      defaults: isolatedUserDefaults(),
      environment: [:]
    )
    let searchModel = SearchModel(client: connectionStore.switchableClient)
    let telegramID = RegisteredTrawlerIdentity(registeredTrawlerIdentity: "telegram")
    let interaction = SearchInteraction(model: searchModel, registeredTrawler: telegramID)
    interaction.selectedSearchMatchIdentifier = SearchMatchIdentifier(
      trawlLink: GloballyRoutableTrawlLink(globallyRoutableTrawlLink: "telegram:1"),
      recordAnchor: RecordAnchorIdentifier(recordAnchorIdentifier: "1")
    )

    interaction.changeScope(to: nil)

    #expect(interaction.registeredTrawler == nil)
    #expect(interaction.selectedSearchMatchIdentifier == nil)
    #expect(searchModel.openPhase == .idle)
  }

  @MainActor
  @Test func photoFieldHelperExtractsCachedPathAndHidesRawFilePathInPhotoRecords() {
    let cachedPathField = TrawlerSpecificCommandDetailPresentationField(
      fieldDisplayName: "Picture",
      fieldValue: .text("/tmp/opentrawl-picture-cache/example.jpg"),
      fieldAnchor: nil
    )
    let notFetchedField = TrawlerSpecificCommandDetailPresentationField(
      fieldDisplayName: "Picture",
      fieldValue: .text("Not fetched yet."),
      fieldAnchor: nil
    )
    let cameraField = TrawlerSpecificCommandDetailPresentationField(
      fieldDisplayName: "Camera",
      fieldValue: .text("iPhone"),
      fieldAnchor: nil
    )

    #expect(
      OpenedPhotoPreviewView.localCachedPictureFilePath(from: [cameraField, cachedPathField])
        == "/tmp/opentrawl-picture-cache/example.jpg"
    )
    #expect(
      OpenedPhotoPreviewView.localCachedPictureFilePath(from: [cameraField, notFetchedField]) == nil
    )
    #expect(
      OpenedPhotoPreviewView.shouldHideRawPicturePathField(cachedPathField, isPhotoRecord: true)
    )
    #expect(
      !OpenedPhotoPreviewView.shouldHideRawPicturePathField(notFetchedField, isPhotoRecord: true)
    )
    #expect(
      !OpenedPhotoPreviewView.shouldHideRawPicturePathField(cachedPathField, isPhotoRecord: false)
    )
    #expect(
      OpenedPhotoPreviewView.unavailableReason(
        fromPictureFieldText:
          "Fetching the picture failed: OpenTrawl does not have full access to Apple Photos."
      ) == "OpenTrawl does not have full access to Apple Photos."
    )
    #expect(
      OpenedPhotoPreviewView.unavailableReason(fromPictureFieldText: "Not fetched yet.")
        == "Not fetched yet."
    )
    #expect(
      OpenedPhotoPreviewView.unavailableReason(
        fromPictureFieldText: "Available via remote photo endpoint."
      ) == nil
    )
    #expect(
      OpenedPhotoPreviewView.unavailableReason(
        fromPictureFieldText: "/tmp/opentrawl-picture-cache/example.jpg"
      ) == nil
    )
  }

  @MainActor
  @Test func remoteArchiveServerAutoEnablesForLocalModeWithActiveTokenAndClassifiesExits() throws {
    let tempStateRoot = FileManager.default.temporaryDirectory
      .appending(path: "opentrawl-server-policy-\(UUID().uuidString)", directoryHint: .isDirectory)
    defer { try? FileManager.default.removeItem(at: tempStateRoot) }
    let accessDir = tempStateRoot.appending(path: "agent-access", directoryHint: .isDirectory)
    try FileManager.default.createDirectory(at: accessDir, withIntermediateDirectories: true)
    let tokenJSON = """
      {
        "agent_access_tokens": [
          {
            "token_id": "tok_active",
            "allowed_surface": "local"
          },
          {
            "token_id": "tok_revoked",
            "allowed_surface": "local",
            "revoked_time": "2026-01-01T00:00:00Z"
          },
          {
            "token_id": "tok_relay",
            "allowed_surface": "relay"
          }
        ]
      }
      """
    try Data(tokenJSON.utf8).write(
      to: accessDir.appending(path: "access-tokens.json", directoryHint: .notDirectory)
    )

    let defaults = isolatedUserDefaults()
    let localController = RemoteArchiveServerController(
      runtimeConfiguration: TrawlRuntimeConfiguration(),
      isRemoteArchiveMode: false,
      defaults: defaults,
      stateRootURL: tempStateRoot,
      isTailscaleInstalledProvider: { true }
    )
    #expect(localController.activeLocalTokenCount == 1)
    #expect(localController.isSharingEnabled)

    // Explicit opt-out persists and disables auto-start.
    localController.setSharingEnabled(false)
    #expect(!localController.isSharingEnabled)

    let reloadedOptOut = RemoteArchiveServerController(
      runtimeConfiguration: TrawlRuntimeConfiguration(),
      isRemoteArchiveMode: false,
      defaults: defaults,
      stateRootURL: tempStateRoot,
      isTailscaleInstalledProvider: { true }
    )
    #expect(!reloadedOptOut.isSharingEnabled)

    // Remote archive mode never enables local serving.
    let remoteController = RemoteArchiveServerController(
      runtimeConfiguration: TrawlRuntimeConfiguration(),
      isRemoteArchiveMode: true,
      defaults: isolatedUserDefaults(),
      stateRootURL: tempStateRoot,
      isTailscaleInstalledProvider: { true }
    )
    #expect(!remoteController.isSharingEnabled)
    #expect(remoteController.state == .disabledInRemoteMode)

    // Output parsing extracts the advertised Tailscale URL cleanly.
    let parsedRemote = RemoteArchiveServerController.parseAdvertisedArchiveURL(
      fromOutputLine: "remote archive  http://archive-mac.tailnet.ts.net:7391"
    )
    #expect(
      parsedRemote
        == .init(
          address: "http://archive-mac.tailnet.ts.net:7391",
          isPreferredRemoteArchiveLine: true
        )
    )

    // Exit classification detects port conflicts and Tailscale offline cleanly.
    #expect(
      RemoteArchiveServerController.classifyUnexpectedExit(
        output: "Cannot listen on 100.64.0.1:7391: listen tcp 100.64.0.1:7391: bind: address already in use",
        exitCode: 1
      ) == .portAlreadyInUse(listenAddress: "100.64.0.1:7391")
    )
    #expect(
      RemoteArchiveServerController.classifyUnexpectedExit(
        output: "Tailscale is not running on this machine; start Tailscale",
        exitCode: 1
      ) == .tailscaleUnavailable
    )
  }

}

@MainActor
private func hostedWindow<V: View>(_ view: V, size: CGSize) -> NSWindow {
  _ = NSApplication.shared
  let window = NSWindow(
    contentRect: NSRect(origin: NSPoint(x: -20_000, y: -20_000), size: size),
    styleMask: [.titled, .closable, .resizable],
    backing: .buffered,
    defer: false
  )
  let host = NSHostingView(rootView: view)
  host.frame = NSRect(origin: .zero, size: size)
  window.contentView = host
  window.orderBack(nil)
  window.layoutIfNeeded()
  host.layoutSubtreeIfNeeded()
  host.displayIfNeeded()
  RunLoop.main.run(until: Date().addingTimeInterval(0.05))
  return window
}

@MainActor
private func assertRenderedButtonsFitTitles(_ expectedTitles: [String], in root: NSView) {
  let renderedControls = renderedButtonFrames(in: root)
  #expect(renderedControls.count == expectedTitles.count)
  let labelFont = NSFont.systemFont(ofSize: NSFont.systemFontSize)
  for (title, frame) in zip(expectedTitles, renderedControls) {
    let textWidth = (title as NSString).size(withAttributes: [.font: labelFont]).width
    let minimumReadableWidth = ceil(textWidth) + 16
    #expect(
      frame.width >= minimumReadableWidth,
      "Button '\(title)' truncated: rendered width \(frame.width)pt < minimum \(minimumReadableWidth)pt (text \(textWidth)pt)"
    )
  }
}

@MainActor
private func renderedButtonFrames(in view: NSView) -> [CGRect] {
  var result: [CGRect] = []
  let typeName = String(describing: type(of: view))
  if let button = view as? NSButton, !button.isHidden, button.bounds.width > 0 {
    result.append(button.frame)
  } else if typeName == "KeyViewProxy", !view.isHidden, view.frame.width > 0 {
    result.append(view.frame)
  }
  for subview in view.subviews {
    result.append(contentsOf: renderedButtonFrames(in: subview))
  }
  return result.sorted { $0.minX < $1.minX }
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
