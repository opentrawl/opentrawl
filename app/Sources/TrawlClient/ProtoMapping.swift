import Foundation

extension Trawl_App_V1_OperationOutcome {
  fileprivate func model() throws -> OperationOutcome {
    switch self {
    case .complete:
      .complete
    case .partial:
      .partial
    case .failed:
      .failed
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_App_V1_FailureCode {
  fileprivate func model() throws -> SourceFailureCode {
    switch self {
    case .unavailable:
      .unavailable
    case .permission:
      .permission
    case .authentication:
      .authentication
    case .invalidInput:
      .invalidInput
    case .notFound:
      .notFound
    case .timeout:
      .timeout
    case .internal:
      .internalError
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_App_V1_SetupKind {
  fileprivate func model() throws -> SetupKind {
    switch self {
    case .fullDiskAccess:
      .fullDiskAccess
    case .photosPermission:
      .photosPermission
    case .account:
      .account
    case .pairing:
      .pairing
    case .archiveImport:
      .archiveImport
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_App_V1_SetupState {
  fileprivate func model() throws -> SetupState {
    switch self {
    case .ready:
      .ready
    case .needsAction:
      .needsAction
    case .unavailable:
      .unavailable
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_App_V1_SetupActionKind {
  fileprivate func model() throws -> SetupAction {
    switch self {
    case .none:
      .none
    case .openFullDiskAccess:
      .openFullDiskAccess
    case .requestPhotos:
      .requestPhotos
    case .runCommand:
      .runCommand
    case .chooseArchive:
      .chooseArchive
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_App_V1_SetupRequirement {
  fileprivate func model() throws -> SetupRequirement {
    guard !id.isEmpty else {
      throw TrawlClientError.invalidProtobuf
    }
    return try SetupRequirement(
      id: id,
      kind: kind.model(),
      state: state.model(),
      explanation: explanation,
      action: action.model(),
      command: command
    )
  }
}

extension Trawl_App_V1_SourceFailure {
  fileprivate func model() throws -> SourceFailure {
    return try SourceFailure(
      sourceID: appID,
      sourceName: surface,
      code: code.model(),
      message: message,
      remedy: remedy
    )
  }
}

extension Trawl_App_V1_SourceStatus {
  fileprivate func model() throws -> SourceStatus {
    guard !appID.isEmpty else {
      throw TrawlClientError.invalidProtobuf
    }
    let requirements = try setupRequirements.map { try $0.model() }
    return SourceStatus(
      id: appID,
      name: surface,
      state: state,
      summary: summary,
      counts: counts.map { SourceCount(id: $0.id, display: $0.display) },
      lastSyncedDisplay: lastSyncedDisplay,
      archiveBytes: archiveBytes,
      setupRequirements: requirements
    )
  }
}

extension Trawl_App_V1_SearchHit {
  fileprivate func model() throws -> SearchHit {
    guard !openRef.isEmpty, !appID.isEmpty else {
      throw TrawlClientError.invalidProtobuf
    }
    return SearchHit(
      id: openRef,
      sourceID: appID,
      title: title,
      snippet: snippet,
      whenDisplay: whenDisplay
    )
  }
}

extension Trawl_App_V1_StatusResponse {
  func model() throws -> StatusResponse {
    try StatusResponse(
      sources: sources.map { try $0.model() },
      failures: failures.map { try $0.model() },
      outcome: outcome.model()
    )
  }
}

extension Trawl_App_V1_SearchResponse {
  func model() throws -> SearchResponse {
    try SearchResponse(
      hits: hits.map { try $0.model() },
      failures: failures.map { try $0.model() },
      outcome: outcome.model(),
      resultLimit: resultLimit,
      truncated: truncated
    )
  }
}

extension Trawl_App_V1_SyncSourceResult {
  fileprivate func model() throws -> SyncSourceResult {
    guard !appID.isEmpty else {
      throw TrawlClientError.invalidProtobuf
    }
    return try SyncSourceResult(
      sourceID: appID,
      sourceName: surface,
      outcome: outcome.model(),
      failure: hasFailure ? failure.model() : nil
    )
  }
}

extension Trawl_App_V1_SyncResponse {
  func model() throws -> SyncResponse {
    try SyncResponse(
      sources: sources.map { try $0.model() },
      failures: failures.map { try $0.model() },
      outcome: outcome.model()
    )
  }
}

extension Trawl_App_V1_OpenResponse {
  func model() throws -> OpenResponse {
    try OpenResponse(
      outcome: outcome.model(),
      sourceID: appID,
      openRef: openRef,
      output: output,
      failure: hasFailure ? failure.model() : nil
    )
  }
}
