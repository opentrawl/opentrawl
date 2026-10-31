import Foundation
import OSLog
import Sparkle
import SwiftUI

@MainActor
final class UpdateController: NSObject, SPUUpdaterDelegate {
  private var controller: SPUStandardUpdaterController!
  private let archiveRefreshIsRunning: () -> Bool
  private let logger = Logger(subsystem: "org.opentrawl.trawl", category: "SoftwareUpdates")
  private var pendingInstallation: Task<Void, Never>?
  let isConfigured: Bool

  init(bundle: Bundle = .main, archiveRefreshIsRunning: @escaping () -> Bool = { false }) {
    self.archiveRefreshIsRunning = archiveRefreshIsRunning
    isConfigured =
      bundle.object(forInfoDictionaryKey: "SUFeedURL") != nil
      && bundle.object(forInfoDictionaryKey: "SUPublicEDKey") != nil
    super.init()
    controller = SPUStandardUpdaterController(
      startingUpdater: isConfigured,
      updaterDelegate: self,
      userDriverDelegate: nil
    )
    if isConfigured, controller.updater.automaticallyChecksForUpdates {
      controller.updater.checkForUpdatesInBackground()
    }
  }

  func updater(
    _ updater: SPUUpdater,
    willInstallUpdateOnQuit item: SUAppcastItem,
    immediateInstallationBlock install: @escaping () -> Void
  ) -> Bool {
    logger.notice("Software update downloaded; waiting for CLI and archive refresh to finish")
    pendingInstallation = Task { @MainActor in
      while !Task.isCancelled {
        if !archiveRefreshIsRunning(), cliIsIdle() {
          logger.notice("Installing software update and relaunching")
          UserDefaults.standard.set(true, forKey: "OpenTrawlLaunchInBackground")
          install()
          return
        }
        try? await Task.sleep(for: .seconds(2))
      }
    }
    return true
  }

  private func cliIsIdle() -> Bool {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: "/usr/bin/pgrep")
    process.arguments = ["-u", String(getuid()), "-x", "trawl"]
    process.standardOutput = FileHandle.nullDevice
    process.standardError = FileHandle.nullDevice
    do {
      try process.run()
      process.waitUntilExit()
      return process.terminationStatus == 1
    } catch {
      logger.error("Cannot check CLI activity: \(error.localizedDescription, privacy: .public)")
      return false
    }
  }

  func updater(_ updater: SPUUpdater, didAbortWithError error: Error) {
    logger.error("Software update failed: \(error.localizedDescription, privacy: .public)")
  }

  func checkForUpdates() {
    controller.checkForUpdates(nil)
  }
}

struct CheckForUpdatesCommand: View {
  let updates: UpdateController

  var body: some View {
    Button("Check for Updates…") {
      updates.checkForUpdates()
    }
    .disabled(!updates.isConfigured)
  }
}
