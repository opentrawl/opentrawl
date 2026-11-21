import AppKit
import OSLog
import TrawlClient

@MainActor
enum MacSourceAppLauncher {
  private static let logger = Logger(subsystem: "org.opentrawl.trawl", category: "SourceAppLauncher")
  private static let hideSweepIntervals: [Duration] = [.seconds(1), .seconds(2), .seconds(2)]

  /// Launches closed macOS source apps hidden and without activation so their
  /// local stores sync before an archive update without disturbing the user.
  /// Already-running apps are left untouched.
  static func launchClosedSourceAppsHidden(
    for manifests: [RegisteredTrawlerManifest]
  ) async {
    var seenBundleIdentifiers = Set<String>()
    var launchedApplications: [NSRunningApplication] = []

    for manifest in manifests {
      guard manifest.sourceFreshnessDependency == .sourceAppRunning,
        let bundleIdentifier = manifest.trawlerBranding?.bundleIdentifier,
        !bundleIdentifier.isEmpty,
        seenBundleIdentifiers.insert(bundleIdentifier).inserted
      else {
        continue
      }
      let alreadyRunning = NSRunningApplication.runningApplications(
        withBundleIdentifier: bundleIdentifier
      ).contains { !$0.isTerminated }
      if alreadyRunning {
        continue
      }
      guard let applicationURL = NSWorkspace.shared.urlForApplication(
        withBundleIdentifier: bundleIdentifier
      ) else {
        continue
      }
      let openConfiguration = NSWorkspace.OpenConfiguration()
      openConfiguration.activates = false
      openConfiguration.hides = true
      openConfiguration.addsToRecentItems = false
      do {
        let runningApplication = try await NSWorkspace.shared.openApplication(
          at: applicationURL,
          configuration: openConfiguration
        )
        if !runningApplication.isActive {
          _ = runningApplication.hide()
        }
        launchedApplications.append(runningApplication)
        logger.notice(
          "Launched closed source app hidden: \(bundleIdentifier, privacy: .public)"
        )
      } catch {
        logger.error(
          "Could not launch source app \(bundleIdentifier, privacy: .public): \(error.localizedDescription, privacy: .public)"
        )
      }
    }

    guard !launchedApplications.isEmpty else { return }
    // Give newly launched apps a short window to sync their local stores while
    // re-hiding any app that tries to unhide during applicationDidFinishLaunching.
    for interval in hideSweepIntervals {
      do {
        try await Task.sleep(for: interval)
      } catch {
        return
      }
      for runningApplication in launchedApplications where !runningApplication.isTerminated {
        if !runningApplication.isActive {
          _ = runningApplication.hide()
        }
      }
    }
  }
}
