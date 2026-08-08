import AppKit
import PermissionGuide
import SwiftUI
import TrawlClient
import TrawlCore

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
  let runtimeConfiguration = TrawlRuntimeConfiguration()
  lazy var client: any TrawlClient = ProcessTrawlClient(configuration: runtimeConfiguration)
  lazy var model = AppModel(client: client)
  lazy var onboarding = OnboardingModel(openFullDiskAccess: requestFullDiskAccess)

  func applicationDidFinishLaunching(_ notification: Notification) {
    NSApplication.shared.setActivationPolicy(.regular)
    Task { await model.refresh() }
  }

  func requestFullDiskAccess() {
    FullDiskAccessGuide.present(
      grantCheck: { self.model.checkDiskAccess() == .granted }
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
  private let updates = UpdateController()

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
    .defaultLaunchBehavior(.presented)
    .restorationBehavior(.disabled)
    .windowResizability(.contentSize)
    .commands {
      CommandGroup(after: .appInfo) {
        CheckForUpdatesCommand(updates: updates)
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
