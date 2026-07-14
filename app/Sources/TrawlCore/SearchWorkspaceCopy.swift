import TrawlClient

public enum SearchWorkspaceCopy {
  public static func usefulResults(_ count: Int) -> String {
    "Showing \(count) useful \(count == 1 ? "result" : "results")."
  }

  public static func partialNoMatches(failureGuidance: String?, isScoped: Bool) -> String {
    guard !isScoped else {
      return failureGuidance ?? "Some sources failed; the others returned no matches."
    }
    guard let failureGuidance else {
      return "No matches in available sources. Some sources failed."
    }
    return "No matches in available sources. \(failureGuidance)"
  }

  public static func skippedOutcome(for sources: [SkippedSource]) -> String {
    guard let first = sources.first else { return "A source was skipped." }
    let source = first.surface.isEmpty ? first.sourceID : first.surface
    let remaining = sources.count - 1
    guard remaining > 0 else { return "\(source): \(first.reason)" }
    let noun = remaining == 1 ? "source" : "sources"
    let verb = remaining == 1 ? "was" : "were"
    return "\(source): \(first.reason) \(remaining) more \(noun) \(verb) skipped."
  }

  public static func outcomeTitle(for phase: SearchPhase) -> String {
    switch phase {
    case .complete, .partial:
      "No matches"
    case .skipped, .failed:
      "Search unavailable"
    case .timedOut:
      "Search timed out"
    case .idle, .loading:
      "Search"
    }
  }

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

  public static func outcomeDetail(
    for phase: SearchPhase,
    failureGuidance: String?,
    skippedSources: [SkippedSource],
    isScoped: Bool,
    timeoutSeconds: Int
  ) -> String {
    switch phase {
    case .complete:
      isScoped ? "No matches in this source." : "No matches in available sources."
    case .partial:
      partialNoMatches(failureGuidance: failureGuidance, isScoped: isScoped)
    case .skipped:
      skippedOutcome(for: skippedSources)
    case .failed(let message):
      message
    case .timedOut:
      timedOutOutcome(after: timeoutSeconds)
    case .idle, .loading:
      ""
    }
  }

  public static func timedOutOutcome(after seconds: Int) -> String {
    "Search stopped after \(seconds) seconds."
  }
}
