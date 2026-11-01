import AppKit
import PermissionGuide
import Observation
import SwiftUI
import TrawlClient
import TrawlCore

@MainActor
private let launchInBackground = ProcessInfo.processInfo.arguments.contains("--background")
  || UserDefaults.standard.bool(forKey: "OpenTrawlLaunchInBackground")

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
  private var archiveRefreshTask: Task<Void, Never>?
  private var automaticRefreshTrawlers: [RegisteredTrawlerIdentity] = []
  private let appInstallations = MacAppInstallations()
  let runtimeConfiguration = TrawlRuntimeConfiguration()
  lazy var client: any TrawlClient = ProcessTrawlClient(configuration: runtimeConfiguration)
  lazy var model = AppModel(client: client)
  lazy var updates = UpdateController(archiveRefreshIsRunning: { [weak self] in
    self?.model.isUpdating ?? false
  })
  lazy var onboarding = OnboardingModel(openFullDiskAccess: requestFullDiskAccess)

  func applicationDidFinishLaunching(_ notification: Notification) {
    NSApplication.shared.setActivationPolicy(launchInBackground ? .accessory : .regular)
    UserDefaults.standard.removeObject(forKey: "OpenTrawlLaunchInBackground")
    _ = updates
    observeAutomaticArchiveRefresh()
    Task { await model.refresh() }
  }

  private func observeAutomaticArchiveRefresh() {
    let registeredTrawlers = withObservationTracking {
      appInstallations.refresh(registeredTrawlerCatalog: model.registeredTrawlerCatalog)
      return onboarding.isComplete ? AppFeatureFlags.current().trawlersToUpdate(
        reportedTrawlers: model.updateCandidateTrawlers,
        unavailableTrawlers: appInstallations.unavailableTrawlers
      ) : []
    } onChange: { [weak self] in
      Task { @MainActor [weak self] in self?.observeAutomaticArchiveRefresh() }
    }
    guard registeredTrawlers != automaticRefreshTrawlers else { return }
    automaticRefreshTrawlers = registeredTrawlers
    archiveRefreshTask?.cancel()
    archiveRefreshTask = Task {
      await model.runAutomaticUpdateLoop(registeredTrawlers: registeredTrawlers)
    }
  }

  func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
    sender.setActivationPolicy(.regular)
    return true
  }

  func requestFullDiskAccess() {
    FullDiskAccessGuide.present(
      grantCheck: { self.model.checkDiskAccess() == .granted },
      completion: { [weak self] _ in self?.onboarding.permissionGuideDidClose() }
    )
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

@main
struct TrawlApp: App {
  @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate

  var body: some Scene {
    Window("OpenTrawl", id: "main") {
      RootView(
        model: delegate.model,
        client: delegate.client,
        onboarding: delegate.onboarding,
        aiInstruction: AgentPrompts.connectAI,
        openFullDiskAccess: delegate.requestFullDiskAccess
      )
      .frame(
        width: TrawlDesign.defaultWindow.width,
        height: TrawlDesign.defaultWindow.height
      )
    }
    .defaultSize(
      width: TrawlDesign.defaultWindow.width,
      height: TrawlDesign.defaultWindow.height
    )
    .defaultLaunchBehavior(launchInBackground ? .suppressed : .presented)
    .restorationBehavior(.disabled)
    .windowResizability(.contentSize)
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
  }
}
