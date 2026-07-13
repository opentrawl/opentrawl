import TrawlClient

public enum SearchWorkspaceCopy {
  public static func usefulResults(_ count: Int) -> String {
    "Showing \(count) useful \(count == 1 ? "result" : "results")."
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
}
