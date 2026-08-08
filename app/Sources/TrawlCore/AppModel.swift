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

public enum TrawlerArchiveUpdateProgressState: Sendable, Equatable {
  case waiting
  case building
  case finalising
  case finished
  case failed(String)
}

public enum TrawlerArchiveUpdateTrigger: Sendable, Equatable {
  case manual
  case automatic
}

@MainActor
@Observable
public final class AppModel {
  private let client: any TrawlClient
  private let permissionProbe: FullDiskAccessProbe
  private let automaticUpdateBaseDelay: Duration
  private let automaticUpdateSleep: @Sendable (Duration) async throws -> Void

  public private(set) var phase: HomePhase = .loading
  public private(set) var trawlerStatuses: [TrawlerStatus] = []
  public private(set) var registeredTrawlerCatalog: [RegisteredTrawlerCatalogEntry] = []
  public private(set) var statusOperationFailures: [TrawlerOperationFailure] = []
  public private(set) var trawlersSkippedFromStatus: [TrawlerSkippedFromOperation] = []
  public private(set) var statusOperationOutcome: OperationOutcome = .complete
  public private(set) var statusRefreshFailure: String?
  public private(set) var isUpdating = false
  public private(set) var lastSuccessfullyCompletedArchiveUpdateTime: Date?
  public private(set) var updateMessage: String?
  public private(set) var trawlerArchiveUpdateResults: [TrawlerArchiveUpdateResult] = []
  public private(set) var updateOperationFailures: [TrawlerOperationFailure] = []
  public private(set) var updateProgress:
    [RegisteredTrawlerIdentity: TrawlerArchiveUpdateProgressState] = [:]
  public private(set) var diskAccess: FullDiskAccessStatus = .undetermined
  private var automaticUpdateFailureCounts: [RegisteredTrawlerIdentity: Int] = [:]

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

  public var updateCandidateTrawlers: [RegisteredTrawlerIdentity] {
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
      && (statusOperationFailures + updateOperationFailures).contains {
        $0.failureCode == .permission
      }
  }

