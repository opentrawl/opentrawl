import TrawlClient

public enum RestingTrawlerState: Sendable, Equatable {
  case ok
  case failed
  case skipped
  case comingSoon
}

public struct RestingTrawler: Sendable, Equatable, Identifiable {
  public let id: RegisteredTrawlerIdentity
  public let registeredTrawlerDisplayName: String
  public let state: RestingTrawlerState
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
      state = .failed
      detail = failure.failureMessage
      needsAttention = true
    } else if let skipped {
      state = .skipped
      detail = skipped.skipReason
      needsAttention = true
    } else {
      state = status.trawlerArchiveCanAnswerCurrentCommands ? .ok : .failed
      detail = TrawlerRestingCopy.detail(for: status)
      needsAttention = TrawlerRestingCopy.needsAttention(status)
    }
  }

  fileprivate init(failure: TrawlerOperationFailure) {
    id = failure.failedTrawler
    registeredTrawlerDisplayName =
      failure.registeredTrawlerDisplayName.isEmpty
      ? failure.failedTrawler.registeredTrawlerIdentity
      : failure.registeredTrawlerDisplayName
    state = .failed
    detail = failure.failureMessage
    needsAttention = true
  }

  fileprivate init(skipped: TrawlerSkippedFromOperation) {
    id = skipped.skippedTrawler
    registeredTrawlerDisplayName =
      skipped.registeredTrawlerDisplayName.isEmpty
      ? skipped.skippedTrawler.registeredTrawlerIdentity
      : skipped.registeredTrawlerDisplayName
    state = .skipped
    detail = skipped.skipReason
    needsAttention = true
  }

  public init(comingSoon entry: RegisteredTrawlerCatalogEntry) {
    id = entry.id
    registeredTrawlerDisplayName =
      entry.registeredTrawlerManifest.registeredTrawlerDisplayName
    state = .comingSoon
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
      registeredTrawler: \.failedTrawler)
    let skippedByTrawler = firstByTrawler(
      trawlersSkippedFromOperation,
      registeredTrawler: \.skippedTrawler)
    var seen = Set<RegisteredTrawlerIdentity>()
    var trawlers = statuses.map { status in
      seen.insert(status.id)
      return RestingTrawler(
        status: status,
        failure: failureByTrawler[status.id],
        skipped: skippedByTrawler[status.id]
      )
    }
    for failure in failures
    where seen.insert(failure.failedTrawler).inserted {
      trawlers.append(RestingTrawler(failure: failure))
    }
    for skipped in trawlersSkippedFromOperation
    where seen.insert(skipped.skippedTrawler).inserted {
      trawlers.append(RestingTrawler(skipped: skipped))
    }
    return trawlers
  }

  public static func title(for trawler: RestingTrawler) -> String {
    trawler.state == .comingSoon
      ? trawler.registeredTrawlerDisplayName
      : "Search \(trawler.registeredTrawlerDisplayName)"
  }

  public static func title(for trawlerStatus: TrawlerStatus) -> String {
    "Search \(trawlerStatus.registeredTrawlerManifest.registeredTrawlerDisplayName)"
  }

  public static func detail(for trawlerStatus: TrawlerStatus) -> String? {
    switch trawlerStatus.archiveLiveSyncCredentialStatus {
    case .requiresSignedApp:
      return "Sync needs signed app"
    case .notConnected:
      return "Sync needs sign-in"
    case .available, .notApplicable:
      break
    }
    if trawlerStatus.sourceAppSyncLiveness == .sourceAppNotRunning {
      return "App closed"
    }
    return primaryArchiveCountSummary(for: trawlerStatus)
  }

  public static func primaryArchiveCountSummary(for trawlerStatus: TrawlerStatus) -> String? {
    let counts = trawlerStatus.archiveContentCountsAfterLastSuccessfullyCompletedUpdate
    guard let primary = counts.first(where: { $0.archiveContentCount > 0 }) else {
      return nil
    }
    let displayUnit = primary.archiveContentKindDisplayName
      .trimmingCharacters(in: .whitespacesAndNewlines)
    let fallbackKind = primary.archiveContentKindName
      .trimmingCharacters(in: .whitespacesAndNewlines)
    let fallbackUnit = fallbackKind == "apple_library_photos" ? "photos" : fallbackKind
    let unit = displayUnit.isEmpty ? fallbackUnit : displayUnit
    let formattedCount = compactRecordCount(primary.archiveContentCount)
    guard !unit.isEmpty else { return formattedCount }
    return "\(formattedCount) \(unit)"
  }

  public static func compactRecordCount(_ count: UInt64) -> String {
    switch count {
    case 1_000_000...:
      let millions = Double(count) / 1_000_000
      return String(format: "%.1fM", millions)
    case 10_000...:
      let thousands = Double(count) / 1_000
      return String(format: "%.1fK", thousands)
    default:
      return count.formatted()
    }
  }

  public static func needsAttention(_ trawlerStatus: TrawlerStatus) -> Bool {
    !trawlerStatus.trawlerArchiveCanAnswerCurrentCommands
      || trawlerStatus.sourceAppSyncLiveness == .sourceAppNotRunning
      || trawlerStatus.archiveLiveSyncCredentialStatus == .requiresSignedApp
      || trawlerStatus.archiveLiveSyncCredentialStatus == .notConnected
  }

  private static func firstByTrawler<Value>(
    _ values: [Value],
    registeredTrawler: KeyPath<Value, RegisteredTrawlerIdentity>
  ) -> [RegisteredTrawlerIdentity: Value] {
    values.reduce(into: [:]) { result, value in
      let id = value[keyPath: registeredTrawler]
      if result[id] == nil { result[id] = value }
    }
  }
}
