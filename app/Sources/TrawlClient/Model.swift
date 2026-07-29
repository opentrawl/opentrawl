import Foundation

public struct TrawlerArchiveSyncResult: Sendable, Equatable, Identifiable {
  public let registeredTrawlerManifestIdentity: String
  public let registeredTrawlerDisplayName: String
  public let archiveRecordCountAddedByThisSync: UInt64?
  public let archiveRecordCountUpdatedByThisSync: UInt64?
  public let archiveRecordCountRemovedByThisSync: UInt64?

  public var id: String { registeredTrawlerManifestIdentity }

  public init(
    registeredTrawlerManifestIdentity: String,
    registeredTrawlerDisplayName: String,
    archiveRecordCountAddedByThisSync: UInt64?,
    archiveRecordCountUpdatedByThisSync: UInt64?,
    archiveRecordCountRemovedByThisSync: UInt64?
  ) {
    self.registeredTrawlerManifestIdentity = registeredTrawlerManifestIdentity
    self.registeredTrawlerDisplayName = registeredTrawlerDisplayName
    self.archiveRecordCountAddedByThisSync = archiveRecordCountAddedByThisSync
    self.archiveRecordCountUpdatedByThisSync = archiveRecordCountUpdatedByThisSync
    self.archiveRecordCountRemovedByThisSync = archiveRecordCountRemovedByThisSync
  }
}

public struct SyncResponse: Sendable, Equatable {
  public let trawlerArchiveSyncResults: [TrawlerArchiveSyncResult]
  public let operationFailures: [TrawlerOperationFailure]
  public let outcome: OperationOutcome

  public init(
    trawlerArchiveSyncResults: [TrawlerArchiveSyncResult],
    operationFailures: [TrawlerOperationFailure],
    outcome: OperationOutcome
  ) {
    self.trawlerArchiveSyncResults = trawlerArchiveSyncResults
    self.operationFailures = operationFailures
    self.outcome = outcome
  }
}

public enum SyncProgress: Sendable, Equatable {
  case building(registeredTrawlerManifestIdentity: String)
  case finalising(registeredTrawlerManifestIdentity: String)
}

public enum TrawlClientError: Error, Sendable, Equatable, LocalizedError {
  case helperMissing
  case launchFailed
  case timedOut
  case cancelled
  case selectedTrawlerSyncUnsupported
  case telegramHistoryUnsupported
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
    case .selectedTrawlerSyncUnsupported:
      "This OpenTrawl client cannot update selected trawlers."
    case .telegramHistoryUnsupported:
      "This OpenTrawl client cannot download older Telegram messages."
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
  func sync() async throws -> SyncResponse
  func sync(progress: @escaping @Sendable (SyncProgress) -> Void) async throws -> SyncResponse
  func sync(
    registeredTrawlerManifestIdentities: [String],
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse
  func downloadTelegramMessageHistory(
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse
  func search(
    _ query: String,
    registeredTrawlerManifestIdentity: String?
  ) async throws -> SearchResponse
  func open(link: String, anchorIdentifier: String) async throws -> OpenResponse
}

extension TrawlClient {
  public func downloadTelegramMessageHistory(
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse {
    throw TrawlClientError.telegramHistoryUnsupported
  }

  public func sync(
    registeredTrawlerManifestIdentities: [String]
  ) async throws -> SyncResponse {
    try await sync(
      registeredTrawlerManifestIdentities: registeredTrawlerManifestIdentities
    ) { _ in }
  }

  public func sync(
    registeredTrawlerManifestIdentities: [String],
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse {
    if registeredTrawlerManifestIdentities.isEmpty {
      return try await sync(progress: progress)
    }
    throw TrawlClientError.selectedTrawlerSyncUnsupported
  }

  public func sync(
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse {
    try await sync()
  }
}
