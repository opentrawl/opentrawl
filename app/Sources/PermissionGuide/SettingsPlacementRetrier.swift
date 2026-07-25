import CoreGraphics
import Foundation

/// Waits briefly for System Settings to publish a usable window frame. It
/// repositions on every reading and stops after two consecutive stable frames.
@MainActor
final class SettingsPlacementRetrier {
  private var task: Task<Void, Never>?

  func start(
    interval: Duration = .milliseconds(250),
    maximumAttempts: Int = 20,
    locate: @escaping @MainActor () -> CGRect?,
    position: @escaping @MainActor (CGRect) -> Void,
    onFinished: @escaping @MainActor (Bool) -> Void = { _ in }
  ) {
    stop()
    guard maximumAttempts > 0 else {
      onFinished(false)
      return
    }

    task = Task { @MainActor [weak self] in
      var previousBounds: CGRect?

      for attempt in 0..<maximumAttempts {
        guard !Task.isCancelled else { return }

        if let bounds = locate() {
          position(bounds)
          if let previousBounds, bounds.isApproximatelyEqual(to: previousBounds) {
            self?.task = nil
            onFinished(true)
            return
          }
          previousBounds = bounds
        }

        guard attempt < maximumAttempts - 1 else { break }
        do {
          try await Task.sleep(for: interval)
        } catch {
          return
        }
      }

      self?.task = nil
      onFinished(false)
    }
  }

  func stop() {
    task?.cancel()
    task = nil
  }

  deinit {
    task?.cancel()
  }
}

extension CGRect {
  fileprivate func isApproximatelyEqual(to other: CGRect, tolerance: CGFloat = 1) -> Bool {
    abs(minX - other.minX) <= tolerance
      && abs(minY - other.minY) <= tolerance
      && abs(width - other.width) <= tolerance
      && abs(height - other.height) <= tolerance
  }
}
