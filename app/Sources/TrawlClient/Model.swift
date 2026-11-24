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

public struct FederatedTrawlerArchiveUpdateOperation: Sendable, Equatable {
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

public enum TrawlerArchiveUpdateProgress: Sendable, Equatable {
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
  case remoteServerURLInvalid
  case remoteInsecureTransportRejected
  case remoteAuthenticationRequired
  case remoteSavedAccessTokenMissing(hostDisplayName: String)
  case remoteAccessDenied(String)
  case remoteRequestRejected(String)
  case remoteServerUnreachable(String)
  case remoteArchiveReadOnly

  public var errorDescription: String? {
    switch self {
    case .helperMissing: "OpenTrawl's bundled helper is missing. Rebuild the app."
    case .launchFailed: "OpenTrawl could not start its bundled helper."
    case .timedOut: "OpenTrawl took too long to respond."
    case .cancelled: "OpenTrawl stopped the request."
    case .terminatedBySignal: "OpenTrawl's helper stopped unexpectedly."
    case .nonZeroExitBeforeFrame: "OpenTrawl's helper stopped before it returned a result."
    case .missingFrame: "OpenTrawl's helper returned no result."
    case .extraFrame, .invalidFrame, .invalidProtobuf:
      "OpenTrawl returned unreadable data."
    case .oversizedFrame: "OpenTrawl returned too much data in one result."
    case .remoteServerURLInvalid:
      "Enter a valid Tailscale or https:// server address."
    case .remoteInsecureTransportRejected:
      "Plain HTTP is only allowed over Tailscale or localhost. Use Tailscale or https://."
    case .remoteAuthenticationRequired:
      "The access token was rejected or revoked. Create a new token on the archive Mac."
    case .remoteSavedAccessTokenMissing(let hostDisplayName):
      "The saved access token for \(hostDisplayName) is missing on this Mac. Open Change archive host to enter a token, or switch to This Mac."
    case .remoteAccessDenied(let detail):
      detail.isEmpty ? "This access token does not allow that source." : detail
    case .remoteRequestRejected(let detail):
      detail.isEmpty ? "The remote archive rejected the request." : detail
    case .remoteServerUnreachable(let detail):
      detail.isEmpty
        ? "Could not reach the remote archive server."
        : "Could not reach the remote archive server: \(detail)"
    case .remoteArchiveReadOnly:
      "Remote archives are read-only. Run updates on the archive host."
    }
  }
}

public protocol TrawlClient: Sendable {
  func status() async throws -> FederatedTrawlerStatusOperation
  func update(
    registeredTrawlers: [RegisteredTrawlerIdentity],
    progress: @escaping @Sendable (TrawlerArchiveUpdateProgress) -> Void
  ) async throws -> FederatedTrawlerArchiveUpdateOperation
  func search(_ request: TrawlArchiveSearchRequest) async throws -> FederatedTrawlerSearchOperation
  func open(
    link: GloballyRoutableTrawlLink,
    anchor: RecordAnchorIdentifier
  ) async throws -> OpenResponse
  func photoImageData(for link: GloballyRoutableTrawlLink) async throws -> Data?
}

extension TrawlClient {
  public func photoImageData(for link: GloballyRoutableTrawlLink) async throws -> Data? {
    nil
  }
  public func update() async throws -> FederatedTrawlerArchiveUpdateOperation {
    try await update(registeredTrawlers: []) { _ in }
  }

  public func update(
    progress: @escaping @Sendable (TrawlerArchiveUpdateProgress) -> Void
  ) async throws -> FederatedTrawlerArchiveUpdateOperation {
    try await update(registeredTrawlers: [], progress: progress)
  }

  public func update(
    registeredTrawlers: [RegisteredTrawlerIdentity]
  ) async throws -> FederatedTrawlerArchiveUpdateOperation {
    try await update(
      registeredTrawlers: registeredTrawlers
    ) { _ in }
  }

  public func search(
    _ query: String,
    registeredTrawler: RegisteredTrawlerIdentity?
  ) async throws -> FederatedTrawlerSearchOperation {
    try await search(
      TrawlArchiveSearchRequest(
        searchQueryText: query,
        onlySearchThisRegisteredTrawler: registeredTrawler
      )
    )
  }
}
