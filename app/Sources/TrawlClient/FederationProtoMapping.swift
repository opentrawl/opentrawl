import Foundation

private func required<T>(_ value: T?, _ hasValue: Bool) throws -> T {
  guard hasValue, let value else { throw TrawlClientError.invalidProtobuf }
  return value
}
private func validateRFC3339(_ value: String) throws {
  guard value.isEmpty || ISO8601DateFormatter().date(from: value) != nil else {
    throw TrawlClientError.invalidProtobuf
  }
}
extension Trawl_Federation_V1_OperationOutcome {
  func model() throws -> OperationOutcome {
    switch self {
    case .complete: .complete
    case .partial: .partial
    case .failed: .failed
    case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Federation_V1_FailureCode {
  func model() throws -> SourceFailureCode {
    switch self {
    case .unavailable: .unavailable
    case .permission: .permission
    case .authentication: .authentication
    case .invalidInput: .invalidInput
    case .notFound: .notFound
    case .timeout: .timeout
    case .internal: .internalError
    case .cancelled: .cancelled
    case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Federation_V1_SourceFailure {
  func model() throws -> SourceFailure {
    try SourceFailure(
      sourceID: sourceID, sourceName: surface, code: code.model(), message: message, remedy: remedy)
  }
}
extension Trawl_Federation_V1_SkippedSource {
  fileprivate func model() -> SkippedSource {
    SkippedSource(sourceID: sourceID, surface: surface, reason: reason)
  }
}
extension Trawl_Federation_V1_Count {
  fileprivate func model() -> SourceCount { SourceCount(id: id, label: label, value: value) }
}
extension Trawl_Federation_V1_Branding {
  fileprivate func model() -> Branding {
    Branding(
      symbolName: symbolName, accentColor: accentColor, iconPath: iconPath,
      bundleIdentifier: bundleIdentifier)
  }
}
extension Trawl_Federation_V1_SourceManifest {
  fileprivate func model() throws -> SourceManifest {
    guard !sourceID.isEmpty else { throw TrawlClientError.invalidProtobuf }
    return SourceManifest(
      sourceID: sourceID, surface: surface, branding: hasBranding ? branding.model() : nil,
      headlines: headlines, capabilities: capabilities)
  }
}
extension Trawl_Federation_V1_Freshness {
  fileprivate func model() -> Freshness {
    Freshness(status: status, ageSeconds: ageSeconds, staleAfterSeconds: staleAfterSeconds)
  }
}
extension Trawl_Federation_V1_Share {
  fileprivate func model() -> Share {
    Share(
      enabled: enabled, repoPath: repoPath, remote: remote, branch: branch, needsUpdate: needsUpdate
    )
  }
}
extension Trawl_Federation_V1_Remote {
  fileprivate func model() throws -> Remote {
    try validateRFC3339(lastIngestRfc3339)
    try validateRFC3339(lastSyncRfc3339)
    return Remote(
      enabled: enabled, mode: mode, endpoint: endpoint, archive: archive,
      lastIngestRFC3339: lastIngestRfc3339, lastSyncRFC3339: lastSyncRfc3339,
      needsUpdate: needsUpdate)
  }
}
extension Trawl_Federation_V1_SetupKind {
  fileprivate func model() throws -> SetupKind {
    switch self {
    case .fullDiskAccess: .fullDiskAccess
    case .photosPermission: .photosPermission
    case .account: .account
    case .pairing: .pairing
    case .archiveImport: .archiveImport
    case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Federation_V1_SetupState {
  fileprivate func model() throws -> SetupState {
    switch self {
    case .ready: .ready
    case .needsAction: .needsAction
    case .unavailable: .unavailable
    case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Federation_V1_SetupActionKind {
  fileprivate func model() throws -> SetupAction {
    switch self {
    case .none: .none
    case .openFullDiskAccess: .openFullDiskAccess
    case .requestPhotos: .requestPhotos
    case .runCommand: .runCommand
    case .chooseArchive: .chooseArchive
    case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Federation_V1_SetupRequirement {
  fileprivate func model() throws -> SetupRequirement {
    guard !id.isEmpty else { throw TrawlClientError.invalidProtobuf }
    return try SetupRequirement(
      id: id, kind: kind.model(), state: state.model(), explanation: explanation,
      action: action.model(), command: command)
  }
}
extension Trawl_Federation_V1_Database {
  fileprivate func model() throws -> Database {
    try validateRFC3339(modifiedRfc3339)
    return Database(
      id: id, label: label, kind: kind, role: role, path: path, endpoint: endpoint,
      archive: archive, isPrimary: isPrimary, bytes: bytes, modifiedRFC3339: modifiedRfc3339,
      counts: counts.map { $0.model() })
  }
}
extension Trawl_Federation_V1_SourceStatus {
  fileprivate func model() throws -> SourceStatus {
    try validateRFC3339(generatedRfc3339)
    try validateRFC3339(lastSyncRfc3339)
    try validateRFC3339(lastImportRfc3339)
    try validateRFC3339(lastExportRfc3339)
    return SourceStatus(
      manifest: try required(manifest, hasManifest).model(), appID: appID,
      schemaVersion: schemaVersion, generatedRFC3339: generatedRfc3339, state: state,
      summary: summary, configPath: configPath, databasePath: databasePath,
      databaseBytes: databaseBytes, walBytes: walBytes, lastSyncRFC3339: lastSyncRfc3339,
      lastImportRFC3339: lastImportRfc3339, lastExportRFC3339: lastExportRfc3339,
      counts: counts.map { $0.model() }, freshness: hasFreshness ? freshness.model() : nil,
      share: hasShare ? share.model() : nil, remote: hasRemote ? try remote.model() : nil,
      databases: try databases.map { try $0.model() },
      setupRequirements: try setupRequirements.map { try $0.model() }, warnings: warnings,
      errors: errors)
  }
}
extension Trawl_Federation_V1_StatusResponse {
  func model() throws -> StatusResponse {
    try StatusResponse(
      sources: sources.map { try $0.model() }, failures: failures.map { try $0.model() },
      skippedSources: skippedSources.map { $0.model() }, outcome: outcome.model())
  }
}
extension Trawl_Federation_V1_SearchOrder {
  fileprivate func model() throws -> SearchOrder {
    switch self {
    case .recency: .recency
    case .relevance: .relevance
    case .unspecified, .UNRECOGNIZED: throw TrawlClientError.invalidProtobuf
    }
  }
}
extension Trawl_Federation_V1_WhoResolved {
  fileprivate func model() -> WhoResolved { WhoResolved(who: who, identifiers: identifiers) }
}
extension Trawl_Federation_V1_SearchHit {
  fileprivate func model() throws -> SearchHit {
    guard !sourceID.isEmpty, !openRef.isEmpty else { throw TrawlClientError.invalidProtobuf }
    let date: Date?
    if timeRfc3339.isEmpty {
      date = nil
    } else if let parsed = ISO8601DateFormatter().date(from: timeRfc3339) {
      date = parsed
    } else {
      throw TrawlClientError.invalidProtobuf
    }
    return SearchHit(
      sourceID: sourceID, openRef: openRef, shortRef: shortRef, timeRFC3339: timeRfc3339,
      time: date, who: who, where: `where`, calendar: calendar, snippet: snippet, allDay: allDay,
      availability: hasAvailability ? availability : nil, unread: hasUnread ? unread : nil)
  }
}
extension Trawl_Federation_V1_SearchSourceResult {
  fileprivate func model() throws -> SearchSourceResult {
    SearchSourceResult(
      sourceID: sourceID, surface: surface, whoResolved: hasWhoResolved ? whoResolved.model() : nil,
      hits: try hits.map { try $0.model() }, totalMatches: totalMatches, truncated: truncated)
  }
}
extension Trawl_Federation_V1_SearchResponse {
  func model() throws -> SearchResponse {
    try SearchResponse(
      order: order.model(), sources: sources.map { try $0.model() },
      hits: hits.map { try $0.model() }, failures: failures.map { try $0.model() },
      skippedSources: skippedSources.map { $0.model() }, outcome: outcome.model(),
      resultLimit: resultLimit, truncated: truncated)
  }
}
