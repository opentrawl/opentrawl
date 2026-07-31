import Foundation

public struct TrawlerArchiveSyncResult: Sendable, Equatable, Identifiable {
  public let registeredTrawler: RegisteredTrawlerIdentity
  public let registeredTrawlerDisplayName: String
  public let archiveRecordCountAddedByThisSync: UInt64?
  public let archiveRecordCountUpdatedByThisSync: UInt64?
  public let archiveRecordCountRemovedByThisSync: UInt64?

  public var id: RegisteredTrawlerIdentity { registeredTrawler }

  public init(
    registeredTrawler: RegisteredTrawlerIdentity,
    registeredTrawlerDisplayName: String,
    archiveRecordCountAddedByThisSync: UInt64?,
    archiveRecordCountUpdatedByThisSync: UInt64?,
    archiveRecordCountRemovedByThisSync: UInt64?
  ) {
    self.registeredTrawler = registeredTrawler
    self.registeredTrawlerDisplayName = registeredTrawlerDisplayName
    self.archiveRecordCountAddedByThisSync = archiveRecordCountAddedByThisSync
    self.archiveRecordCountUpdatedByThisSync = archiveRecordCountUpdatedByThisSync
    self.archiveRecordCountRemovedByThisSync = archiveRecordCountRemovedByThisSync
  }
}

public struct PeopleArchiveUpdateFailureAfterTrawlerArchiveSync:
  Sendable, Equatable, Identifiable
{
  public let successfullySyncedTrawler: RegisteredTrawlerIdentity
  public let successfullySyncedTrawlerDisplayName: String

  public var id: RegisteredTrawlerIdentity { successfullySyncedTrawler }
}

public struct SyncResponse: Sendable, Equatable {
  public let trawlerArchiveSyncResults: [TrawlerArchiveSyncResult]
  public let operationFailures: [TrawlerOperationFailure]
  public let peopleArchiveUpdateFailuresAfterTrawlerArchiveSync:
    [PeopleArchiveUpdateFailureAfterTrawlerArchiveSync]
  public let outcome: OperationOutcome

  public init(
    trawlerArchiveSyncResults: [TrawlerArchiveSyncResult],
    operationFailures: [TrawlerOperationFailure],
    peopleArchiveUpdateFailuresAfterTrawlerArchiveSync:
      [PeopleArchiveUpdateFailureAfterTrawlerArchiveSync],
    outcome: OperationOutcome
  ) {
    self.trawlerArchiveSyncResults = trawlerArchiveSyncResults
    self.operationFailures = operationFailures
    self.peopleArchiveUpdateFailuresAfterTrawlerArchiveSync =
      peopleArchiveUpdateFailuresAfterTrawlerArchiveSync
    self.outcome = outcome
  }
}

public enum SyncProgress: Sendable, Equatable {
  case building(syncingTrawler: RegisteredTrawlerIdentity)
  case finalising(syncingTrawler: RegisteredTrawlerIdentity)
}

public enum TrawlClientError: Error, Sendable, Equatable, LocalizedError {
  case helperMissing
  case launchFailed
  case timedOut
  case cancelled
  case terminatedBySignal(Int32)
  case nonZeroExitBeforeFrame(Int32)
  case missingFrame
  case extraFrame
  case oversizedFrame
  case invalidFrame
  case invalidProtobuf

  public var errorDescription: String? {
    switch self {
    case .helperMissing: "OpenTrawl's bundled helper is missing. Rebuild the app."
    case .launchFailed: "OpenTrawl could not start its bundled helper."
    case .timedOut: "OpenTrawl's helper took too long to respond."
    case .cancelled: "OpenTrawl stopped the helper request."
    case .terminatedBySignal: "OpenTrawl's helper stopped unexpectedly."
    case .nonZeroExitBeforeFrame: "OpenTrawl's helper stopped before it returned a result."
    case .missingFrame: "OpenTrawl's helper returned no result."
    case .extraFrame, .invalidFrame, .invalidProtobuf:
      "OpenTrawl's helper returned unreadable data."
    case .oversizedFrame: "OpenTrawl's helper returned too much data in one result."
    }
  }
}

public protocol TrawlClient: Sendable {
  func status() async throws -> StatusResponse
  func sync(
    registeredTrawlers: [RegisteredTrawlerIdentity],
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse
  func downloadTelegramMessageHistory(
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse
  func search(_ request: TrawlArchiveSearchRequest) async throws -> SearchResponse
  func open(
    link: GloballyRoutableTrawlLink,
    anchor: RecordAnchorIdentifier
  ) async throws -> OpenResponse
}

extension TrawlClient {
  public func sync() async throws -> SyncResponse {
    try await sync(registeredTrawlers: []) { _ in }
  }

  public func sync(
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse {
    try await sync(registeredTrawlers: [], progress: progress)
  }

  public func sync(
    registeredTrawlers: [RegisteredTrawlerIdentity]
  ) async throws -> SyncResponse {
    try await sync(
      registeredTrawlers: registeredTrawlers
    ) { _ in }
  }

  public func search(
    _ query: String,
    registeredTrawler: RegisteredTrawlerIdentity?
  ) async throws -> SearchResponse {
    try await search(
      TrawlArchiveSearchRequest(
        searchQueryText: query,
        onlySearchThisRegisteredTrawler: registeredTrawler
      )
    )
  }
}
