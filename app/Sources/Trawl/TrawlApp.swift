import AppKit
import PermissionGuide
import SwiftUI
import TrawlClient
import TrawlCore

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
  let client: any TrawlClient = ProcessTrawlClient()
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

  var body: some Scene {
    Window("OpenTrawl", id: "main") {
      RootView(
        model: delegate.model,
        client: delegate.client,
        onRequestDiskAccess: delegate.requestFullDiskAccess
      )
      .preferredColorScheme(.light)
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
      CommandGroup(replacing: .newItem) {}
    }
  }
}
