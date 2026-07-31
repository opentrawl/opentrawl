import Foundation

public struct TrawlerArchiveUpdateResult: Sendable, Equatable, Identifiable {
  public let registeredTrawler: RegisteredTrawlerIdentity
  public let registeredTrawlerDisplayName: String
  public let archiveRecordCountAddedByThisUpdate: UInt64?
  public let archiveRecordCountUpdatedByThisUpdate: UInt64?
  public let archiveRecordCountRemovedByThisUpdate: UInt64?

  public var id: RegisteredTrawlerIdentity { registeredTrawler }

  public init(
    registeredTrawler: RegisteredTrawlerIdentity,
    registeredTrawlerDisplayName: String,
    archiveRecordCountAddedByThisUpdate: UInt64?,
    archiveRecordCountUpdatedByThisUpdate: UInt64?,
    archiveRecordCountRemovedByThisUpdate: UInt64?
  ) {
    self.registeredTrawler = registeredTrawler
    self.registeredTrawlerDisplayName = registeredTrawlerDisplayName
    self.archiveRecordCountAddedByThisUpdate = archiveRecordCountAddedByThisUpdate
    self.archiveRecordCountUpdatedByThisUpdate = archiveRecordCountUpdatedByThisUpdate
    self.archiveRecordCountRemovedByThisUpdate = archiveRecordCountRemovedByThisUpdate
  }
}

public struct PeopleArchiveUpdateFailureAfterTrawlerArchiveUpdate:
  Sendable, Equatable, Identifiable
{
  public let successfullyUpdatedTrawler: RegisteredTrawlerIdentity
  public let successfullyUpdatedTrawlerDisplayName: String

  public var id: RegisteredTrawlerIdentity { successfullyUpdatedTrawler }
}

public struct UpdateResponse: Sendable, Equatable {
  public let trawlerArchiveUpdateResults: [TrawlerArchiveUpdateResult]
  public let operationFailures: [TrawlerOperationFailure]
  public let peopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate:
    [PeopleArchiveUpdateFailureAfterTrawlerArchiveUpdate]
  public let outcome: OperationOutcome

  public init(
    trawlerArchiveUpdateResults: [TrawlerArchiveUpdateResult],
    operationFailures: [TrawlerOperationFailure],
    peopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate:
      [PeopleArchiveUpdateFailureAfterTrawlerArchiveUpdate],
    outcome: OperationOutcome
  ) {
    self.trawlerArchiveUpdateResults = trawlerArchiveUpdateResults
    self.operationFailures = operationFailures
    self.peopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate =
      peopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate
    self.outcome = outcome
  }
}

public enum UpdateProgress: Sendable, Equatable {
  case building(updatingTrawler: RegisteredTrawlerIdentity)
  case finalising(updatingTrawler: RegisteredTrawlerIdentity)
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
  func update(
    registeredTrawlers: [RegisteredTrawlerIdentity],
    progress: @escaping @Sendable (UpdateProgress) -> Void
  ) async throws -> UpdateResponse
  func downloadTelegramMessageHistory(
    progress: @escaping @Sendable (UpdateProgress) -> Void
  ) async throws -> UpdateResponse
  func search(_ request: TrawlArchiveSearchRequest) async throws -> SearchResponse
  func open(
    link: GloballyRoutableTrawlLink,
    anchor: RecordAnchorIdentifier
  ) async throws -> OpenResponse
}

extension TrawlClient {
  public func update() async throws -> UpdateResponse {
    try await update(registeredTrawlers: []) { _ in }
  }

  public func update(
    progress: @escaping @Sendable (UpdateProgress) -> Void
  ) async throws -> UpdateResponse {
    try await update(registeredTrawlers: [], progress: progress)
  }

  public func update(
    registeredTrawlers: [RegisteredTrawlerIdentity]
  ) async throws -> UpdateResponse {
    try await update(
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
