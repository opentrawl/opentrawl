import Foundation
import Observation
import PermissionGuide
import TrawlClient

public enum HomePhase: Sendable, Equatable {
  case loading
  case ready
  case partial
  case timedOut
  case failed(String)
}

public enum AppSyncProgressState: Sendable, Equatable {
  case waiting
  case building
  case finalising
  case finished
  case failed(String)
}

public enum SyncTrigger: Sendable, Equatable {
  case manual
  case automatic
}

@MainActor
@Observable
public final class AppModel {
  private let client: any TrawlClient
  private let permissionProbe: FullDiskAccessProbe
  private let automaticSyncBaseDelay: Duration
  private let automaticSyncSleep: @Sendable (Duration) async throws -> Void

  public private(set) var phase: HomePhase = .loading
  public private(set) var trawlerStatuses: [TrawlerStatus] = []
  public private(set) var registeredTrawlerCatalog: [RegisteredTrawlerCatalogEntry] = []
  public private(set) var statusOperationFailures: [TrawlerOperationFailure] = []
  public private(set) var trawlersSkippedFromStatus: [TrawlerSkippedFromOperation] = []
  public private(set) var statusOperationOutcome: OperationOutcome = .complete
  public private(set) var statusRefreshFailure: String?
  public private(set) var isSyncing = false
  public private(set) var syncMessage: String?
  public private(set) var trawlerArchiveSyncResults: [TrawlerArchiveSyncResult] = []
  public private(set) var syncOperationFailures: [TrawlerOperationFailure] = []
  public private(set) var syncProgress: [String: AppSyncProgressState] = [:]
  public private(set) var diskAccess: FullDiskAccessStatus = .undetermined
  private var automaticSyncFailureCounts: [String: Int] = [:]

  public var restingTrawlers: [RestingTrawler] {
    let runtimeTrawlers = TrawlerRestingCopy.trawlers(
      from: trawlerStatuses,
      failures: statusOperationFailures,
      trawlersSkippedFromOperation: trawlersSkippedFromStatus
    )
    let byID = Dictionary(uniqueKeysWithValues: runtimeTrawlers.map { ($0.id, $0) })
    return displayedAppIDs.compactMap { byID[$0] }
  }

  public var homeTrawlers: [RestingTrawler] {
    let runtimeByID = Dictionary(uniqueKeysWithValues: restingTrawlers.map { ($0.id, $0) })
    return displayedAppIDs.compactMap { appID in
      if let runtime = runtimeByID[appID] { return runtime }
      guard
        let entry = catalogEntry(for: appID),
        entry.registeredTrawlerReleaseState == .comingSoon
      else {
        return nil
      }
      return RestingTrawler(comingSoon: entry)
    }
  }

  public var displayedAppIDs: [String] {
    registeredTrawlerCatalog.map(\.id)
  }

  public var syncCandidateAppIDs: [String] {
    registeredTrawlerCatalog.compactMap { entry in
      entry.registeredTrawlerReleaseState == .available
        && entry.registeredTrawlerIsEnabled ? entry.id : nil
    }
  }

  public func catalogEntry(for appID: String) -> RegisteredTrawlerCatalogEntry? {
    registeredTrawlerCatalog.first { $0.id == appID }
  }

  public func manifest(for appID: String) -> RegisteredTrawlerManifest? {
    catalogEntry(for: appID)?.registeredTrawlerManifest
  }

  public var shouldShowFailureFallback: Bool {
    blockingFailureMessage != nil
  }

  public var needsFullDiskAccessRecovery: Bool {
    if diskAccess == .denied { return true }
    return diskAccess != .granted
      && (statusOperationFailures + syncOperationFailures).contains {
        $0.failureCode == .permission
      }
  }

  public var fullDiskAccessAppIDs: [String] {
    (statusOperationFailures.filter { $0.failureCode == .permission }
      .map(\.registeredTrawlerManifestIdentity)
      + syncOperationFailures.filter { $0.failureCode == .permission }
        .map(\.registeredTrawlerManifestIdentity))
      .reduce(into: []) { appIDs, appID in
        if !appIDs.contains(appID) { appIDs.append(appID) }
      }
  }

  public var blockingFailureMessage: String? {
    guard restingTrawlers.isEmpty else { return nil }
    switch phase {
    case .failed(let message):
      return message
    case .timedOut:
      return statusRefreshFailure ?? "Trawler status checks timed out."
    case .loading, .ready, .partial:
      return nil
    }
  }

  public init(
    client: any TrawlClient,
    permissionProbe: FullDiskAccessProbe = FullDiskAccessProbe(),
    automaticSyncBaseDelay: Duration = .seconds(3_600),
    automaticSyncSleep: @escaping @Sendable (Duration) async throws -> Void = {
      try await Task.sleep(for: $0)
    }
  ) {
    self.client = client
    self.permissionProbe = permissionProbe
    self.automaticSyncBaseDelay = automaticSyncBaseDelay
    self.automaticSyncSleep = automaticSyncSleep
  }

