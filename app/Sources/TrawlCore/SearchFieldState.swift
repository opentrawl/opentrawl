import Foundation
import Observation

@MainActor
@Observable
public final class SearchFieldState {
  public let identity = UUID()
  public private(set) var focusRequest = 0

  public init() {}

  public func requestFocus() {
    focusRequest &+= 1
  }
}
