import Sparkle
import SwiftUI

@MainActor
final class UpdateController {
  private let controller: SPUStandardUpdaterController

  init(startingUpdater: Bool = true) {
    controller = SPUStandardUpdaterController(
      startingUpdater: startingUpdater,
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
  }
}
