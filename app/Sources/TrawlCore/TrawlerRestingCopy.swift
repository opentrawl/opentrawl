import TrawlClient

public struct RestingTrawler: Sendable, Equatable, Identifiable {
  public let id: String
  public let registeredTrawlerDisplayName: String
  public let state: String
  public let detail: String?
  public let needsAttention: Bool

  fileprivate init(
    status: TrawlerStatus,
    failure: TrawlerOperationFailure? = nil,
    skipped: TrawlerSkippedFromOperation? = nil
  ) {
    id = status.id
    registeredTrawlerDisplayName =
      status.registeredTrawlerManifest.registeredTrawlerDisplayName
    if let failure {
      state = "failed"
      detail = failure.failureMessage
      needsAttention = true
    } else if let skipped {
      state = "skipped"
      detail = skipped.skipReason
      needsAttention = true
    } else {
      state = status.trawlerArchiveCanAnswerCurrentCommands ? "ok" : "failed"
      detail = TrawlerRestingCopy.detail(for: status)
      needsAttention = TrawlerRestingCopy.needsAttention(status)
    }
  }

  fileprivate init(failure: TrawlerOperationFailure) {
    id = failure.registeredTrawlerManifestIdentity
    registeredTrawlerDisplayName =
      failure.registeredTrawlerDisplayName.isEmpty
      ? failure.registeredTrawlerManifestIdentity
      : failure.registeredTrawlerDisplayName
    state = "failed"
    detail = failure.failureMessage
    needsAttention = true
  }

  fileprivate init(skipped: TrawlerSkippedFromOperation) {
    id = skipped.registeredTrawlerManifestIdentity
    registeredTrawlerDisplayName =
      skipped.registeredTrawlerDisplayName.isEmpty
      ? skipped.registeredTrawlerManifestIdentity
      : skipped.registeredTrawlerDisplayName
    state = "skipped"
    detail = skipped.skipReason
    needsAttention = true
  }

  public init(comingSoon entry: RegisteredTrawlerCatalogEntry) {
    id = entry.id
    registeredTrawlerDisplayName =
      entry.registeredTrawlerManifest.registeredTrawlerDisplayName
    state = "comingSoon"
    detail = nil
    needsAttention = false
  }
}

public enum TrawlerRestingCopy {
  public static func trawlers(
    from statuses: [TrawlerStatus],
    failures: [TrawlerOperationFailure],
    trawlersSkippedFromOperation: [TrawlerSkippedFromOperation]
  ) -> [RestingTrawler] {
    let failureByTrawler = firstByTrawler(
      failures,
      registeredTrawlerManifestIdentity: \.registeredTrawlerManifestIdentity)
    let skippedByTrawler = firstByTrawler(
      trawlersSkippedFromOperation,
      registeredTrawlerManifestIdentity: \.registeredTrawlerManifestIdentity)
    var seen = Set<String>()
    var trawlers = statuses.map { status in
      seen.insert(status.id)
      return RestingTrawler(
        status: status,
        failure: failureByTrawler[status.id],
        skipped: skippedByTrawler[status.id]
      )
    }
    for failure in failures
    where seen.insert(failure.registeredTrawlerManifestIdentity).inserted {
      trawlers.append(RestingTrawler(failure: failure))
    }
    for skipped in trawlersSkippedFromOperation
    where seen.insert(skipped.registeredTrawlerManifestIdentity).inserted {
      trawlers.append(RestingTrawler(skipped: skipped))
    }
    return trawlers
  }

  public static func title(for trawler: RestingTrawler) -> String {
    trawler.state == "comingSoon"
      ? trawler.registeredTrawlerDisplayName
      : "Search \(trawler.registeredTrawlerDisplayName)"
  }

  public static func title(for trawlerStatus: TrawlerStatus) -> String {
    "Search \(trawlerStatus.registeredTrawlerManifest.registeredTrawlerDisplayName)"
  }

  public static func detail(for trawlerStatus: TrawlerStatus) -> String? {
    let commandNames = trawlerStatus.registeredTrawlerManifest
      .trawlerCommandNamesShownInBareTrawlOverview.lazy
      .filter { !$0.isEmpty }
      .prefix(4)
    guard !commandNames.isEmpty else { return nil }
    return commandNames.joined(separator: " · ")
  }

  public static func needsAttention(_ trawlerStatus: TrawlerStatus) -> Bool {
    !trawlerStatus.trawlerArchiveCanAnswerCurrentCommands
  }

  private static func firstByTrawler<Value>(
    _ values: [Value],
    registeredTrawlerManifestIdentity: KeyPath<Value, String>
  ) -> [String: Value] {
    values.reduce(into: [:]) { result, value in
      let id = value[keyPath: registeredTrawlerManifestIdentity]
      if result[id] == nil { result[id] = value }
    }
  }
}
