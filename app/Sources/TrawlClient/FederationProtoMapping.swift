import Foundation

extension Trawl_Federation_V1_OperationOutcome {
  func model() throws -> OperationOutcome {
    switch self {
    case .complete: .complete
    case .partial: .partial
    case .failed: .failed
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_Federation_V1_FailureCode {
  fileprivate func model() throws -> TrawlerFailureCode {
    switch self {
    case .unavailable: .unavailable
    case .permission: .permission
    case .authentication: .authentication
    case .invalidInput: .invalidInput
    case .notFound: .notFound
    case .timeout: .timeout
    case .internal: .internalError
    case .cancelled: .cancelled
    case .alreadySyncing: .alreadySyncing
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_Federation_V1_TrawlerOperationFailure {
  func model() throws -> TrawlerOperationFailure {
    TrawlerOperationFailure(
      registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      failureCode: try failureCode.model(),
      failureMessage: failureMessage)
  }
}

extension Trawl_Federation_V1_TrawlerSkippedFromOperation {
  fileprivate func model() -> TrawlerSkippedFromOperation {
    TrawlerSkippedFromOperation(
      registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      skipReason: skipReason)
  }
}

extension Trawl_Federation_V1_TrawlerBranding {
  fileprivate func model() -> TrawlerBranding {
    TrawlerBranding(
      symbolName: symbolName,
      accentColor: accentColor,
      iconPath: iconPath,
      bundleIdentifier: bundleIdentifier,
      artworkBundleIdentifier: artworkBundleIdentifier)
  }
}

extension Trawl_Federation_V1_RegisteredTrawlerManifest {
  fileprivate func model() throws -> RegisteredTrawlerManifest {
    guard
      isNonBlank(registeredTrawlerManifestIdentity),
      isNonBlank(registeredTrawlerDisplayName)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return RegisteredTrawlerManifest(
      registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity,
      registeredTrawlerCommandName: registeredTrawlerCommandName,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      registeredTrawlerAliases: registeredTrawlerAliases,
      trawlerBranding: hasTrawlerBranding ? trawlerBranding.model() : nil,
      trawlerCommandNamesShownInBareTrawlOverview:
        trawlerCommandNamesShownInBareTrawlOverview,
      trawlerCapabilities: trawlerCapabilities)
  }
}

extension Trawl_Federation_V1_RegisteredTrawlerReleaseState {
  fileprivate func model() throws -> RegisteredTrawlerReleaseState {
    switch self {
    case .available: .available
    case .comingSoon: .comingSoon
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_Federation_V1_RegisteredTrawlerCatalogEntry {
  fileprivate func model() throws -> RegisteredTrawlerCatalogEntry {
    guard hasRegisteredTrawlerManifest else {
      throw TrawlClientError.invalidProtobuf
    }
    return RegisteredTrawlerCatalogEntry(
      registeredTrawlerManifest: try registeredTrawlerManifest.model(),
      registeredTrawlerReleaseState: try registeredTrawlerReleaseState.model(),
      registeredTrawlerIsEnabled: registeredTrawlerIsEnabled)
  }
}

extension Trawl_Federation_V1_FederatedTrawlerStatusOperation {
  func model() throws -> StatusResponse {
    let registeredTrawlerCatalog = try self.registeredTrawlerCatalog.map {
      try $0.model()
    }
    let registeredTrawlerManifestsByIdentity = Dictionary(
      uniqueKeysWithValues: registeredTrawlerCatalog.map {
        ($0.id, $0.registeredTrawlerManifest)
      })
    let trawlerStatuses = try trawlerStatusResults.map {
      trawlerStatusResult -> TrawlerStatus in
      guard
        trawlerStatusResult.hasTrawlerStatusResponse,
        trawlerStatusResult.trawlerStatusResponse.hasTrawlerArchiveStatus,
        let registeredTrawlerManifest = registeredTrawlerManifestsByIdentity[
          trawlerStatusResult.registeredTrawlerManifestIdentity
        ]
      else {
        throw TrawlClientError.invalidProtobuf
      }
      let trawlerArchiveStatus =
        trawlerStatusResult.trawlerStatusResponse.trawlerArchiveStatus
      return TrawlerStatus(
        registeredTrawlerManifest: registeredTrawlerManifest,
        archiveContentCountsAfterLastSuccessfullyCompletedSync:
          trawlerArchiveStatus.archiveContentCountsAfterLastSuccessfullyCompletedSync.map {
            ArchiveContentCountAfterLastSuccessfullyCompletedSync(
              archiveContentKindName: $0.archiveContentKindName,
              archiveContentKindDisplayName: $0.archiveContentKindDisplayName,
              archiveContentCount: $0.archiveContentCount)
          },
        lastSuccessfullyCompletedArchiveSyncTime:
          trawlerArchiveStatus.hasLastSuccessfullyCompletedArchiveSyncTime
          ? trawlerArchiveStatus.lastSuccessfullyCompletedArchiveSyncTime.date : nil,
        trawlerArchiveCanAnswerCurrentCommands:
          trawlerArchiveStatus.trawlerArchiveCanAnswerCurrentCommands)
    }
    return StatusResponse(
      trawlerStatuses: trawlerStatuses,
      operationFailures: try operationFailures.map { try $0.model() },
      trawlersSkippedFromOperation: trawlersSkippedFromOperation.map { $0.model() },
      outcome: try outcome.model(),
      registeredTrawlerCatalog: registeredTrawlerCatalog)
  }
}

extension Trawl_Federation_V1_SearchPersonFilterResolution {
  fileprivate func model() -> SearchPersonFilterResolution {
    SearchPersonFilterResolution(
      personFilterText: personFilterText,
      resolvedPersonIdentifiers: resolvedPersonIdentifiers)
  }
}

extension Trawl_Search_V1_SearchMatchPresentation {
  fileprivate func model() -> SearchMatchPresentation {
    SearchMatchPresentation(
      matchingRecordAssociatedTime:
        hasMatchingRecordAssociatedTime ? matchingRecordAssociatedTime.model() : nil,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      matchingRecordDisplayName: matchingRecordDisplayName,
      peopleRelatedToMatchingRecord: peopleRelatedToMatchingRecord.map { $0.model() },
      digitalContainerNamesNearestToBroadest: digitalContainerNamesNearestToBroadest,
      physicalPlaceNamesSpecificToBroadest: physicalPlaceNamesSpecificToBroadest,
      searchMatchTextFieldsInDisplayOrder:
        searchMatchTextFieldsInDisplayOrder.enumerated().map { fieldIndex, field in
          SearchMatchTextField(
            searchMatchTextFieldName: field.searchMatchTextFieldName,
            searchMatchTextFragmentsInDisplayOrder:
              field.searchMatchTextFragmentsInDisplayOrder.enumerated().map {
                fragmentIndex, fragment in
                SearchMatchTextFragment(
                  searchMatchTextFragmentContent:
                    fragment.searchMatchTextFragmentContent,
                  searchMatchTextFragmentMatchesSearchQuery:
                    fragment.searchMatchTextFragmentMatchesSearchQuery,
                  displayOrder: fragmentIndex)
              },
            displayOrder: fieldIndex)
        },
      matchingRecordKindDisplayName: matchingRecordKindDisplayName)
  }
}

extension Trawl_Federation_V1_FederatedSearchMatch {
  fileprivate func model() throws -> SearchMatch {
    guard
      hasSearchMatchPresentation,
      parseGloballyRoutableTrawlLink(globallyRoutableTrawlLink) != nil,
      isValidAnchorIdentifier(matchingRecordAnchorIdentifier)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return SearchMatch(
      globallyRoutableTrawlLink: globallyRoutableTrawlLink,
      matchingRecordAnchorIdentifier: matchingRecordAnchorIdentifier,
      searchMatchPresentation: searchMatchPresentation.model())
  }
}

extension Trawl_Federation_V1_TrawlerSearchResult {
  fileprivate func model() throws -> TrawlerSearchResult {
    TrawlerSearchResult(
      registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      searchPersonFilterResolution:
        hasSearchPersonFilterResolution ? searchPersonFilterResolution.model() : nil,
      searchMatchesFromTrawlerInDisplayOrder:
        try searchMatchesFromTrawlerInDisplayOrder.map { try $0.model() },
      totalSearchMatches: totalSearchMatches,
      totalSearchMatchesIsLowerBound: totalSearchMatchesIsLowerBound,
      moreSearchMatchesExist: moreSearchMatchesExist)
  }
}

extension Trawl_Federation_V1_FederatedTrawlerSearchOperation {
  func model() throws -> SearchResponse {
    SearchResponse(
      trawlerSearchResults: try trawlerSearchResults.map { try $0.model() },
      searchMatchesInDisplayOrder: try searchMatchesInDisplayOrder.map {
        try $0.model()
      },
      operationFailures: try operationFailures.map { try $0.model() },
      trawlersSkippedFromOperation: trawlersSkippedFromOperation.map { $0.model() },
      outcome: try outcome.model(),
      resultLimit: resultLimit,
      moreSearchMatchesExist: moreSearchMatchesExist)
  }
}

extension Trawl_Federation_V1_FederatedTrawlerArchiveSyncOperation {
  func model() throws -> SyncResponse {
    let trawlerArchiveSyncResults = self.trawlerArchiveSyncResults.map {
      TrawlerArchiveSyncResult(
        registeredTrawlerManifestIdentity: $0.registeredTrawlerManifestIdentity,
        registeredTrawlerDisplayName: $0.registeredTrawlerDisplayName,
        archiveRecordCountAddedByThisSync:
          $0.trawlerArchiveSyncReport.hasArchiveRecordCountAddedByThisSync
          ? $0.trawlerArchiveSyncReport.archiveRecordCountAddedByThisSync : nil,
        archiveRecordCountUpdatedByThisSync:
          $0.trawlerArchiveSyncReport.hasArchiveRecordCountUpdatedByThisSync
          ? $0.trawlerArchiveSyncReport.archiveRecordCountUpdatedByThisSync : nil,
        archiveRecordCountRemovedByThisSync:
          $0.trawlerArchiveSyncReport.hasArchiveRecordCountRemovedByThisSync
          ? $0.trawlerArchiveSyncReport.archiveRecordCountRemovedByThisSync : nil)
    }
    return SyncResponse(
      trawlerArchiveSyncResults: trawlerArchiveSyncResults,
      operationFailures: try operationFailures.map { try $0.model() },
      outcome: try outcome.model())
  }
}
