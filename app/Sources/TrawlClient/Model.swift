import Foundation

public enum OperationOutcome: Sendable, Equatable {
  case complete
  case partial
  case failed
}

public typealias FanoutCompletion = OperationOutcome

public enum SourceFailureCode: Sendable, Equatable {
  case unavailable
  case permission
  case authentication
  case invalidInput
  case notFound
  case timeout
  case internalError
}

public struct SourceFailure: Sendable, Equatable, Identifiable {
  public let sourceID: String
  public let sourceName: String
  public let code: SourceFailureCode
  public let message: String
  public let remedy: String

  public var id: String { "\(sourceID):\(code):\(message)" }

  public init(
    sourceID: String,
    sourceName: String,
    code: SourceFailureCode,
    message: String,
    remedy: String
  ) {
    self.sourceID = sourceID
    self.sourceName = sourceName
    self.code = code
    self.message = message
    self.remedy = remedy
  }
}

public enum SetupKind: Sendable, Equatable {
  case fullDiskAccess
  case photosPermission
  case account
  case pairing
  case archiveImport
}

public enum SetupState: Sendable, Equatable {
  case ready
  case needsAction
  case unavailable
}

public enum SetupAction: Sendable, Equatable {
  case none
  case openFullDiskAccess
  case requestPhotos
  case runCommand
  case chooseArchive
}

public struct SetupRequirement: Sendable, Equatable, Identifiable {
  public let id: String
  public let kind: SetupKind
  public let state: SetupState
  public let explanation: String
  public let action: SetupAction
  public let command: [String]

  public init(
    id: String,
    kind: SetupKind,
    state: SetupState,
    explanation: String,
    action: SetupAction,
    command: [String]
  ) {
    self.id = id
    self.kind = kind
    self.state = state
    self.explanation = explanation
    self.action = action
    self.command = command
  }
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
  public let setupRequirements: [SetupRequirement]

  public init(
    id: String,
    name: String,
    state: String,
    summary: String,
    counts: [SourceCount],
    lastSyncedDisplay: String,
    archiveBytes: Int64,
    setupRequirements: [SetupRequirement] = []
  ) {
    self.id = id
    self.name = name
    self.state = state
    self.summary = summary
    self.counts = counts
    self.lastSyncedDisplay = lastSyncedDisplay
    self.archiveBytes = archiveBytes
    self.setupRequirements = setupRequirements
  }
}

public struct StatusResponse: Sendable, Equatable {
  public let sources: [SourceStatus]
  public let failures: [SourceFailure]
  public let outcome: OperationOutcome

  public var completion: OperationOutcome { outcome }

  public init(
    sources: [SourceStatus],
    failures: [SourceFailure] = [],
    outcome: OperationOutcome
  ) {
    self.sources = sources
    self.failures = failures
    self.outcome = outcome
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
  public static let maximumResults: UInt32 = 20

  public let hits: [SearchHit]
  public let failures: [SourceFailure]
  public let outcome: OperationOutcome
  public let resultLimit: UInt32
  public let truncated: Bool

  public var completion: OperationOutcome { outcome }

  public init(
    hits: [SearchHit],
    failures: [SourceFailure] = [],
    outcome: OperationOutcome,
    resultLimit: UInt32,
    truncated: Bool
  ) {
    self.hits = hits
    self.failures = failures
    self.outcome = outcome
    self.resultLimit = resultLimit
    self.truncated = truncated
  }
}

public struct SyncSourceResult: Sendable, Equatable, Identifiable {
  public let sourceID: String
  public let sourceName: String
  public let outcome: OperationOutcome
  public let failure: SourceFailure?

  public var id: String { sourceID }

  public init(
    sourceID: String,
    sourceName: String,
    outcome: OperationOutcome,
    failure: SourceFailure?
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

  public var completion: OperationOutcome { outcome }

  public init(
    sources: [SyncSourceResult],
    failures: [SourceFailure],
    outcome: OperationOutcome
  ) {
    self.sources = sources
    self.failures = failures
    self.outcome = outcome
  }
}

public struct OpenResponse: Sendable, Equatable {
  public let outcome: OperationOutcome
  public let sourceID: String
  public let openRef: String
  public let output: Data
  public let failure: SourceFailure?

  public init(
    outcome: OperationOutcome,
    sourceID: String,
    openRef: String,
    output: Data,
    failure: SourceFailure?
  ) {
    self.outcome = outcome
    self.sourceID = sourceID
    self.openRef = openRef
    self.output = output
    self.failure = failure
  }
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
    case .helperMissing:
      "OpenTrawl's bundled helper is missing. Rebuild the app."
    case .launchFailed:
      "OpenTrawl could not start its bundled helper."
    case .timedOut:
      "OpenTrawl's helper took too long to respond."
    case .cancelled:
      "OpenTrawl stopped the helper request."
    case .terminatedBySignal:
      "OpenTrawl's helper stopped unexpectedly."
    case .nonZeroExitBeforeFrame:
      "OpenTrawl's helper stopped before it returned a result."
    case .missingFrame:
      "OpenTrawl's helper returned no result."
    case .extraFrame, .invalidFrame, .invalidProtobuf:
      "OpenTrawl's helper returned unreadable data."
    case .oversizedFrame:
      "OpenTrawl's helper returned too much data in one result."
    }
  }
}

public protocol TrawlClient: Sendable {
  func status() async throws -> StatusResponse
  func sync() async throws -> SyncResponse
  func search(_ query: String, source: String?) async throws -> SearchResponse
  func open(_ ref: String) async throws -> OpenResponse
}
