import AppKit
import Observation
import PermissionGuide
import SwiftUI
import TrawlClient
import TrawlCore

@MainActor
private let launchInBackground = ProcessInfo.processInfo.arguments.contains("--background")
  || UserDefaults.standard.bool(forKey: "OpenTrawlLaunchInBackground")

@MainActor
@Observable
final class AppShellState {
  static let keepRunningInMenuBarKey = "OpenTrawlKeepRunningInMenuBar"

  var selectedSettingsPane: SettingsPane = .general
  var isShowingXSignInFromSettings = false
  var keepRunningInMenuBar: Bool = {
    if UserDefaults.standard.object(forKey: keepRunningInMenuBarKey) == nil {
      return true
    }
    return UserDefaults.standard.bool(forKey: keepRunningInMenuBarKey)
  }() {
    didSet {
      UserDefaults.standard.set(keepRunningInMenuBar, forKey: Self.keepRunningInMenuBarKey)
      if !keepRunningInMenuBar {
        NSApplication.shared.setActivationPolicy(.regular)
      }
    }
  }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
  private var archiveRefreshTask: Task<Void, Never>?
  private var automaticRefreshTrawlers: [RegisteredTrawlerIdentity] = []
  private var windowCloseObserver: NSObjectProtocol?
  let shellState = AppShellState()
  let appInstallations = MacAppInstallations()
  let trawlerIconStore = TrawlerIconStore()
  let runtimeConfiguration = TrawlRuntimeConfiguration()
  lazy var connectionStore = ArchiveConnectionStore(runtimeConfiguration: runtimeConfiguration)
  lazy var remoteArchiveServer = RemoteArchiveServerController(
    runtimeConfiguration: runtimeConfiguration,
    isRemoteArchiveMode: connectionStore.isRemote
  )
  lazy var client: any TrawlClient = connectionStore.switchableClient
  lazy var model = AppModel(
    client: client,
    prepareSourceAppsForUpdate: { manifests in
      await MacSourceAppLauncher.launchClosedSourceAppsHidden(for: manifests)
    }
  )
  lazy var updates = UpdateController(archiveRefreshIsRunning: { [weak self] in
    self?.model.isUpdating ?? false
  })
  lazy var onboarding = OnboardingModel(openFullDiskAccess: requestFullDiskAccess)

  func applicationDidFinishLaunching(_ notification: Notification) {
    NSApplication.shared.setActivationPolicy(launchInBackground ? .accessory : .regular)
    UserDefaults.standard.removeObject(forKey: "OpenTrawlLaunchInBackground")
    BrowserXSessionImporter.cleanupStaleTemporaryCopies()
    _ = updates
    observeAutomaticArchiveRefresh()
    observeRemoteArchiveServerMode()
    observeWindowCloseForMenuBarMode()
    Task { await model.refresh() }
  }

  func applicationWillTerminate(_ notification: Notification) {
    remoteArchiveServer.stopSynchronouslyForTermination()
  }

