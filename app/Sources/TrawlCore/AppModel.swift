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
  case running
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

  public private(set) var phase: HomePhase = .loading
  public private(set) var sources: [SourceStatus] = []
  public private(set) var statusFailures: [SourceFailure] = []
  public private(set) var skippedSources: [SkippedSource] = []
  public private(set) var completion: FanoutCompletion = .complete
  public private(set) var statusRefreshFailure: String?
  public private(set) var isSyncing = false
  public private(set) var syncMessage: String?
  public private(set) var syncResults: [SyncSourceResult] = []
  public private(set) var syncFailures: [SourceFailure] = []
  public private(set) var syncProgress: [String: AppSyncProgressState] = [:]
  public private(set) var diskAccess: FullDiskAccessStatus = .undetermined
  public private(set) var automaticSyncFailureCount = 0

  public var photosAccess: SetupRequirement? {
    sources.first(where: { $0.id == "photos" })?.setupRequirements.first {
      $0.kind == .photosPermission && $0.state != .ready
    }
  }

  public var restingSources: [RestingSource] {
    SourceRestingCopy.sources(
      from: sources,
      failures: statusFailures,
      skippedSources: skippedSources
    )
  }

  public var shouldShowFailureFallback: Bool {
    blockingFailureMessage != nil
  }

  public var blockingFailureMessage: String? {
    guard restingSources.isEmpty else { return nil }
    switch phase {
    case .failed(let message):
      return message
    case .timedOut:
      return statusRefreshFailure ?? "Source status checks timed out."
    case .loading, .ready, .partial:
      return nil
    }
  }

  public init(
    client: any TrawlClient,
    permissionProbe: FullDiskAccessProbe = FullDiskAccessProbe()
  ) {
    self.client = client
    self.permissionProbe = permissionProbe
  }

  public func refresh() async {
    diskAccess = permissionProbe.status()
    if sources.isEmpty {
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
      statusRefreshFailure = "Source status checks timed out."
      phase = .timedOut
    } catch {
      let message = error.localizedDescription
      statusRefreshFailure = message
      phase = .failed(message)
    }
  }

  public func requestPhotos() async {
    guard photosAccess?.action == .requestPhotos else { return }
    do {
      applyStatus(try await client.requestPhotos())
    } catch is CancellationError {
      return
    } catch TrawlClientError.cancelled {
      return
    } catch {
      statusRefreshFailure = error.localizedDescription
    }
  }

  private func applyStatus(_ response: StatusResponse) {
    sources = response.sources
    statusFailures = response.failures
    skippedSources = response.skippedSources
    completion = response.outcome
    statusRefreshFailure = nil
    if response.outcome == .failed, !response.failures.isEmpty,
      response.failures.allSatisfy({ $0.code == .timeout })
    {
      phase = .timedOut
    } else if response.outcome == .failed {
      phase = .failed(response.failures.first?.message ?? "No source status check succeeded.")
    } else if response.outcome == .partial {
      phase = .partial
    } else {
      phase = .ready
    }
  }

  public var automaticSyncDelay: Duration {
    let hours = min(8, 1 << min(automaticSyncFailureCount, 3))
    return .seconds(Int64(hours * 3_600))
  }

  public func syncNow(appIDs: [String] = [], trigger: SyncTrigger = .manual) async {
    guard !isSyncing else { return }
    isSyncing = true
    let previousSyncMessage = syncMessage
    let previousSyncResults = syncResults
    let previousSyncFailures = syncFailures
    let previousSyncProgress = syncProgress
    syncMessage = nil
    syncResults = []
    syncFailures = []
    let requestedAppIDs = appIDs.isEmpty ? sources.map(\.id) : appIDs
    syncProgress = Dictionary(uniqueKeysWithValues: requestedAppIDs.map { ($0, .waiting) })
    defer { isSyncing = false }

    do {
      let result = try await syncWithProgress(appIDs: appIDs)
      syncResults = result.sources
      syncFailures = result.failures
      for source in result.sources {
        syncProgress[source.sourceID] = progressState(for: source)
      }
      switch result.outcome {
      case .complete:
        automaticSyncFailureCount = 0
        break
      case .partial:
        syncMessage = "Some apps could not sync."
        if trigger == .automatic { automaticSyncFailureCount += 1 }
      case .failed:
        syncMessage = "No app could sync."
        if trigger == .automatic { automaticSyncFailureCount += 1 }
      }
      await refresh()
    } catch is CancellationError {
      syncMessage = previousSyncMessage
      syncResults = previousSyncResults
      syncFailures = previousSyncFailures
      syncProgress = previousSyncProgress
      return
    } catch TrawlClientError.cancelled {
      syncMessage = previousSyncMessage
      syncResults = previousSyncResults
      syncFailures = previousSyncFailures
      syncProgress = previousSyncProgress
      return
    } catch {
      syncMessage = error.localizedDescription
      if trigger == .automatic { automaticSyncFailureCount += 1 }
      for (sourceID, state) in syncProgress where state == .running {
        syncProgress[sourceID] = .failed(error.localizedDescription)
      }
    }
  }

  public func runAutomaticSyncLoop(appIDs: [String]) async {
    while !Task.isCancelled {
      do {
        try await Task.sleep(for: automaticSyncDelay)
      } catch {
        return
      }
      guard !Task.isCancelled else { return }
      await syncNow(appIDs: appIDs, trigger: .automatic)
    }
  }

  private func syncWithProgress(appIDs: [String]) async throws -> SyncResponse {
    let client = self.client
    let (events, continuation) = AsyncStream<SyncProgress>.makeStream()
    let task = Task<SyncResponse, Error> {
      defer { continuation.finish() }
      return try await client.sync(sourceIDs: appIDs) { event in
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
    case .started(let sourceID, _):
      syncProgress[sourceID] = .running
    case .finished(let result):
      syncProgress[result.sourceID] = progressState(for: result)
    }
  }

  private func progressState(for result: SyncSourceResult) -> AppSyncProgressState {
    if let failure = result.failure {
      return .failed(failure.message)
    }
    return result.outcome == .failed ? .failed("Sync failed.") : .finished
  }

  public func permissionChanged() async {
    diskAccess = permissionProbe.status()
    await refresh()
  }
}
