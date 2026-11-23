import Foundation
import TrawlClient

public enum RestingTrawlerState: Sendable, Equatable {
  case ok
  case failed
  case skipped
  case comingSoon
}

public enum TrawlerAccessModel: Sendable, Equatable {
  case zeroFullDiskAccess
  case requiresMacFullDiskAccess

  private static let macFullDiskAccessTrawlerIDs: Set<String> = [
    "imessage",
    "notes",
    "mail",
  ]

  public static func resolve(
    for registeredTrawler: RegisteredTrawlerIdentity,
    failureCode: TrawlerFailureCode? = nil
  ) -> Self {
    if failureCode == .permission {
      return .requiresMacFullDiskAccess
    }
    if macFullDiskAccessTrawlerIDs.contains(
      registeredTrawler.registeredTrawlerIdentity.lowercased()
    ) {
      return .requiresMacFullDiskAccess
    }
    return .zeroFullDiskAccess
  }
}

public enum TrawlerHealthState: Sendable, Equatable {
  case synced
  case stale
  case empty
  case updating
  case needsFullDiskAccess
  case needsSignIn
  case needsSignedApp
  case appClosed
  case notInstalled
  case failed(String)
  case skipped(String)
  case comingSoon

  public var badgeText: String {
    switch self {
    case .synced: "Synced"
    case .stale: "Stale"
    case .empty: "Ready"
    case .updating: "Updating…"
    case .needsFullDiskAccess: "Needs FDA"
    case .needsSignIn: "Needs sign-in"
    case .needsSignedApp: "Signed app required"
    case .appClosed: "App closed"
    case .notInstalled: "Not installed"
    case .failed: "Unavailable"
    case .skipped: "Skipped"
    case .comingSoon: "Coming soon"
    }
  }

  public var requiresUserAttention: Bool {
    switch self {
    case .needsFullDiskAccess, .needsSignIn, .needsSignedApp, .appClosed, .stale, .failed, .skipped:
      true
    case .synced, .empty, .updating, .notInstalled, .comingSoon:
      false
    }
  }
}

public struct RestingTrawler: Sendable, Equatable, Identifiable {
  public let id: RegisteredTrawlerIdentity
  public let registeredTrawlerDisplayName: String
  public let commandName: String
  public let state: RestingTrawlerState
  public let healthState: TrawlerHealthState
  public let accessModel: TrawlerAccessModel
  public let detail: String?
  public let archiveContentCounts: [ArchiveContentCountAfterLastSuccessfullyCompletedUpdate]
  public let totalRecordCount: UInt64
  public let formattedRecordCountsSummary: String?
  public let compactRecordCount: String?
  public let lastUpdatedTime: Date?
  public let newestRecordTime: Date?
  public let privacyBoundary: TrawlerPrivacyBoundary?
  public let symbolName: String
  public let accentColorHex: String
  public let needsAttention: Bool

  private static let staleThresholdSeconds: TimeInterval = 48 * 60 * 60

  fileprivate init(
    status: TrawlerStatus,
    failure: TrawlerOperationFailure? = nil,
    skipped: TrawlerSkippedFromOperation? = nil,
    referenceDate: Date = Date()
  ) {
    let manifest = status.registeredTrawlerManifest
    let counts = status.archiveContentCountsAfterLastSuccessfullyCompletedUpdate
    let total = counts.reduce(UInt64(0)) { partial, item in
      partial &+ item.archiveContentCount
    }
    let summary = TrawlerRestingCopy.formattedCountsSummary(from: counts)

    id = status.id
    registeredTrawlerDisplayName = manifest.registeredTrawlerDisplayName
    commandName = manifest.registeredTrawlerCommandName
    archiveContentCounts = counts
    totalRecordCount = total
    formattedRecordCountsSummary = summary
    compactRecordCount = total > 0 ? TrawlerRestingCopy.compactCount(total) : nil
    lastUpdatedTime = status.lastSuccessfullyCompletedArchiveUpdateTime
    newestRecordTime = status.newestArchivedRecordTime
    privacyBoundary = manifest.registeredTrawlerPrivacyBoundary
    symbolName = manifest.trawlerBranding?.symbolName ?? "shippingbox.fill"
    accentColorHex = manifest.trawlerBranding?.accentColor ?? "#8E8E93"
    accessModel = TrawlerAccessModel.resolve(
      for: status.id,
      failureCode: failure?.failureCode
    )

    if let failure {
      state = .failed
      detail = failure.failureMessage
      needsAttention = true
      healthState =
        failure.failureCode == .permission
        ? .needsFullDiskAccess
        : (failure.failureCode == .authentication ? .needsSignIn : .failed(failure.failureMessage))
    } else if let skipped {
      state = .skipped
      detail = skipped.skipReason
      needsAttention = true
      healthState = .skipped(skipped.skipReason)
    } else {
      state = status.trawlerArchiveCanAnswerCurrentCommands ? .ok : .failed
      detail = TrawlerRestingCopy.detail(for: status)
      needsAttention = TrawlerRestingCopy.needsAttention(status)
      healthState = Self.resolveHealthState(
        for: status,
        totalRecordCount: total,
        referenceDate: referenceDate
      )
    }
  }

