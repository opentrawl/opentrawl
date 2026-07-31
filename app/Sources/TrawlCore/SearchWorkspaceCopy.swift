import TrawlClient

public enum SearchWorkspaceCopy {
  public static func usefulResults(_ count: Int) -> String {
    "Showing \(count) useful \(count == 1 ? "result" : "results")."
  }

  public static func partialNoMatches(failureGuidance: String?, isScoped: Bool) -> String {
    guard !isScoped else {
      return failureGuidance ?? "Some apps failed; the others returned no matches."
    }
    return failureGuidance ?? "Some apps could not be searched."
  }

  public static func skippedOutcome(
    for trawlersSkippedFromOperation: [TrawlerSkippedFromOperation]
  ) -> String {
    guard let first = trawlersSkippedFromOperation.first else {
      return "An app was skipped."
    }
    let appName =
      first.registeredTrawlerDisplayName.isEmpty
      ? first.skippedTrawler.registeredTrawlerIdentity
      : first.registeredTrawlerDisplayName
    let remaining = trawlersSkippedFromOperation.count - 1
    guard remaining > 0 else { return "\(appName): \(first.skipReason)" }
    let noun = remaining == 1 ? "app" : "apps"
    let verb = remaining == 1 ? "was" : "were"
    return "\(appName): \(first.skipReason) \(remaining) more \(noun) \(verb) skipped."
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
    trawlersSkippedFromOperation: [TrawlerSkippedFromOperation],
    isScoped: Bool,
    timedOutLocally: Bool = true,
    timeoutSeconds: Int
  ) -> String {
    switch phase {
    case .complete:
      ""
    case .partial:
      partialNoMatches(failureGuidance: failureGuidance, isScoped: isScoped)
    case .skipped:
      skippedOutcome(for: trawlersSkippedFromOperation)
    case .failed(let message):
      message
    case .timedOut:
      timedOutLocally
        ? timedOutOutcome(after: timeoutSeconds)
        : (failureGuidance ?? "An app timed out.")
    case .idle, .loading:
      ""
    }
  }

  public static func timedOutOutcome(after seconds: Int) -> String {
    "Search stopped after \(seconds) seconds."
  }
}