  private func observeRemoteArchiveServerMode() {
    let isRemote = withObservationTracking {
      connectionStore.isRemote
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observeRemoteArchiveServerMode() }
    }
    remoteArchiveServer.updateRemoteArchiveMode(isRemote)
  }

  private func observeWindowCloseForMenuBarMode() {
    windowCloseObserver = NotificationCenter.default.addObserver(
      forName: NSWindow.willCloseNotification,
      object: nil,
      queue: .main
    ) { [weak self] notification in
      let closingWindow = notification.object as? NSWindow
      Task { @MainActor [weak self] in
        guard let self, self.shellState.keepRunningInMenuBar else { return }
        let remainingStandardWindows = NSApplication.shared.windows.filter { window in
          window !== closingWindow
            && window.isVisible
            && !window.isMiniaturized
            && window.level == .normal
            && window.canBecomeMain
        }
        if remainingStandardWindows.isEmpty {
          NSApplication.shared.setActivationPolicy(.accessory)
        }
      }
    }
  }

  private func observeAutomaticArchiveRefresh() {
    let registeredTrawlers = withObservationTracking {
      if connectionStore.isRemote {
        return [RegisteredTrawlerIdentity]()
      }
      appInstallations.refresh(registeredTrawlerCatalog: model.registeredTrawlerCatalog)
      guard onboarding.isComplete else { return [RegisteredTrawlerIdentity]() }
      let candidates = AppFeatureFlags.current().trawlersToUpdate(
        reportedTrawlers: model.updateCandidateTrawlers,
        unavailableTrawlers: appInstallations.unavailableTrawlers
      )
      if model.fullDiskAccessChoice == .leftOut {
        return candidates.filter {
          !AppOperabilityState.fullDiskAccessTrawlerIDs.contains($0.registeredTrawlerIdentity)
        }
      }
      return candidates
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observeAutomaticArchiveRefresh() }
    }
    guard registeredTrawlers != automaticRefreshTrawlers else { return }
    automaticRefreshTrawlers = registeredTrawlers
    archiveRefreshTask?.cancel()
    guard !registeredTrawlers.isEmpty else {
      archiveRefreshTask = nil
      return
    }
    archiveRefreshTask = Task {
      await model.runAutomaticUpdateLoop(registeredTrawlers: registeredTrawlers)
    }
  }

  func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
    sender.setActivationPolicy(.regular)
    return true
  }

  func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
    !shellState.keepRunningInMenuBar
  }

  func requestFullDiskAccess() {
    FullDiskAccessGuide.present(
      grantCheck: { self.model.checkDiskAccess() == .granted },
      completion: { [weak self] _ in self?.onboarding.permissionGuideDidClose() }
    )
  }

  func triggerManualArchiveUpdate() {
    guard !connectionStore.isRemote, !model.isUpdating else { return }
    appInstallations.refresh(registeredTrawlerCatalog: model.registeredTrawlerCatalog)
    var candidates = AppFeatureFlags.current().trawlersToUpdate(
      reportedTrawlers: model.updateCandidateTrawlers,
      unavailableTrawlers: appInstallations.unavailableTrawlers
    )
    if model.fullDiskAccessChoice == .leftOut {
      candidates = candidates.filter {
        !AppOperabilityState.fullDiskAccessTrawlerIDs.contains($0.registeredTrawlerIdentity)
      }
    }
    guard !candidates.isEmpty else { return }
    Task {
      await model.updateNow(registeredTrawlers: candidates)
    }
  }

  func signInGmail() {
    Task {
      do {
        try await TrawlerAccountConnector.connectGmail(configuration: runtimeConfiguration)
        await model.refresh()
        await model.updateNow(
          registeredTrawlers: [RegisteredTrawlerIdentity(registeredTrawlerIdentity: "gmail")]
        )
      } catch {
        // Error surfaced in status / next refresh
      }
    }
  }

  func signOutGmail() {
    Task {
      do {
        try await TrawlerAccountConnector.disconnectGmail(configuration: runtimeConfiguration)
        await model.refresh()
      } catch {
        // Error surfaced on next refresh
      }
    }
  }

  func signOutX() {
    Task {
      do {
        try await TrawlerAccountConnector.disconnectTwitter(configuration: runtimeConfiguration)
        await model.refresh()
      } catch {
        // Error surfaced on next refresh
      }
    }
  }

  func openSourceApp(bundleIdentifier: String) {
    guard let appURL = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleIdentifier) else {
      return
    }
    let config = NSWorkspace.OpenConfiguration()
    config.activates = true
    NSWorkspace.shared.openApplication(at: appURL, configuration: config)
  }

  /// Debug tool: clear onboarding state, remove this build's Full Disk Access
  /// entry, and relaunch so the app starts clean on the onboarding flow.
  func restartOnboarding() {
    onboarding.reset()
    let tccBundleIdentifier = Bundle.main.bundleIdentifier ?? "org.opentrawl.trawl"
    let tccutil = Process()
    tccutil.executableURL = URL(fileURLWithPath: "/usr/bin/tccutil")
    tccutil.arguments = ["reset", "SystemPolicyAllFiles", tccBundleIdentifier]
    do {
      try tccutil.run()
      tccutil.waitUntilExit()
    } catch {
      // Best effort: the onboarding reset and relaunch proceed regardless.
    }
    let relaunch = Process()
    relaunch.executableURL = URL(fileURLWithPath: "/usr/bin/open")
    relaunch.arguments = ["-n", Bundle.main.bundleURL.path]
    try? relaunch.run()
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
      NSApplication.shared.terminate(nil)
    }
  }
}

