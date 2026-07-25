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

  func applicationDidFinishLaunching(_ notification: Notification) {
    NSApplication.shared.setActivationPolicy(.regular)
    Task { await model.refresh() }
  }

  func requestFullDiskAccess() {
    FullDiskAccessGuide.present(
      grantCheck: { self.model.checkDiskAccess() == .granted }
    )
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
        onboarding: OnboardingModel(
          openFullDiskAccess: delegate.requestFullDiskAccess
        ),
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
    }
  }
}
