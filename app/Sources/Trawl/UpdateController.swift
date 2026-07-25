import Foundation
import Sparkle
import SwiftUI

@MainActor
final class UpdateController {
  private let controller: SPUStandardUpdaterController
  let isConfigured: Bool

  init(bundle: Bundle = .main) {
    isConfigured =
      bundle.object(forInfoDictionaryKey: "SUFeedURL") != nil
      && bundle.object(forInfoDictionaryKey: "SUPublicEDKey") != nil
    controller = SPUStandardUpdaterController(
      startingUpdater: isConfigured,
      updaterDelegate: nil,
      userDriverDelegate: nil
    )
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
