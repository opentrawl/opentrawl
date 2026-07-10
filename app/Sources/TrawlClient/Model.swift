import Foundation

public enum FanoutCompletion: Sendable, Equatable {
  case complete
  case partial
  case failed
}

public struct SourceCount: Sendable, Equatable, Identifiable {
  public let id: String
  public let display: String

  public init(id: String, display: String) {
    self.id = id
    self.display = display
  }
}

public struct SourceStatus: Sendable, Equatable, Identifiable {
  public let id: String
  public let name: String
  public let state: String
  public let summary: String
  public let counts: [SourceCount]
  public let lastSyncedDisplay: String
  public let archiveBytes: Int64

  public init(
    id: String,
    name: String,
    state: String,
    summary: String,
    counts: [SourceCount],
    lastSyncedDisplay: String,
    archiveBytes: Int64
  ) {
    self.id = id
    self.name = name
    self.state = state
    self.summary = summary
    self.counts = counts
    self.lastSyncedDisplay = lastSyncedDisplay
    self.archiveBytes = archiveBytes
  }
}

public struct StatusResponse: Sendable, Equatable {
  public let sources: [SourceStatus]
  public let completion: FanoutCompletion

  public init(sources: [SourceStatus], completion: FanoutCompletion) {
    self.sources = sources
    self.completion = completion
  }
}

public struct SearchHit: Sendable, Equatable, Identifiable {
  public let id: String
  public let sourceID: String
  public let title: String
  public let snippet: String
  public let whenDisplay: String

  public init(
    id: String,
    sourceID: String,
    title: String,
    snippet: String,
    whenDisplay: String
  ) {
    self.id = id
    self.sourceID = sourceID
    self.title = title
    self.snippet = snippet
    self.whenDisplay = whenDisplay
  }
}

public struct SearchResponse: Sendable, Equatable {
  public static let maximumResults = 20

  public let hits: [SearchHit]
  public let completion: FanoutCompletion

  public init(hits: [SearchHit], completion: FanoutCompletion) {
    self.hits = hits
    self.completion = completion
  }
}

public enum TrawlClientError: Error, Sendable, Equatable, LocalizedError {
  case binaryMissing
  case launchFailed
  case processFailed(exitCode: Int32)
  case processDied(signal: Int32)
  case invalidFrame
  case frameTooLarge
  case invalidMessage

  public var errorDescription: String? {
    switch self {
    case .binaryMissing:
      "OpenTrawl's helper is missing. Rebuild the app."
    case .launchFailed:
      "OpenTrawl could not start its helper."
    case .processFailed(let exitCode):
      "OpenTrawl's helper stopped with exit code \(exitCode)."
    case .processDied:
      "OpenTrawl's helper stopped unexpectedly."
    case .invalidFrame, .invalidMessage:
      "OpenTrawl's helper returned unreadable data."
    case .frameTooLarge:
      "OpenTrawl's helper returned too much data in one result."
    }
  }
}

public protocol TrawlClient: Sendable {
  func status() async throws -> StatusResponse
  func sync() async throws -> FanoutCompletion
  func search(_ query: String, source: String?) async throws -> SearchResponse
  func open(_ ref: String) async throws -> String
}
