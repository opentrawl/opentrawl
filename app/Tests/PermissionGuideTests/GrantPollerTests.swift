import Testing

@testable import PermissionGuide

@Suite struct GrantPollerTests {
  @Test @MainActor func firesOnceCheckPasses() async {
    let poller = GrantPoller()
    var calls = 0
    await withCheckedContinuation { continuation in
      poller.start(
        interval: .milliseconds(5),
        check: {
          calls += 1
          return calls >= 3
        },
        onGranted: { continuation.resume() }
      )
    }
    #expect(calls == 3)
  }

  @Test @MainActor func stopPreventsFiring() async {
    let poller = GrantPoller()
    var granted = false
    poller.start(
      interval: .milliseconds(5),
      check: { false },
      onGranted: { granted = true }
    )
    poller.stop()
    try? await Task.sleep(for: .milliseconds(40))
    #expect(granted == false)
  }
}
