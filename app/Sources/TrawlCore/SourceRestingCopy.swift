import TrawlClient

public struct RestingSource: Sendable, Equatable, Identifiable {
  public let id: String
  public let surface: String
  public let state: String
  public let databaseBytes: Int64
  public let detail: String?
  public let needsAttention: Bool

  fileprivate init(status: SourceStatus, failure: SourceFailure? = nil, skipped: SkippedSource? = nil) {
    id = status.id
    surface = status.manifest.displayName
    databaseBytes = status.databaseBytes
    if let failure {
      state = "failed"
      detail = failure.message
      needsAttention = true
    } else if let skipped {
      state = "skipped"
      detail = skipped.reason
      needsAttention = true
    } else {
      state = status.state
      detail = SourceRestingCopy.detail(for: status)
      needsAttention = SourceRestingCopy.needsAttention(status)
    }
  }

  fileprivate init(failure: SourceFailure) {
    id = failure.sourceID
    surface = failure.sourceName.isEmpty ? failure.sourceID : failure.sourceName
    state = "failed"
    databaseBytes = 0
    detail = failure.message
    needsAttention = true
  }

  fileprivate init(skipped: SkippedSource) {
    id = skipped.sourceID
    surface = skipped.surface.isEmpty ? skipped.sourceID : skipped.surface
    state = "skipped"
    databaseBytes = 0
    detail = skipped.reason
    needsAttention = true
  }
}

public enum SourceRestingCopy {
  public static func sources(
    from statuses: [SourceStatus],
    failures: [SourceFailure],
    skippedSources: [SkippedSource]
  ) -> [RestingSource] {
    let failureBySource = firstBySource(failures, sourceID: \.sourceID)
    let skippedBySource = firstBySource(skippedSources, sourceID: \.sourceID)
    var seen = Set<String>()
    var sources = statuses.map { status in
      seen.insert(status.id)
      return RestingSource(
        status: status,
        failure: failureBySource[status.id],
        skipped: skippedBySource[status.id]
      )
    }
    for failure in failures where seen.insert(failure.sourceID).inserted {
      sources.append(RestingSource(failure: failure))
    }
    for skipped in skippedSources where seen.insert(skipped.sourceID).inserted {
      sources.append(RestingSource(skipped: skipped))
    }
    return sources
  }

  public static func title(for source: RestingSource) -> String {
    "Search \(source.surface)"
  }

  public static func title(for source: SourceStatus) -> String {
    "Search \(source.manifest.displayName)"
  }

  public static func detail(for source: SourceStatus) -> String? {
    if let requirement = source.setupRequirements.first(where: { $0.state == .needsAction }) {
      return requirement.explanation
    }
    if let error = source.errors.first(where: { !$0.isEmpty }) { return error }
    if let warning = source.warnings.first(where: { !$0.isEmpty }) { return warning }
    switch source.state {
    case "missing":
      return "Not set up."
    default:
      break
    }
    if source.state != "ok", source.state != "stale", !source.summary.isEmpty {
      return source.summary
    }
    let headlines = source.manifest.headlines.lazy.filter { !$0.isEmpty }.prefix(4)
    guard !headlines.isEmpty else { return nil }
    return headlines.joined(separator: " · ")
  }

  public static func needsAttention(_ source: SourceStatus) -> Bool {
    (source.state != "ok" && source.state != "stale")
      || source.setupRequirements.contains(where: { $0.state == .needsAction })
      || source.errors.contains(where: { !$0.isEmpty })
      || source.warnings.contains(where: { !$0.isEmpty })
  }

  private static func firstBySource<Value>(
    _ values: [Value],
    sourceID: KeyPath<Value, String>
  ) -> [String: Value] {
    values.reduce(into: [:]) { result, value in
      let id = value[keyPath: sourceID]
      if result[id] == nil { result[id] = value }
    }
  }
}
