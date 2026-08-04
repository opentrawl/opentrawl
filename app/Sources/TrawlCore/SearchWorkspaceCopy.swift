public enum SearchWorkspaceCopy {
  public static func outcomeSymbol(for phase: SearchPhase) -> String {
    switch phase {
    case .complete:
      "magnifyingglass"
    case .partial, .skipped:
      "exclamationmark.triangle"
    case .failed:
      "exclamationmark.circle"
    case .timedOut:
      "clock.badge.exclamationmark"
    case .idle, .loading:
      "magnifyingglass"
    }
  }

}
