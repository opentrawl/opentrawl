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
  public private(set) var syncProgress:
    [RegisteredTrawlerIdentity: AppSyncProgressState] = [:]
  public private(set) var diskAccess: FullDiskAccessStatus = .undetermined
  private var automaticSyncFailureCounts: [RegisteredTrawlerIdentity: Int] = [:]

  public var restingTrawlers: [RestingTrawler] {
    let runtimeTrawlers = TrawlerRestingCopy.trawlers(
      from: trawlerStatuses,
      failures: statusOperationFailures,
      trawlersSkippedFromOperation: trawlersSkippedFromStatus
    )
    let byID = Dictionary(uniqueKeysWithValues: runtimeTrawlers.map { ($0.id, $0) })
    return displayedTrawlers.compactMap { byID[$0] }
  }

  public var homeTrawlers: [RestingTrawler] {
    let runtimeByID = Dictionary(uniqueKeysWithValues: restingTrawlers.map { ($0.id, $0) })
    return displayedTrawlers.compactMap { registeredTrawler in
      if let runtime = runtimeByID[registeredTrawler] { return runtime }
      guard
        let entry = catalogEntry(for: registeredTrawler),
        entry.registeredTrawlerReleaseState == .comingSoon
      else {
        return nil
      }
      return RestingTrawler(comingSoon: entry)
    }
  }

  public var displayedTrawlers: [RegisteredTrawlerIdentity] {
    registeredTrawlerCatalog.map(\.id)
  }

  public var syncCandidateTrawlers: [RegisteredTrawlerIdentity] {
    registeredTrawlerCatalog.compactMap { entry in
      entry.registeredTrawlerReleaseState == .available
        && entry.registeredTrawlerIsEnabled ? entry.id : nil
    }
  }

  public func catalogEntry(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> RegisteredTrawlerCatalogEntry? {
    registeredTrawlerCatalog.first { $0.id == registeredTrawler }
  }

  public func manifest(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> RegisteredTrawlerManifest? {
    catalogEntry(for: registeredTrawler)?.registeredTrawlerManifest
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

  public var fullDiskAccessTrawlers: [RegisteredTrawlerIdentity] {
    (statusOperationFailures.filter { $0.failureCode == .permission }
      .map(\.failedTrawler)
      + syncOperationFailures.filter { $0.failureCode == .permission }
        .map(\.failedTrawler))
      .reduce(into: []) { registeredTrawlers, registeredTrawler in
        if !registeredTrawlers.contains(registeredTrawler) {
          registeredTrawlers.append(registeredTrawler)
        }
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

  private func orderedUniqueRegisteredTrawlers(
    _ values: [RegisteredTrawlerIdentity]
  ) -> [RegisteredTrawlerIdentity] {
    values.reduce(into: []) { result, registeredTrawler in
      if !result.contains(registeredTrawler) { result.append(registeredTrawler) }
    }
  }

  public func automaticSyncFailureCount(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> Int {
    automaticSyncFailureCounts[registeredTrawler, default: 0]
  }

  public func automaticSyncDelay(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> Duration {
    let multiplier = min(
      8, 1 << min(automaticSyncFailureCount(for: registeredTrawler), 3))
    return automaticSyncBaseDelay * multiplier
  }

  public func syncNow(
    registeredTrawlers: [RegisteredTrawlerIdentity] = [],
    trigger: SyncTrigger = .manual
  ) async {
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
    let requestedTrawlers =
      registeredTrawlers.isEmpty ? trawlerStatuses.map(\.id) : registeredTrawlers
    let requestedSet = Set(requestedTrawlers)
    syncMessage = nil
    if registeredTrawlers.isEmpty {
      trawlerArchiveSyncResults = []
      syncOperationFailures = []
      syncProgress = [:]
    } else {
      trawlerArchiveSyncResults.removeAll {
        requestedSet.contains($0.registeredTrawler)
      }
      syncOperationFailures.removeAll {
        requestedSet.contains($0.failedTrawler)
      }
      for registeredTrawler in requestedTrawlers {
        syncProgress.removeValue(forKey: registeredTrawler)
      }
    }
    for registeredTrawler in requestedTrawlers {
      syncProgress[registeredTrawler] = .waiting
    }
    defer { isSyncing = false }

    do {
      let result = try await syncWithProgress(registeredTrawlers: requestedTrawlers)
      trawlerArchiveSyncResults = mergeSyncResults(
        trawlerArchiveSyncResults,
        replacing: result.trawlerArchiveSyncResults)
      syncOperationFailures = mergeFailures(
        syncOperationFailures,
        replacing: result.operationFailures)
      for trawlerArchiveSyncResult in result.trawlerArchiveSyncResults {
        syncProgress[trawlerArchiveSyncResult.registeredTrawler] =
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
        recordAutomaticSync(
          success: true, registeredTrawlers: requestedTrawlers, trigger: trigger)
        break
      case .partial:
        if result.operationFailures.isEmpty,
          !result.peopleArchiveUpdateFailuresAfterTrawlerArchiveSync.isEmpty
        {
          syncMessage = "People did not update."
        } else if !result.peopleArchiveUpdateFailuresAfterTrawlerArchiveSync.isEmpty {
          syncMessage = "Some apps and People did not update."
        } else {
          syncMessage = "Some apps could not sync."
        }
        let successfullySyncedTrawlers = Set(
          result.trawlerArchiveSyncResults.map(\.registeredTrawler))
        recordAutomaticSync(
          success: successfullySyncedTrawlers.isSuperset(of: requestedSet),
          registeredTrawlers: requestedTrawlers,
          trigger: trigger)
      case .failed:
        syncMessage = "No app could sync."
        recordAutomaticSync(
          success: false, registeredTrawlers: requestedTrawlers, trigger: trigger)
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
      recordAutomaticSync(
        success: false, registeredTrawlers: requestedTrawlers, trigger: trigger)
      for registeredTrawler in requestedTrawlers {
        switch syncProgress[registeredTrawler] {
        case .waiting, .building, .finalising:
          syncProgress[registeredTrawler] =
            .failed(error.localizedDescription)
        case .finished, .failed, .none:
          break
        }
      }
    }
  }

  public func runAutomaticSyncLoop(
    registeredTrawlers: [RegisteredTrawlerIdentity]
  ) async {
    let registeredTrawlers = orderedUniqueRegisteredTrawlers(registeredTrawlers)
    guard !registeredTrawlers.isEmpty else { return }
    var remaining = Dictionary(
      uniqueKeysWithValues: registeredTrawlers.map {
        ($0, automaticSyncDelay(for: $0))
      }
    )

    while !Task.isCancelled {
      guard let nextDelay = remaining.values.min() else { return }
      do {
        try await automaticSyncSleep(nextDelay)
      } catch {
        return
      }
      guard !Task.isCancelled else { return }
      for registeredTrawler in registeredTrawlers {
        remaining[registeredTrawler] =
          (remaining[registeredTrawler] ?? nextDelay) - nextDelay
      }
      let dueTrawlers = registeredTrawlers.filter { remaining[$0] == .zero }
      for registeredTrawler in dueTrawlers {
        guard !Task.isCancelled else { return }
        await syncNow(registeredTrawlers: [registeredTrawler], trigger: .automatic)
        remaining[registeredTrawler] = automaticSyncDelay(for: registeredTrawler)
      }
    }
  }

  private func recordAutomaticSync(
    success: Bool,
    registeredTrawlers: [RegisteredTrawlerIdentity],
    trigger: SyncTrigger
  ) {
    guard trigger == .automatic else { return }
    for registeredTrawler in registeredTrawlers {
      if success {
        automaticSyncFailureCounts[registeredTrawler] = 0
      } else {
        automaticSyncFailureCounts[registeredTrawler, default: 0] += 1
      }
    }
  }

  private func syncWithProgress(
    registeredTrawlers: [RegisteredTrawlerIdentity]
  ) async throws -> SyncResponse {
    let client = self.client
    let (events, continuation) = AsyncStream<SyncProgress>.makeStream()
    let task = Task<SyncResponse, Error> {
      defer { continuation.finish() }
      return try await client.sync(
        registeredTrawlers: registeredTrawlers
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
    case .building(let syncingTrawler):
      syncProgress[syncingTrawler] = .building
    case .finalising(let syncingTrawler):
      syncProgress[syncingTrawler] = .finalising
    }
  }

  private func progressState(for result: TrawlerArchiveSyncResult) -> AppSyncProgressState {
    .finished
  }

  private func mergeSyncResults(
    _ existing: [TrawlerArchiveSyncResult],
    replacing replacements: [TrawlerArchiveSyncResult]
  ) -> [TrawlerArchiveSyncResult] {
    let replacementIDs = Set(replacements.map(\.registeredTrawler))
    return existing.filter {
      !replacementIDs.contains($0.registeredTrawler)
    } + replacements
  }

  private func mergeFailures(
    _ existing: [TrawlerOperationFailure],
    replacing replacements: [TrawlerOperationFailure]
  ) -> [TrawlerOperationFailure] {
    let replacementIDs = Set(replacements.map(\.failedTrawler))
    return existing.filter {
      !replacementIDs.contains($0.failedTrawler)
    } + replacements
  }

  public func permissionChanged() async {
    checkDiskAccess()
    await refresh()
  }

  public func recoverFullDiskAccess(
    registeredTrawlers: [RegisteredTrawlerIdentity]
  ) async {
    let permissionFailureIDs = Set(
      (statusOperationFailures + syncOperationFailures)
        .filter { $0.failureCode == .permission }
        .map(\.failedTrawler)
    )
    await permissionChanged()
    guard !needsFullDiskAccessRecovery else { return }
    let retryTrawlers = registeredTrawlers.filter {
      permissionFailureIDs.contains($0)
    }
    if !retryTrawlers.isEmpty {
      await syncNow(registeredTrawlers: retryTrawlers)
    }
  }

  @discardableResult
  public func checkDiskAccess() -> FullDiskAccessStatus {
    let status = permissionProbe.status()
    diskAccess = status
    return status
  }
}