  fileprivate init(
    failure: TrawlerOperationFailure,
    manifest: RegisteredTrawlerManifest? = nil
  ) {
    id = failure.failedTrawler
    registeredTrawlerDisplayName =
      failure.registeredTrawlerDisplayName.isEmpty
      ? (manifest?.registeredTrawlerDisplayName
        ?? failure.failedTrawler.registeredTrawlerIdentity)
      : failure.registeredTrawlerDisplayName
    commandName =
      manifest?.registeredTrawlerCommandName ?? failure.failedTrawler.registeredTrawlerIdentity
    state = .failed
    healthState =
      failure.failureCode == .permission
      ? .needsFullDiskAccess
      : (failure.failureCode == .authentication ? .needsSignIn : .failed(failure.failureMessage))
    accessModel = TrawlerAccessModel.resolve(
      for: failure.failedTrawler,
      failureCode: failure.failureCode
    )
    detail = failure.failureMessage
    archiveContentCounts = []
    totalRecordCount = 0
    formattedRecordCountsSummary = nil
    compactRecordCount = nil
    lastUpdatedTime = nil
    newestRecordTime = nil
    privacyBoundary = manifest?.registeredTrawlerPrivacyBoundary
    symbolName = manifest?.trawlerBranding?.symbolName ?? "shippingbox.fill"
    accentColorHex = manifest?.trawlerBranding?.accentColor ?? "#8E8E93"
    needsAttention = true
  }

  fileprivate init(
    skipped: TrawlerSkippedFromOperation,
    manifest: RegisteredTrawlerManifest? = nil
  ) {
    id = skipped.skippedTrawler
    registeredTrawlerDisplayName =
      skipped.registeredTrawlerDisplayName.isEmpty
      ? (manifest?.registeredTrawlerDisplayName
        ?? skipped.skippedTrawler.registeredTrawlerIdentity)
      : skipped.registeredTrawlerDisplayName
    commandName =
      manifest?.registeredTrawlerCommandName ?? skipped.skippedTrawler.registeredTrawlerIdentity
    state = .skipped
    healthState = .skipped(skipped.skipReason)
    accessModel = TrawlerAccessModel.resolve(for: skipped.skippedTrawler)
    detail = skipped.skipReason
    archiveContentCounts = []
    totalRecordCount = 0
    formattedRecordCountsSummary = nil
    compactRecordCount = nil
    lastUpdatedTime = nil
    newestRecordTime = nil
    privacyBoundary = manifest?.registeredTrawlerPrivacyBoundary
    symbolName = manifest?.trawlerBranding?.symbolName ?? "shippingbox.fill"
    accentColorHex = manifest?.trawlerBranding?.accentColor ?? "#8E8E93"
    needsAttention = true
  }

  public init(comingSoon entry: RegisteredTrawlerCatalogEntry) {
    let manifest = entry.registeredTrawlerManifest
    id = entry.id
    registeredTrawlerDisplayName = manifest.registeredTrawlerDisplayName
    commandName = manifest.registeredTrawlerCommandName
    state = .comingSoon
    healthState = .comingSoon
    accessModel = TrawlerAccessModel.resolve(for: entry.id)
    detail = nil
    archiveContentCounts = []
    totalRecordCount = 0
    formattedRecordCountsSummary = nil
    compactRecordCount = nil
    lastUpdatedTime = nil
    newestRecordTime = nil
    privacyBoundary = manifest.registeredTrawlerPrivacyBoundary
    symbolName = manifest.trawlerBranding?.symbolName ?? "shippingbox.fill"
    accentColorHex = manifest.trawlerBranding?.accentColor ?? "#8E8E93"
    needsAttention = false
  }

  public init(unloadedAvailable entry: RegisteredTrawlerCatalogEntry) {
    let manifest = entry.registeredTrawlerManifest
    id = entry.id
    registeredTrawlerDisplayName = manifest.registeredTrawlerDisplayName
    commandName = manifest.registeredTrawlerCommandName
    state = .ok
    healthState = .empty
    accessModel = TrawlerAccessModel.resolve(for: entry.id)
    detail = nil
    archiveContentCounts = []
    totalRecordCount = 0
    formattedRecordCountsSummary = nil
    compactRecordCount = nil
    lastUpdatedTime = nil
    newestRecordTime = nil
    privacyBoundary = manifest.registeredTrawlerPrivacyBoundary
    symbolName = manifest.trawlerBranding?.symbolName ?? "shippingbox.fill"
    accentColorHex = manifest.trawlerBranding?.accentColor ?? "#8E8E93"
    needsAttention = false
  }