struct TrawlApp: App {
  @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
  @Environment(\.openWindow) private var openWindow

  var body: some Scene {
    Window("OpenTrawl", id: "main") {
      RootView(
        model: delegate.model,
        client: delegate.client,
        connectionStore: delegate.connectionStore,
        runtimeConfiguration: delegate.runtimeConfiguration,
        onboarding: delegate.onboarding,
        appInstallations: delegate.appInstallations,
        trawlerIconStore: delegate.trawlerIconStore,
        aiInstruction: AgentPrompts.connectAI,
        openFullDiskAccess: delegate.requestFullDiskAccess,
        onOpenSettingsPane: { pane in
          delegate.shellState.selectedSettingsPane = pane
        }
      )
      .environment(delegate.trawlerIconStore)
      .frame(
        minWidth: TrawlDesign.minimumWindow.width,
        minHeight: TrawlDesign.minimumWindow.height
      )
    }
    .defaultSize(
      width: TrawlDesign.defaultWindow.width,
      height: TrawlDesign.defaultWindow.height
    )
    .defaultLaunchBehavior(launchInBackground ? .suppressed : .presented)
    .restorationBehavior(.disabled)
    .windowResizability(.contentMinSize)
    .commands {
      CommandGroup(after: .appInfo) {
        CheckForUpdatesCommand(updates: delegate.updates)
      }
      CommandGroup(replacing: .newItem) {}
      CommandGroup(after: .help) {
        Divider()
        Button("Debug: Restart Onboarding") {
          delegate.restartOnboarding()
        }
      }
    }

    Settings {
      OpenTrawlSettingsView(
        model: delegate.model,
        connectionStore: delegate.connectionStore,
        remoteArchiveServer: delegate.remoteArchiveServer,
        runtimeConfiguration: delegate.runtimeConfiguration,
        appInstallations: delegate.appInstallations,
        buildIdentity: .current,
        aiInstruction: AgentPrompts.connectAI,
        selectedPane: Binding(
          get: { delegate.shellState.selectedSettingsPane },
          set: { delegate.shellState.selectedSettingsPane = $0 }
        ),
        keepRunningInMenuBar: Binding(
          get: { delegate.shellState.keepRunningInMenuBar },
          set: { delegate.shellState.keepRunningInMenuBar = $0 }
        ),
        onConnectionChanged: {
          delegate.model.clearForConnectionSwitch()
          Task { await delegate.model.refresh() }
        },
        onOpenFullDiskAccess: {
          delegate.model.setFullDiskAccessChoice(.included)
          delegate.requestFullDiskAccess()
        },
        onSignInGmail: delegate.signInGmail,
        onSignOutGmail: delegate.signOutGmail,
        onSignInX: { delegate.shellState.isShowingXSignInFromSettings = true },
        onSignOutX: delegate.signOutX,
        onOpenSourceApp: delegate.openSourceApp(bundleIdentifier:)
      )
      .environment(delegate.trawlerIconStore)
      .sheet(
        isPresented: Binding(
          get: { delegate.shellState.isShowingXSignInFromSettings },
          set: { delegate.shellState.isShowingXSignInFromSettings = $0 }
        )
      ) {
        XWebSignInSheet(
          runtimeConfiguration: delegate.runtimeConfiguration,
          onConnected: {
            Task {
              await delegate.model.refresh()
              await delegate.model.updateNow(
                registeredTrawlers: [RegisteredTrawlerIdentity(registeredTrawlerIdentity: "twitter")]
              )
            }
          },
          onDismiss: {
            delegate.shellState.isShowingXSignInFromSettings = false
          }
        )
        .environment(delegate.trawlerIconStore)
      }
    }

    MenuBarExtra {
      OpenTrawlMenuBarContent(
        delegate: delegate,
        onOpenWindow: {
          NSApplication.shared.setActivationPolicy(.regular)
          NSApplication.shared.activate(ignoringOtherApps: true)
          openWindow(id: "main")
        }
      )
    } label: {
      OpenTrawlMenuBarLabel()
    }
  }
}

