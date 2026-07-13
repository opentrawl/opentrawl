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
  public private(set) var diskAccess: FullDiskAccessStatus = .undetermined

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
    if response.outcome == .failed, !response.failures.isEmpty, response.failures.allSatisfy({ $0.code == .timeout }) {
      phase = .timedOut
    } else if response.outcome == .failed {
      phase = .failed(response.failures.first?.message ?? "No source status check succeeded.")
    } else if response.outcome == .partial {
      phase = .partial
    } else {
      phase = .ready
    }
  }

  public func syncNow() async {
    guard !isSyncing else { return }
    isSyncing = true
    let previousSyncMessage = syncMessage
    let previousSyncResults = syncResults
    let previousSyncFailures = syncFailures
    syncMessage = nil
    syncResults = []
    syncFailures = []
    defer { isSyncing = false }

    do {
      let result = try await client.sync()
      syncResults = result.sources
      syncFailures = result.failures
      switch result.outcome {
      case .complete:
        break
      case .partial:
        syncMessage = "Some sources could not sync."
      case .failed:
        syncMessage = "No source could sync."
      }
      await refresh()
    } catch is CancellationError {
      syncMessage = previousSyncMessage
      syncResults = previousSyncResults
      syncFailures = previousSyncFailures
      return
    } catch TrawlClientError.cancelled {
      syncMessage = previousSyncMessage
      syncResults = previousSyncResults
      syncFailures = previousSyncFailures
      return
    } catch {
      syncMessage = error.localizedDescription
    }
  }

  public func permissionChanged() async {
    diskAccess = permissionProbe.status()
    await refresh()
  }
}
