import Foundation

public enum OperationOutcome: Sendable, Equatable { case complete, partial, failed }
public typealias FanoutCompletion = OperationOutcome
public enum SourceFailureCode: Sendable, Equatable {
  case unavailable, permission, authentication, invalidInput, notFound, timeout, internalError,
    cancelled, alreadySyncing
}
public struct SourceFailure: Sendable, Equatable, Identifiable {
  public let sourceID: String
  public let sourceName: String
  public let code: SourceFailureCode
  public let message: String
  public let remedy: String
  public var id: String { "\(sourceID):\(code):\(message)" }
  public init(
    sourceID: String, sourceName: String, code: SourceFailureCode, message: String, remedy: String
  ) {
    self.sourceID = sourceID
    self.sourceName = sourceName
    self.code = code
    self.message = message
    self.remedy = remedy
  }
}
public struct SkippedSource: Sendable, Equatable, Identifiable {
  public let sourceID: String
  public let surface: String
  public let reason: String
  public var id: String { sourceID }
  public init(sourceID: String, surface: String, reason: String) {
    self.sourceID = sourceID
    self.surface = surface
    self.reason = reason
  }
}
public struct Branding: Sendable, Equatable {
  public let symbolName: String
  public let accentColor: String
  public let iconPath: String
  public let bundleIdentifier: String
}
public struct SourceManifest: Sendable, Equatable {
  public let sourceID: String
  public let displayName: String
  public let branding: Branding?
  public let headlines: [String]
  public let capabilities: [String]
}
public struct SourceCount: Sendable, Equatable, Identifiable {
  public let id: String
  public let label: String
  public let value: Int64
}
public struct Freshness: Sendable, Equatable {
  public let status: String
  public let ageSeconds: Int64
  public let staleAfterSeconds: Int64
}
public enum SetupKind: Sendable, Equatable {
  case fullDiskAccess, photosPermission, account, pairing, archiveImport
}
public enum SetupState: Sendable, Equatable { case ready, needsAction, unavailable }
public enum SetupAction: Sendable, Equatable {
  case none, openFullDiskAccess, requestPhotos, runCommand, chooseArchive
}
public struct SetupRequirement: Sendable, Equatable, Identifiable {
  public let id: String
  public let kind: SetupKind
  public let state: SetupState
  public let explanation: String
  public let action: SetupAction
  public let command: [String]
}
public struct Database: Sendable, Equatable, Identifiable {
  public let id: String
  public let label: String
  public let kind: String
  public let role: String
  public let path: String
  public let endpoint: String
  public let archive: String
  public let isPrimary: Bool
  public let bytes: Int64
  public let modifiedRFC3339: String
  public let counts: [SourceCount]
}
public struct Share: Sendable, Equatable {
  public let enabled: Bool
  public let repoPath: String
  public let remote: String
  public let branch: String
  public let needsUpdate: Bool
}
public struct Remote: Sendable, Equatable {
  public let enabled: Bool
  public let mode: String
  public let endpoint: String
  public let archive: String
  public let lastIngestRFC3339: String
  public let lastSyncRFC3339: String
  public let needsUpdate: Bool
}
public struct SourceStatus: Sendable, Equatable, Identifiable {
  public let manifest: SourceManifest
  public let appID: String
  public let schemaVersion: String
  public let generatedRFC3339: String
  public let state: String
  public let summary: String
  public let configPath: String
  public let databasePath: String
  public let databaseBytes: Int64
  public let walBytes: Int64
  public let lastSyncRFC3339: String
  public let lastImportRFC3339: String
  public let lastExportRFC3339: String
  public let counts: [SourceCount]
  public let freshness: Freshness?
  public let share: Share?
  public let remote: Remote?
  public let databases: [Database]
  public let setupRequirements: [SetupRequirement]
  public let warnings: [String]
  public let errors: [String]
  public var id: String { manifest.sourceID }
}
public struct StatusResponse: Sendable, Equatable {
  public let sources: [SourceStatus]
  public let failures: [SourceFailure]
  public let skippedSources: [SkippedSource]
  public let outcome: OperationOutcome
}
public enum SearchOrder: Sendable, Equatable { case recency, relevance }
public struct WhoResolved: Sendable, Equatable {
  public let who: String
  public let identifiers: [String]
}
public struct ResultSummary: Sendable, Equatable {
  public let title: String
  public let subtitle: String
}
public struct SearchTextRun: Sendable, Equatable {
  public let text: String
  public let matched: Bool
}
public enum SearchEvidence: Sendable, Equatable {
  case text(label: String, runs: [SearchTextRun])
  case field(label: String, name: String, value: [SearchTextRun])
  case media(label: String, resourceRef: String, description: [SearchTextRun])
  case relation(label: String, relation: String, target: [SearchTextRun])
}
extension SearchEvidence {
  public var label: String {
    switch self {
    case .text(let label, _), .field(let label, _, _), .media(let label, _, _),
      .relation(let label, _, _):
      label
    }
  }

  public var displayText: String {
    let runs: [SearchTextRun]
    switch self {
    case .text(_, let value): runs = value
    case .field(_, _, let value): runs = value
    case .media(_, _, let value): runs = value
    case .relation(_, _, let value): runs = value
    }
    return runs.map(\.text).joined()
  }

  public var labelledDisplayText: String { "\(label): \(displayText)" }
}
public struct SearchArchiveContext: Sendable, Equatable {
  public let kind: String
  public let label: String
  public init(kind: String, label: String) {
    self.kind = kind
    self.label = label
  }
}
public struct SearchHitID: Sendable, Hashable {
  public let sourceID: String
  public let openRef: String
  public let anchorID: String
}

public struct SearchHit: Sendable, Equatable, Identifiable {
  public let sourceID: String
  public let openRef: String
  public let shortRef: String
  public let timeRFC3339: String
  public let time: Date?
  public let anchorID: String
  public let summary: ResultSummary
  public let archiveContext: [SearchArchiveContext]
  public let evidence: [SearchEvidence]
  public let allDay: Bool
  public let availability: Int64?
  public let unread: Bool?
  public var id: SearchHitID {
    SearchHitID(sourceID: sourceID, openRef: openRef, anchorID: anchorID)
  }
  public init(
    sourceID: String, openRef: String, shortRef: String, timeRFC3339: String, time: Date?,
    anchorID: String, summary: ResultSummary, archiveContext: [SearchArchiveContext] = [],
    evidence: [SearchEvidence], allDay: Bool, availability: Int64?, unread: Bool?
  ) {
    self.sourceID = sourceID
    self.openRef = openRef
    self.shortRef = shortRef
    self.timeRFC3339 = timeRFC3339
    self.time = time
    self.anchorID = anchorID
    self.summary = summary
    self.archiveContext = archiveContext
    self.evidence = evidence
    self.allDay = allDay
    self.availability = availability
    self.unread = unread
  }
}
public struct SearchSourceResult: Sendable, Equatable {
  public let sourceID: String
  public let displayName: String
  public let whoResolved: WhoResolved?
  public let hits: [SearchHit]
  public let totalMatches: UInt64
  public let totalIsExact: Bool
  public let truncated: Bool
}
public struct SearchResponse: Sendable, Equatable {
  public static let maximumResults: UInt32 = 20
  public let order: SearchOrder
  public let sources: [SearchSourceResult]
  public let hits: [SearchHit]
  public let failures: [SourceFailure]
  public let skippedSources: [SkippedSource]
  public let outcome: OperationOutcome
  public let resultLimit: UInt32
  public let truncated: Bool
}
