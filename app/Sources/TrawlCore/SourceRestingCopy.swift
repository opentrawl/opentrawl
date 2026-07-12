import TrawlClient

public enum SourceRestingCopy {
  public static func title(for source: SourceStatus) -> String {
    "Search \(source.manifest.surface)"
  }

  public static func detail(for source: SourceStatus) -> String? {
    if let requirement = source.setupRequirements.first(where: { $0.state == .needsAction }) {
      return requirement.explanation
    }
    if let error = source.errors.first(where: { !$0.isEmpty }) { return error }
    if let warning = source.warnings.first(where: { !$0.isEmpty }) { return warning }
    if source.state != "ok", !source.summary.isEmpty { return source.summary }
    let headlines = source.manifest.headlines.lazy.filter { !$0.isEmpty }.prefix(4)
    guard !headlines.isEmpty else { return nil }
    return headlines.joined(separator: " · ")
  }

  public static func needsAttention(_ source: SourceStatus) -> Bool {
    source.state != "ok"
      || source.setupRequirements.contains(where: { $0.state == .needsAction })
      || source.errors.contains(where: { !$0.isEmpty })
      || source.warnings.contains(where: { !$0.isEmpty })
  }
}