  public func refresh() async {
    if trawlerStatuses.isEmpty {
      phase = .loading
    }
    do {
      let response = try await client.status()
      applyStatus(response)
    } catch is CancellationError {
      return
    } catch TrawlClientError.cancelled {
      return
    } catch TrawlClientError.timedOut {
      statusRefreshFailure = "Trawler status checks timed out."
      phase = .timedOut
    } catch {
      let message = error.localizedDescription
      statusRefreshFailure = message
      phase = .failed(message)
    }
  }

  private func applyStatus(_ response: StatusResponse) {
    trawlerStatuses = response.trawlerStatuses
    registeredTrawlerCatalog = response.registeredTrawlerCatalog
    statusOperationFailures = response.operationFailures
    trawlersSkippedFromStatus = response.trawlersSkippedFromOperation
    statusOperationOutcome = response.outcome
    statusRefreshFailure = nil
    if response.outcome == .failed, !response.operationFailures.isEmpty,
      response.operationFailures.allSatisfy({ $0.failureCode == .timeout })
    {
      phase = .timedOut
    } else if response.outcome == .failed {
      phase = .failed(
        response.operationFailures.first?.failureMessage
          ?? "No trawler status check succeeded.")
    } else if response.outcome == .partial {
      phase = .partial
    } else {
      phase = .ready
    }
  }

  private func orderedUniqueAppIDs(_ values: [String]) -> [String] {
    values.reduce(into: []) { result, appID in
      if !result.contains(appID) { result.append(appID) }
    }
  }

  public func automaticSyncFailureCount(for appID: String) -> Int {
    automaticSyncFailureCounts[appID, default: 0]
  }

  public func automaticSyncDelay(for appID: String) -> Duration {
    let multiplier = min(8, 1 << min(automaticSyncFailureCount(for: appID), 3))
    return automaticSyncBaseDelay * multiplier
  }

  public func syncNow(appIDs: [String] = [], trigger: SyncTrigger = .manual) async {
    guard !isSyncing else { return }
    if checkDiskAccess() == .denied {
      syncMessage = "Full Disk Access is required."
      return
    }
    isSyncing = true
    let previousSyncMessage = syncMessage
    let previousSyncResults = trawlerArchiveSyncResults
    let previousSyncFailures = syncOperationFailures
    let previousSyncProgress = syncProgress
    let requestedAppIDs = appIDs.isEmpty ? trawlerStatuses.map(\.id) : appIDs
    let requestedSet = Set(requestedAppIDs)
    syncMessage = nil
    if appIDs.isEmpty {
      trawlerArchiveSyncResults = []
      syncOperationFailures = []
      syncProgress = [:]
    } else {
      trawlerArchiveSyncResults.removeAll {
        requestedSet.contains($0.registeredTrawlerManifestIdentity)
      }
      syncOperationFailures.removeAll {
        requestedSet.contains($0.registeredTrawlerManifestIdentity)
      }
      for appID in requestedAppIDs { syncProgress.removeValue(forKey: appID) }
    }
    for appID in requestedAppIDs { syncProgress[appID] = .waiting }
    defer { isSyncing = false }

    do {
      let result = try await syncWithProgress(appIDs: requestedAppIDs)
      trawlerArchiveSyncResults = mergeSyncResults(
        trawlerArchiveSyncResults,
        replacing: result.trawlerArchiveSyncResults)
      syncOperationFailures = mergeFailures(
        syncOperationFailures,
        replacing: result.operationFailures)
      for trawlerArchiveSyncResult in result.trawlerArchiveSyncResults {
        syncProgress[trawlerArchiveSyncResult.registeredTrawlerManifestIdentity] =
          progressState(for: trawlerArchiveSyncResult)
      }
      if let collision = result.operationFailures.first(where: {
        $0.failureCode == .alreadySyncing
      }) {
        syncMessage = collision.failureMessage
        trawlerArchiveSyncResults = previousSyncResults
        syncOperationFailures = previousSyncFailures
        syncProgress = previousSyncProgress
        return
      }
      switch result.outcome {
      case .complete:
        recordAutomaticSync(success: true, appIDs: requestedAppIDs, trigger: trigger)
        break
      case .partial:
        syncMessage = "Some apps could not sync."
        recordAutomaticSync(success: false, appIDs: requestedAppIDs, trigger: trigger)
      case .failed:
        syncMessage = "No app could sync."
        recordAutomaticSync(success: false, appIDs: requestedAppIDs, trigger: trigger)
      }
      if result.operationFailures.contains(where: { $0.failureCode == .permission }) {
        checkDiskAccess()
      }
      await refresh()
    } catch is CancellationError {
      syncMessage = previousSyncMessage
      trawlerArchiveSyncResults = previousSyncResults
      syncOperationFailures = previousSyncFailures
      syncProgress = previousSyncProgress
      return
    } catch TrawlClientError.cancelled {
      syncMessage = previousSyncMessage
      trawlerArchiveSyncResults = previousSyncResults
      syncOperationFailures = previousSyncFailures
      syncProgress = previousSyncProgress
      return
    } catch {
      syncMessage = error.localizedDescription
      recordAutomaticSync(success: false, appIDs: requestedAppIDs, trigger: trigger)
      for registeredTrawlerManifestIdentity in requestedAppIDs {
        switch syncProgress[registeredTrawlerManifestIdentity] {
        case .waiting, .building, .finalising:
          syncProgress[registeredTrawlerManifestIdentity] =
            .failed(error.localizedDescription)
        case .finished, .failed, .none:
          break
        }
      }
    }
  }

