public enum SearchWorkspaceMode: Sendable, Equatable {
  case field
  case outcome
  case results

  public static func resolve(phase: SearchPhase, resultCount: Int) -> Self {
    switch phase {
    case .idle:
      .field
    case .loading, .complete, .partial, .failed, .timedOut:
      resultCount > 0 ? .results : .outcome
    case .skipped:
      .outcome
    }
  }
}
