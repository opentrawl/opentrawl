import Foundation

/// Polls a caller-supplied grant check until it passes, then fires once.
@MainActor
final class GrantPoller {
  private var task: Task<Void, Never>?

  /// Starts polling. `check` runs every `interval`; the first true reading
  /// stops the loop and calls `onGranted`. Calling `start` again replaces any
  /// running loop.
  func start(
    interval: Duration = .milliseconds(500),
    check: @escaping @MainActor () -> Bool,
    onGranted: @escaping @MainActor () -> Void
  ) {
    stop()
    task = Task { @MainActor in
      while !Task.isCancelled {
        if check() {
          onGranted()
          return
        }
        try? await Task.sleep(for: interval)
      }
    }
  }

  /// Stops polling. Safe to call more than once.
  func stop() {
    task?.cancel()
    task = nil
  }

  deinit {
    task?.cancel()
  }
}