  public func runAutomaticSyncLoop(appIDs: [String]) async {
    let appIDs = appIDs.reduce(into: [String]()) { result, appID in
      if !result.contains(appID) { result.append(appID) }
    }
    guard !appIDs.isEmpty else { return }
    var remaining = Dictionary(
      uniqueKeysWithValues: appIDs.map { ($0, automaticSyncDelay(for: $0)) }
    )

    while !Task.isCancelled {
      guard let nextDelay = remaining.values.min() else { return }
      do {
        try await automaticSyncSleep(nextDelay)
      } catch {
        return
      }
      guard !Task.isCancelled else { return }
      for appID in appIDs {
        remaining[appID] = (remaining[appID] ?? nextDelay) - nextDelay
      }
      let dueAppIDs = appIDs.filter { remaining[$0] == .zero }
      for appID in dueAppIDs {
        guard !Task.isCancelled else { return }
        await syncNow(appIDs: [appID], trigger: .automatic)
        remaining[appID] = automaticSyncDelay(for: appID)
      }
    }
  }

  private func recordAutomaticSync(
    success: Bool,
    appIDs: [String],
    trigger: SyncTrigger
  ) {
    guard trigger == .automatic else { return }
    for appID in appIDs {
      if success {
        automaticSyncFailureCounts[appID] = 0
      } else {
        automaticSyncFailureCounts[appID, default: 0] += 1
      }
    }
  }

  private func syncWithProgress(appIDs: [String]) async throws -> SyncResponse {
    let client = self.client
    let (events, continuation) = AsyncStream<SyncProgress>.makeStream()
    let task = Task<SyncResponse, Error> {
      defer { continuation.finish() }
      return try await client.sync(
        registeredTrawlerManifestIdentities: appIDs
      ) { event in
        continuation.yield(event)
      }
    }
    return try await withTaskCancellationHandler {
      for await event in events {
        applySyncProgress(event)
      }
      return try await task.value
    } onCancel: {
      task.cancel()
    }
  }

  private func applySyncProgress(_ progress: SyncProgress) {
    switch progress {
    case .building(let registeredTrawlerManifestIdentity):
      syncProgress[registeredTrawlerManifestIdentity] = .building
    case .finalising(let registeredTrawlerManifestIdentity):
      syncProgress[registeredTrawlerManifestIdentity] = .finalising
    }
  }

  private func progressState(for result: TrawlerArchiveSyncResult) -> AppSyncProgressState {
    .finished
  }

  private func mergeSyncResults(
    _ existing: [TrawlerArchiveSyncResult],
    replacing replacements: [TrawlerArchiveSyncResult]
  ) -> [TrawlerArchiveSyncResult] {
    let replacementIDs = Set(replacements.map(\.registeredTrawlerManifestIdentity))
    return existing.filter {
      !replacementIDs.contains($0.registeredTrawlerManifestIdentity)
    } + replacements
  }

  private func mergeFailures(
    _ existing: [TrawlerOperationFailure],
    replacing replacements: [TrawlerOperationFailure]
  ) -> [TrawlerOperationFailure] {
    let replacementIDs = Set(replacements.map(\.registeredTrawlerManifestIdentity))
    return existing.filter {
      !replacementIDs.contains($0.registeredTrawlerManifestIdentity)
    } + replacements
  }

  public func permissionChanged() async {
    checkDiskAccess()
    await refresh()
  }

  public func recoverFullDiskAccess(appIDs: [String]) async {
    let permissionFailureIDs = Set(
      (statusOperationFailures + syncOperationFailures)
        .filter { $0.failureCode == .permission }
        .map(\.registeredTrawlerManifestIdentity)
    )
    await permissionChanged()
    guard !needsFullDiskAccessRecovery else { return }
    let retryIDs = appIDs.filter { permissionFailureIDs.contains($0) }
    if !retryIDs.isEmpty {
      await syncNow(appIDs: retryIDs)
    }
  }

  @discardableResult
  public func checkDiskAccess() -> FullDiskAccessStatus {
    let status = permissionProbe.status()
    diskAccess = status
    return status
  }
}