  public var fullDiskAccessTrawlers: [RegisteredTrawlerIdentity] {
    (statusOperationFailures.filter { $0.failureCode == .permission }
      .map(\.failedTrawler)
      + updateOperationFailures.filter { $0.failureCode == .permission }
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
    automaticUpdateBaseDelay: Duration = .seconds(3_600),
    automaticUpdateSleep: @escaping @Sendable (Duration) async throws -> Void = {
      try await Task.sleep(for: $0)
    }
  ) {
    self.client = client
    self.permissionProbe = permissionProbe
    self.automaticUpdateBaseDelay = automaticUpdateBaseDelay
    self.automaticUpdateSleep = automaticUpdateSleep
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

  private func applyStatus(_ response: FederatedTrawlerStatusOperation) {
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

  public func automaticUpdateFailureCount(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> Int {
    automaticUpdateFailureCounts[registeredTrawler, default: 0]
  }

  public func automaticUpdateDelay(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> Duration {
    let multiplier = min(
      8, 1 << min(automaticUpdateFailureCount(for: registeredTrawler), 3))
    return automaticUpdateBaseDelay * multiplier
  }

  public func updateNow(
    registeredTrawlers: [RegisteredTrawlerIdentity] = [],
    trigger: TrawlerArchiveUpdateTrigger = .manual
  ) async {
    guard !isUpdating else { return }
    if checkDiskAccess() == .denied {
      updateMessage = "Full Disk Access is required."
      return
    }
    isUpdating = true
    let previousUpdateMessage = updateMessage
    let previousUpdateResults = trawlerArchiveUpdateResults
    let previousUpdateFailures = updateOperationFailures
    let previousUpdateProgress = updateProgress
    let requestedTrawlers =
      registeredTrawlers.isEmpty ? trawlerStatuses.map(\.id) : registeredTrawlers
    let requestedSet = Set(requestedTrawlers)
    updateMessage = nil
    if registeredTrawlers.isEmpty {
      trawlerArchiveUpdateResults = []
      updateOperationFailures = []
      updateProgress = [:]
    } else {
      trawlerArchiveUpdateResults.removeAll {
        requestedSet.contains($0.registeredTrawler)
      }
      updateOperationFailures.removeAll {
        requestedSet.contains($0.failedTrawler)
      }
      for registeredTrawler in requestedTrawlers {
        updateProgress.removeValue(forKey: registeredTrawler)
      }
    }
    for registeredTrawler in requestedTrawlers {
      updateProgress[registeredTrawler] = .waiting
    }
    defer { isUpdating = false }

    do {
      let result = try await updateWithProgress(registeredTrawlers: requestedTrawlers)
      trawlerArchiveUpdateResults = mergeUpdateResults(
        trawlerArchiveUpdateResults,
        replacing: result.trawlerArchiveUpdateResults)
      updateOperationFailures = mergeFailures(
        updateOperationFailures,
        replacing: result.operationFailures)
      for trawlerArchiveUpdateResult in result.trawlerArchiveUpdateResults {
        updateProgress[trawlerArchiveUpdateResult.registeredTrawler] =
          progressState(for: trawlerArchiveUpdateResult)
      }
      for operationFailure in result.operationFailures {
        updateProgress[operationFailure.failedTrawler] =
          .failed(operationFailure.failureMessage)
      }
      if let collision = result.operationFailures.first(where: {
        $0.failureCode == .alreadyUpdating
      }) {
        updateMessage = collision.failureMessage
        trawlerArchiveUpdateResults = previousUpdateResults
        updateOperationFailures = previousUpdateFailures
        updateProgress = previousUpdateProgress
        return
      }
      var archiveUpdateCompletionTime: Date?
      switch result.outcome {
      case .complete:
        archiveUpdateCompletionTime = Date()
        recordAutomaticUpdate(
          successfullyUpdatedTrawlers: requestedSet,
          registeredTrawlers: requestedTrawlers,
          trigger: trigger)
      case .partial:
        if result.operationFailures.isEmpty,
          !result.peopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate.isEmpty
        {
          updateMessage = "People did not update."
        } else if !result.peopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate.isEmpty {
          updateMessage = "Some apps and People did not update."
        } else {
          updateMessage = "Some apps could not update."
        }
        if !result.trawlerArchiveUpdateResults.isEmpty {
          archiveUpdateCompletionTime = Date()
        }
        recordAutomaticUpdate(
          successfullyUpdatedTrawlers: Set(
            result.trawlerArchiveUpdateResults.map(\.registeredTrawler)),
          registeredTrawlers: requestedTrawlers,
          trigger: trigger)
      case .failed:
        updateMessage = "No app could update."
        recordAutomaticUpdate(
          successfullyUpdatedTrawlers: [],
          registeredTrawlers: requestedTrawlers,
          trigger: trigger)
      }
      if result.operationFailures.contains(where: { $0.failureCode == .permission }) {
        checkDiskAccess()
      }
      await refresh()
      // Published after the closing status refresh so the home toolbar's
      // completion confirmation starts when the update visibly ends.
      if let archiveUpdateCompletionTime {
        lastSuccessfullyCompletedArchiveUpdateTime = archiveUpdateCompletionTime
      }
    } catch is CancellationError {
      updateMessage = previousUpdateMessage
      trawlerArchiveUpdateResults = previousUpdateResults
      updateOperationFailures = previousUpdateFailures
      updateProgress = previousUpdateProgress
      return
    } catch TrawlClientError.cancelled {
      updateMessage = previousUpdateMessage
      trawlerArchiveUpdateResults = previousUpdateResults
      updateOperationFailures = previousUpdateFailures
      updateProgress = previousUpdateProgress
      return
    } catch {
      updateMessage = error.localizedDescription
      recordAutomaticUpdate(
        successfullyUpdatedTrawlers: [],
        registeredTrawlers: requestedTrawlers,
        trigger: trigger)
      for registeredTrawler in requestedTrawlers {
        switch updateProgress[registeredTrawler] {
        case .waiting, .building, .finalising:
          updateProgress[registeredTrawler] =
            .failed(error.localizedDescription)
        case .finished, .failed, .none:
          break
        }
      }
    }
  }

  private var initialAutomaticUpdateDelay: Duration {
    guard let lastUpdateTime = lastSuccessfullyCompletedArchiveUpdateTime else {
      return .zero
    }
    let elapsed = Duration.seconds(Date().timeIntervalSince(lastUpdateTime))
    guard elapsed < automaticUpdateBaseDelay else { return .zero }
    return automaticUpdateBaseDelay - elapsed
  }

  public func runAutomaticUpdateLoop(
    registeredTrawlers: [RegisteredTrawlerIdentity]
  ) async {
    let registeredTrawlers = orderedUniqueRegisteredTrawlers(registeredTrawlers)
    guard !registeredTrawlers.isEmpty else { return }
    // The first cycle is due immediately so the archive updates on launch.
    // After a recent successful update, such as the onboarding build that
    // just finished, the loop instead waits out the remainder of the
    // automatic delay. Each trawler then repeats on its own delay.
    let initialDelay = initialAutomaticUpdateDelay
    var remaining = Dictionary(
      uniqueKeysWithValues: registeredTrawlers.map { ($0, initialDelay) }
    )

    while !Task.isCancelled {
      guard let nextDelay = remaining.values.min() else { return }
      do {
        try await automaticUpdateSleep(nextDelay)
      } catch {
        return
      }
      guard !Task.isCancelled else { return }
      for registeredTrawler in registeredTrawlers {
        remaining[registeredTrawler] =
          (remaining[registeredTrawler] ?? nextDelay) - nextDelay
      }
      let dueTrawlers = registeredTrawlers.filter { remaining[$0] == .zero }
      guard !dueTrawlers.isEmpty, !Task.isCancelled else { continue }
      await updateNow(registeredTrawlers: dueTrawlers, trigger: .automatic)
      for registeredTrawler in dueTrawlers {
        remaining[registeredTrawler] = automaticUpdateDelay(for: registeredTrawler)
      }
    }
  }

  private func recordAutomaticUpdate(
    successfullyUpdatedTrawlers: Set<RegisteredTrawlerIdentity>,
    registeredTrawlers: [RegisteredTrawlerIdentity],
    trigger: TrawlerArchiveUpdateTrigger
  ) {
    guard trigger == .automatic else { return }
    for registeredTrawler in registeredTrawlers {
      if successfullyUpdatedTrawlers.contains(registeredTrawler) {
        automaticUpdateFailureCounts[registeredTrawler] = 0
      } else {
        automaticUpdateFailureCounts[registeredTrawler, default: 0] += 1
      }
    }
  }

  private func updateWithProgress(
    registeredTrawlers: [RegisteredTrawlerIdentity]
  ) async throws -> FederatedTrawlerArchiveUpdateOperation {
    let client = self.client
    let (events, continuation) = AsyncStream<TrawlerArchiveUpdateProgress>.makeStream()
    let task = Task<FederatedTrawlerArchiveUpdateOperation, Error> {
      defer { continuation.finish() }
      return try await client.update(
        registeredTrawlers: registeredTrawlers
      ) { event in
        continuation.yield(event)
      }
    }
    return try await withTaskCancellationHandler {
      for await event in events {
        applyUpdateProgress(event)
      }
      return try await task.value
    } onCancel: {
      task.cancel()
    }
  }

  private func applyUpdateProgress(_ progress: TrawlerArchiveUpdateProgress) {
    switch progress {
    case .building(let updatingTrawler):
      updateProgress[updatingTrawler] = .building
    case .finalising(let updatingTrawler):
      updateProgress[updatingTrawler] = .finalising
    }
  }

  private func progressState(
    for result: TrawlerArchiveUpdateResult
  ) -> TrawlerArchiveUpdateProgressState {
    .finished
  }

  private func mergeUpdateResults(
    _ existing: [TrawlerArchiveUpdateResult],
    replacing replacements: [TrawlerArchiveUpdateResult]
  ) -> [TrawlerArchiveUpdateResult] {
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
      (statusOperationFailures + updateOperationFailures)
        .filter { $0.failureCode == .permission }
        .map(\.failedTrawler)
    )
    await permissionChanged()
    guard !needsFullDiskAccessRecovery else { return }
    let retryTrawlers = registeredTrawlers.filter {
      permissionFailureIDs.contains($0)
    }
    if !retryTrawlers.isEmpty {
      await updateNow(registeredTrawlers: retryTrawlers)
    }
  }

  @discardableResult
  public func checkDiskAccess() -> FullDiskAccessStatus {
    let status = permissionProbe.status()
    diskAccess = status
    return status
  }
}
