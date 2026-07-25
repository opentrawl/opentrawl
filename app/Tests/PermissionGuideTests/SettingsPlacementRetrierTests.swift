import CoreGraphics
import Testing

@testable import PermissionGuide

@Suite struct SettingsPlacementRetrierTests {
  @Test @MainActor func retriesUntilTheSettingsFrameIsStable() async {
    let retrier = SettingsPlacementRetrier()
    let first = CGRect(x: 200, y: 100, width: 700, height: 500)
    let settled = CGRect(x: 240, y: 120, width: 700, height: 500)
    var readings: [CGRect?] = [nil, first, settled, settled]
    var positions: [CGRect] = []

    let foundStableFrame = await withCheckedContinuation { continuation in
      retrier.start(
        interval: .milliseconds(1),
        maximumAttempts: readings.count,
        locate: { readings.removeFirst() },
        position: { positions.append($0) },
        onFinished: { continuation.resume(returning: $0) }
      )
    }

    #expect(foundStableFrame)
    #expect(positions == [first, settled, settled])
    #expect(readings.isEmpty)
  }

  @Test @MainActor func stopsAfterTheBoundedAttemptCount() async {
    let retrier = SettingsPlacementRetrier()
    var attempts = 0

    let foundStableFrame = await withCheckedContinuation { continuation in
      retrier.start(
        interval: .milliseconds(1),
        maximumAttempts: 3,
        locate: {
          attempts += 1
          return nil
        },
        position: { _ in },
        onFinished: { continuation.resume(returning: $0) }
      )
    }

    #expect(!foundStableFrame)
    #expect(attempts == 3)
  }

  @Test @MainActor func stopCancelsPendingRetries() async {
    let retrier = SettingsPlacementRetrier()
    var attempts = 0
    retrier.start(
      interval: .seconds(1),
      maximumAttempts: 20,
      locate: {
        attempts += 1
        return nil
      },
      position: { _ in }
    )

    await Task.yield()
    retrier.stop()
    try? await Task.sleep(for: .milliseconds(10))
    #expect(attempts <= 1)
  }
}
