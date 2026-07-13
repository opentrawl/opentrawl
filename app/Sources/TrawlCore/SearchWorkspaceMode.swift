public enum SearchWorkspaceMode: Sendable, Equatable {
  case field
  case outcome
  case results

  public static func resolve(phase: SearchPhase, resultCount: Int) -> Self {
    switch phase {
    case .idle:
      .field
    case .complete, .partial:
      resultCount > 0 ? .results : .outcome
    case .loading, .skipped, .failed, .timedOut:
      .outcome
    }
  }
}
