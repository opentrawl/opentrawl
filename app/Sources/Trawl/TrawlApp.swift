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
  private let permissionGuide = PermissionGuideController()

  func applicationDidFinishLaunching(_ notification: Notification) {
    NSApplication.shared.setActivationPolicy(.regular)
    Task { await model.refresh() }
  }

  func requestFullDiskAccess() {
    permissionGuide.present { [weak self] in
      guard let self else { return }
      Task { await self.model.permissionChanged() }
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
        agentInstruction: OnboardingStrings.agentInstruction(
          helperCommand: delegate.runtimeConfiguration.agentCommand
        )
      )
      .frame(
        minWidth: TrawlDesign.minimumWindow.width,
        idealWidth: TrawlDesign.defaultWindow.width,
        minHeight: TrawlDesign.minimumWindow.height,
        idealHeight: TrawlDesign.defaultWindow.height
      )
    }
    .defaultSize(
      width: TrawlDesign.defaultWindow.width,
      height: TrawlDesign.defaultWindow.height
    )
    .windowResizability(.contentMinSize)
    .commands {
      CommandGroup(after: .appInfo) {
        CheckForUpdatesCommand(updates: updates)
      }
      CommandGroup(replacing: .newItem) {}
    }
  }
}
