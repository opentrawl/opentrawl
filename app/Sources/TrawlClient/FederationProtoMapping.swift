import Foundation

private func required<T>(_ value: T?, _ hasValue: Bool) throws -> T {
  guard hasValue, let value else { throw TrawlClientError.invalidProtobuf }
  return value
}

private func isDigit(_ byte: UInt8) -> Bool {
  byte >= 48 && byte <= 57
}

private func number(_ bytes: [UInt8], at index: Int, digits: Int) -> Int {
  bytes[index..<(index + digits)].reduce(0) { $0 * 10 + Int($1 - 48) }
}

private func parseRFC3339(_ value: String) -> Date? {
  let bytes = Array(value.utf8)
  guard bytes.count >= 20,
    bytes[4] == 45, bytes[7] == 45, bytes[10] == 84,
    bytes[13] == 58, bytes[16] == 58,
    [0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18].allSatisfy({ isDigit(bytes[$0]) })
  else { return nil }

  var suffix = 19
  if bytes[suffix] == 46 {
    suffix += 1
    let fractionStart = suffix
    while suffix < bytes.count, isDigit(bytes[suffix]) {
      suffix += 1
    }
    guard suffix > fractionStart else { return nil }
  }
  guard suffix < bytes.count else { return nil }
  if bytes[suffix] == 90 {
    guard suffix + 1 == bytes.count else { return nil }
  } else {
    guard bytes[suffix] == 43 || bytes[suffix] == 45, suffix + 6 == bytes.count,
      isDigit(bytes[suffix + 1]), isDigit(bytes[suffix + 2]), bytes[suffix + 3] == 58,
      isDigit(bytes[suffix + 4]), isDigit(bytes[suffix + 5])
    else { return nil }
  }
  let offsetHour = bytes[suffix] == 90 ? 0 : number(bytes, at: suffix + 1, digits: 2)
  let offsetMinute = bytes[suffix] == 90 ? 0 : number(bytes, at: suffix + 4, digits: 2)
  guard offsetHour <= 23, offsetMinute <= 59 else { return nil }
  let offset = offsetHour * 3_600 + offsetMinute * 60
  let localTimestamp = String(decoding: Array(bytes[..<suffix]) + [90], as: UTF8.self)
  guard let localDate = try? Date(localTimestamp, strategy: .iso8601) else { return nil }
  let date = localDate.addingTimeInterval(TimeInterval(bytes[suffix] == 45 ? offset : -offset))
  var calendar = Calendar(identifier: .gregorian)
  calendar.timeZone = .gmt
  let components = calendar.dateComponents(
    [.era, .year, .month, .day, .hour, .minute, .second], from: localDate)
  let year = number(bytes, at: 0, digits: 4)
  let hasExpectedYear =
    (year == 0 && components.era == 0 && components.year == 1)
    || (year > 0 && components.era == 1 && components.year == year)
  guard hasExpectedYear,
    components.month == number(bytes, at: 5, digits: 2),
    components.day == number(bytes, at: 8, digits: 2),
    components.hour == number(bytes, at: 11, digits: 2),
    components.minute == number(bytes, at: 14, digits: 2),
    components.second == number(bytes, at: 17, digits: 2)
  else { return nil }
  return date
}

private func validateRFC3339(_ value: String) throws {
  guard value.isEmpty || parseRFC3339(value) != nil else {
    throw TrawlClientError.invalidProtobuf
  }
}

func isCanonicalSourceRef(_ value: String, sourceID: String) -> Bool {
  !sourceID.isEmpty && value == value.trimmingCharacters(in: .whitespacesAndNewlines)
    && value.hasPrefix("\(sourceID):")
    && value.dropFirst(sourceID.count + 1).contains(where: { !$0.isWhitespace })
}

func isNonBlank(_ value: String) -> Bool {
  !value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
}

func isValidAnchorID(_ value: String) -> Bool {
  guard !value.isEmpty, value.utf8.count <= 128 else { return false }
  return value.utf8.allSatisfy {
    ($0 >= 65 && $0 <= 90) || ($0 >= 97 && $0 <= 122) || ($0 >= 48 && $0 <= 57)
      || $0 == 45 || $0 == 46 || $0 == 95
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
    guard isNonBlank(sourceID), isNonBlank(displayName) else {
      throw TrawlClientError.invalidProtobuf
    }
    return SourceManifest(
      sourceID: sourceID, displayName: displayName, branding: hasBranding ? branding.model() : nil,
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
extension Trawl_Federation_V1_TextRun {
  fileprivate func model() throws -> SearchTextRun {
    guard !text.isEmpty else { throw TrawlClientError.invalidProtobuf }
    return SearchTextRun(text: text, matched: matched)
  }
}
extension Trawl_Federation_V1_EvidenceFragment {
  fileprivate func model(sourceID: String) throws -> SearchEvidence {
    guard isNonBlank(label), let content else { throw TrawlClientError.invalidProtobuf }
    func runs(_ values: [Trawl_Federation_V1_TextRun]) throws -> [SearchTextRun] {
      let mapped = try values.map { try $0.model() }
      guard !mapped.isEmpty else {
        throw TrawlClientError.invalidProtobuf
      }
      return mapped
    }
    switch content {
    case .text(let value): return .text(label: label, runs: try runs(value.runs))
    case .field(let value):
      guard isNonBlank(value.name) else { throw TrawlClientError.invalidProtobuf }
      return .field(label: label, name: value.name, value: try runs(value.value))
    case .media(let value):
      guard value.resourceRef.isEmpty || isCanonicalSourceRef(value.resourceRef, sourceID: sourceID)
      else { throw TrawlClientError.invalidProtobuf }
      return .media(
        label: label, resourceRef: value.resourceRef,
        description: try runs(value.description_p))
    case .relation(let value):
      guard isNonBlank(value.relation) else { throw TrawlClientError.invalidProtobuf }
      return .relation(
        label: label, relation: value.relation, target: try runs(value.target))
    }
  }
}
extension Trawl_Federation_V1_SearchHit {
  fileprivate func model() throws -> SearchHit {
    guard !sourceID.isEmpty, isCanonicalSourceRef(openRef, sourceID: sourceID),
      isValidAnchorID(anchorID), hasSummary,
      isNonBlank(summary.title), !evidence.isEmpty
    else { throw TrawlClientError.invalidProtobuf }
    let date: Date?
    if timeRfc3339.isEmpty {
      date = nil
    } else if let parsed = parseRFC3339(timeRfc3339) {
      date = parsed
    } else {
      throw TrawlClientError.invalidProtobuf
    }
    return SearchHit(
      sourceID: sourceID, openRef: openRef, shortRef: shortRef, timeRFC3339: timeRfc3339,
      time: date, anchorID: anchorID,
      summary: ResultSummary(title: summary.title, subtitle: summary.subtitle),
      evidence: try evidence.map { try $0.model(sourceID: sourceID) }, allDay: allDay,
      availability: hasAvailability ? availability : nil, unread: hasUnread ? unread : nil)
  }
}
extension Trawl_Federation_V1_SearchSourceResult {
  fileprivate func model() throws -> SearchSourceResult {
    SearchSourceResult(
      sourceID: sourceID, displayName: displayName,
      whoResolved: hasWhoResolved ? whoResolved.model() : nil,
      hits: try hits.map { try $0.model() }, totalMatches: totalMatches, totalIsExact: totalIsExact,
      truncated: truncated)
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
