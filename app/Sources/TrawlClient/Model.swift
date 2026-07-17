import Foundation

public struct SyncSourceResult: Sendable, Equatable, Identifiable {
  public let sourceID: String
  public let sourceName: String
  public let outcome: OperationOutcome
  public let failure: SourceFailure?
  public var id: String { sourceID }
  public init(
    sourceID: String, sourceName: String, outcome: OperationOutcome, failure: SourceFailure?
  ) {
    self.sourceID = sourceID
    self.sourceName = sourceName
    self.outcome = outcome
    self.failure = failure
  }
}

public struct SyncResponse: Sendable, Equatable {
  public let sources: [SyncSourceResult]
  public let failures: [SourceFailure]
  public let outcome: OperationOutcome
  public init(sources: [SyncSourceResult], failures: [SourceFailure], outcome: OperationOutcome) {
    self.sources = sources
    self.failures = failures
    self.outcome = outcome
  }
}

public enum SyncProgress: Sendable, Equatable {
  case started(sourceID: String, sourceName: String)
  case finished(SyncSourceResult)
}

public enum TrawlClientError: Error, Sendable, Equatable, LocalizedError {
  case helperMissing, launchFailed, timedOut, cancelled, scopedSyncUnsupported
  case telegramHistoryUnsupported
  case terminatedBySignal(Int32)
  case nonZeroExitBeforeFrame(Int32)
  case missingFrame, extraFrame, oversizedFrame, invalidFrame, invalidProtobuf
  public var errorDescription: String? {
    switch self {
    case .helperMissing: "OpenTrawl's bundled helper is missing. Rebuild the app."
    case .launchFailed: "OpenTrawl could not start its bundled helper."
    case .timedOut: "OpenTrawl's helper took too long to respond."
    case .cancelled: "OpenTrawl stopped the helper request."
    case .scopedSyncUnsupported: "This OpenTrawl client cannot sync selected sources."
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
  func requestPhotos() async throws -> StatusResponse
  func sync() async throws -> SyncResponse
  func sync(progress: @escaping @Sendable (SyncProgress) -> Void) async throws -> SyncResponse
  func sync(
    sourceIDs: [String], progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse
  func downloadTelegramMessageHistory(
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse
  func search(_ query: String, source: String?) async throws -> SearchResponse
  func open(sourceID: String, ref: String, anchorID: String) async throws -> OpenResponse
  func resource(sourceID: String, ref: String, maxBytes: UInt32) async throws
    -> PresentationResourceData
}

extension TrawlClient {
  public func downloadTelegramMessageHistory(
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse {
    throw TrawlClientError.telegramHistoryUnsupported
  }

  public func sync(sourceIDs: [String]) async throws -> SyncResponse {
    try await sync(sourceIDs: sourceIDs) { _ in }
  }

  public func sync(
    sourceIDs: [String], progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse {
    if sourceIDs.isEmpty {
      return try await sync(progress: progress)
    }
    throw TrawlClientError.scopedSyncUnsupported
  }

  public func sync(progress: @escaping @Sendable (SyncProgress) -> Void) async throws
    -> SyncResponse
  {
    let response = try await sync()
    for source in response.sources {
      progress(.finished(source))
    }
    return response
  }

  public func resource(sourceID _: String, ref _: String, maxBytes _: UInt32) async throws
    -> PresentationResourceData
  {
    throw TrawlClientError.invalidProtobuf
  }
}