  private static func resolveHealthState(
    for status: TrawlerStatus,
    totalRecordCount: UInt64,
    referenceDate: Date
  ) -> TrawlerHealthState {
    switch status.archiveLiveSyncCredentialStatus {
    case .requiresSignedApp:
      return .needsSignedApp
    case .notConnected:
      return .needsSignIn
    case .available, .notApplicable:
      break
    }
    if status.sourceAppSyncLiveness == .sourceAppNotRunning {
      return .appClosed
    }
    if !status.trawlerArchiveCanAnswerCurrentCommands {
      return .failed("Archive unavailable")
    }
    if totalRecordCount == 0 {
      return .empty
    }
    if let lastUpdated = status.lastSuccessfullyCompletedArchiveUpdateTime,
      referenceDate.timeIntervalSince(lastUpdated) > staleThresholdSeconds
    {
      return .stale
    }
    return .synced
  }
}

public enum TrawlerRestingCopy {
  public static func trawlers(
    from statuses: [TrawlerStatus],
    failures: [TrawlerOperationFailure],
    trawlersSkippedFromOperation: [TrawlerSkippedFromOperation],
    catalog: [RegisteredTrawlerCatalogEntry] = [],
    referenceDate: Date = Date()
  ) -> [RestingTrawler] {
    let failureByTrawler = firstByTrawler(
      failures,
      registeredTrawler: \.failedTrawler)
    let skippedByTrawler = firstByTrawler(
      trawlersSkippedFromOperation,
      registeredTrawler: \.skippedTrawler)
    let manifestByTrawler = Dictionary(
      uniqueKeysWithValues: catalog.map { ($0.id, $0.registeredTrawlerManifest) }
    )
    var seen = Set<RegisteredTrawlerIdentity>()
    var trawlers = statuses.map { status in
      seen.insert(status.id)
      return RestingTrawler(
        status: status,
        failure: failureByTrawler[status.id],
        skipped: skippedByTrawler[status.id],
        referenceDate: referenceDate
      )
    }
    for failure in failures
    where seen.insert(failure.failedTrawler).inserted {
      trawlers.append(
        RestingTrawler(
          failure: failure,
          manifest: manifestByTrawler[failure.failedTrawler]
        )
      )
    }
    for skipped in trawlersSkippedFromOperation
    where seen.insert(skipped.skippedTrawler).inserted {
      trawlers.append(
        RestingTrawler(
          skipped: skipped,
          manifest: manifestByTrawler[skipped.skippedTrawler]
        )
      )
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
    let countsSummary = formattedCountsSummary(
      from: trawlerStatus.archiveContentCountsAfterLastSuccessfullyCompletedUpdate
    )
    switch trawlerStatus.archiveLiveSyncCredentialStatus {
    case .requiresSignedApp:
      return countsSummary.map { "\($0) · Sync needs signed app" } ?? "Sync needs signed app"
    case .notConnected:
      return countsSummary.map { "\($0) · Sync needs sign-in" } ?? "Sync needs sign-in"
    case .available, .notApplicable:
      break
    }
    if trawlerStatus.sourceAppSyncLiveness == .sourceAppNotRunning {
      return countsSummary.map { "\($0) · App closed" } ?? "App closed"
    }
    return countsSummary
  }

  public static func formattedCountsSummary(
    from counts: [ArchiveContentCountAfterLastSuccessfullyCompletedUpdate],
    maximumItems: Int = 2
  ) -> String? {
    let nonZero = counts.filter { $0.archiveContentCount > 0 }
    guard !nonZero.isEmpty else { return nil }
    let parts = nonZero.prefix(maximumItems).map { entry in
      let label = conciseKindDisplayName(
        displayName: entry.archiveContentKindDisplayName,
        kindName: entry.archiveContentKindName
      )
      return "\(entry.archiveContentCount.formatted()) \(label)"
    }
    return parts.joined(separator: " · ")
  }

  public static func compactCount(_ count: UInt64) -> String {
    if count < 1_000 {
      return count.formatted()
    }
    return count.formatted(
      .number.notation(.compactName).precision(.fractionLength(0...1))
    )
  }

  public static func needsAttention(_ trawlerStatus: TrawlerStatus) -> Bool {
    !trawlerStatus.trawlerArchiveCanAnswerCurrentCommands
      || trawlerStatus.sourceAppSyncLiveness == .sourceAppNotRunning
      || trawlerStatus.archiveLiveSyncCredentialStatus == .requiresSignedApp
      || trawlerStatus.archiveLiveSyncCredentialStatus == .notConnected
  }

  private static func conciseKindDisplayName(displayName: String, kindName: String) -> String {
    let trimmedDisplay = displayName.trimmingCharacters(in: .whitespacesAndNewlines)
    let trimmedKind = kindName.trimmingCharacters(in: .whitespacesAndNewlines)
    if trimmedDisplay.isEmpty {
      return trimmedKind
    }
    // Some trawlers return verbose labels such as "photos read from Apple Photos".
    // Fall back to the short kind name when it stays concise and readable.
    if trimmedDisplay.count > 22, !trimmedKind.isEmpty {
      return trimmedKind.replacingOccurrences(of: "_", with: " ")
    }
    return trimmedDisplay
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