private struct OpenTrawlMenuBarLabel: View {
  /// Vector template rendering of the OpenTrawl faceted diamond-net mark
  /// (`assets/brand/src/mark-small.svg`), drawn crisply at menu-bar scale so
  /// macOS renders it sharp at 1x/2x and tints it automatically for light and
  /// dark menu bars.
  private static let templateImage: NSImage = {
    let size = NSSize(width: 18, height: 18)
    let image = NSImage(size: size, flipped: true) { rect in
      // Map the 1024x1024 mark-small.svg coordinate space into 18x18 with a
      // 1pt optical margin so strokes and vertex nodes stay razor sharp.
      let pad: CGFloat = 1.2
      let scale = (min(rect.width, rect.height) - pad * 2) / 1024.0
      let offsetX = (rect.width - 1024.0 * scale) / 2.0
      let offsetY = (rect.height - 1024.0 * scale) / 2.0
      func pt(_ x: CGFloat, _ y: CGFloat) -> NSPoint {
        NSPoint(x: offsetX + x * scale, y: offsetY + y * scale)
      }

      NSColor.black.setStroke()
      NSColor.black.setFill()

      let strokePath = NSBezierPath()
      strokePath.lineWidth = 1.35
      strokePath.lineCapStyle = .round
      strokePath.lineJoinStyle = .round

      // Outer gem silhouette
      strokePath.move(to: pt(292, 196))
      strokePath.line(to: pt(732, 196))
      strokePath.line(to: pt(968, 428))
      strokePath.line(to: pt(512, 952))
      strokePath.line(to: pt(56, 428))
      strokePath.close()

      // Horizontal facet line
      strokePath.move(to: pt(56, 428))
      strokePath.line(to: pt(968, 428))

      // Interior net ribs
      strokePath.move(to: pt(292, 196))
      strokePath.line(to: pt(270, 428))
      strokePath.line(to: pt(512, 952))

      strokePath.move(to: pt(732, 196))
      strokePath.line(to: pt(754, 428))
      strokePath.line(to: pt(512, 952))

      strokePath.stroke()

      // Five vertex nodes
      let nodeRadius: CGFloat = 1.45
      for (x, y) in [(292.0, 196.0), (732.0, 196.0), (56.0, 428.0), (968.0, 428.0), (512.0, 952.0)] {
        let center = pt(x, y)
        let dotRect = NSRect(
          x: center.x - nodeRadius,
          y: center.y - nodeRadius,
          width: nodeRadius * 2,
          height: nodeRadius * 2
        )
        NSBezierPath(ovalIn: dotRect).fill()
      }

      return true
    }
    image.isTemplate = true
    return image
  }()

  var body: some View {
    Image(nsImage: Self.templateImage)
      .accessibilityLabel("OpenTrawl")
  }
}

private struct OpenTrawlMenuBarContent: View {
  let delegate: AppDelegate
  let onOpenWindow: () -> Void

  private var statusLine: String {
    if delegate.connectionStore.isRemote {
      return "Connected to \(delegate.connectionStore.activeHostShortDisplayName)"
    }
    if delegate.model.isUpdating {
      return "Updating your archive…"
    }
    let total = delegate.model.trawlerStatuses.reduce(UInt64(0)) { sum, status in
      sum + status.archiveContentCountsAfterLastSuccessfullyCompletedUpdate.reduce(UInt64(0)) {
        $0 + $1.archiveContentCount
      }
    }
    if total > 0 {
      return "\(total.formatted()) items searchable"
    }
    return "OpenTrawl"
  }

  var body: some View {
    Text(statusLine)
    Divider()
    Button("Open OpenTrawl", action: onOpenWindow)
    if !delegate.connectionStore.isRemote {
      Button(
        delegate.model.isUpdating ? "Updating your archive…" : HumanCopy.Home.updateArchiveAction
      ) {
        delegate.triggerManualArchiveUpdate()
      }
      .disabled(delegate.model.isUpdating)
    }
    SettingsLink {
      Text("Settings…")
    }
    Divider()
    Button("Quit OpenTrawl") {
      NSApplication.shared.terminate(nil)
    }
    .keyboardShortcut("q")
  }
}
