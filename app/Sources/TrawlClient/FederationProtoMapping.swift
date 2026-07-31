import Foundation

extension Trawl_Federation_V1_OperationOutcome {
  func decodedOperationOutcome() throws -> OperationOutcome {
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
  fileprivate func decodedTrawlerFailureCode() throws -> TrawlerFailureCode {
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
  func decodedTrawlerOperationFailure() throws -> TrawlerOperationFailure {
    TrawlerOperationFailure(
      failedTrawler: failedTrawler.decodedRegisteredTrawlerIdentity,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      failureCode: try failureCode.decodedTrawlerFailureCode(),
      failureMessage: failureMessage)
  }
}

extension Trawl_Federation_V1_TrawlerSkippedFromOperation {
  fileprivate func decodedTrawlerSkippedFromOperation() -> TrawlerSkippedFromOperation {
    TrawlerSkippedFromOperation(
      skippedTrawler: skippedTrawler.decodedRegisteredTrawlerIdentity,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      skipReason: skipReason)
  }
}

extension Trawl_Federation_V1_SharedTrawlerOperation {
  fileprivate func sharedTrawlerOperationForTrawlClient() throws -> SharedTrawlerOperation {
    switch self {
    case .metadata: .metadata
    case .status: .status
    case .sync: .sync
    case .search: .search
    case .open: .open
    case .who: .who
    case .conversations: .conversations
    case .messages: .messages
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_Federation_V1_TrawlerBranding {
  fileprivate func decodedTrawlerBranding() -> TrawlerBranding {
    TrawlerBranding(
      symbolName: symbolName,
      accentColor: accentColor,
      iconPath: iconPath,
      bundleIdentifier: bundleIdentifier,
      artworkBundleIdentifier: artworkBundleIdentifier)
  }
}

extension Trawl_Federation_V1_RegisteredTrawlerManifest {
  fileprivate func decodedRegisteredTrawlerManifest() throws -> RegisteredTrawlerManifest {
    let registeredTrawlerIdentity = registeredTrawler.decodedRegisteredTrawlerIdentity
    guard
      isNonBlank(registeredTrawlerIdentity.registeredTrawlerIdentity),
      isNonBlank(registeredTrawlerDisplayName)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return RegisteredTrawlerManifest(
      registeredTrawler: registeredTrawlerIdentity,
      registeredTrawlerCommandName: registeredTrawlerCommandName,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      registeredTrawlerAliases: registeredTrawlerAliases,
      trawlerBranding: hasTrawlerBranding ? trawlerBranding.decodedTrawlerBranding() : nil,
      trawlerCommandNamesShownInBareTrawlOverview:
        trawlerCommandNamesShownInBareTrawlOverview,
      supportedSharedTrawlerOperations:
        try supportedSharedTrawlerOperations.map {
          try $0.sharedTrawlerOperationForTrawlClient()
        })
  }
}

extension Trawl_Federation_V1_RegisteredTrawlerReleaseState {
  fileprivate func decodedRegisteredTrawlerReleaseState() throws
    -> RegisteredTrawlerReleaseState
  {
    switch self {
    case .available: .available
    case .comingSoon: .comingSoon
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_Federation_V1_RegisteredTrawlerCatalogEntry {
  fileprivate func decodedRegisteredTrawlerCatalogEntry() throws
    -> RegisteredTrawlerCatalogEntry
  {
    guard hasRegisteredTrawlerManifest else {
      throw TrawlClientError.invalidProtobuf
    }
    return RegisteredTrawlerCatalogEntry(
      registeredTrawlerManifest:
        try registeredTrawlerManifest.decodedRegisteredTrawlerManifest(),
      registeredTrawlerReleaseState:
        try registeredTrawlerReleaseState.decodedRegisteredTrawlerReleaseState(),
      registeredTrawlerIsEnabled: registeredTrawlerIsEnabled)
  }
}

extension Trawl_Federation_V1_FederatedTrawlerStatusOperation {
  func decodedStatusResponse() throws -> StatusResponse {
    let registeredTrawlerCatalog = try self.registeredTrawlerCatalog.map {
      try $0.decodedRegisteredTrawlerCatalogEntry()
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
          trawlerStatusResult.registeredTrawler.decodedRegisteredTrawlerIdentity
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
      operationFailures:
        try operationFailures.map { try $0.decodedTrawlerOperationFailure() },
      trawlersSkippedFromOperation:
        trawlersSkippedFromOperation.map { $0.decodedTrawlerSkippedFromOperation() },
      outcome: try outcome.decodedOperationOutcome(),
      registeredTrawlerCatalog: registeredTrawlerCatalog)
  }
}

extension Trawl_Federation_V1_SearchPersonFilterResolution {
  fileprivate func decodedSearchPersonFilterResolution() -> SearchPersonFilterResolution {
    SearchPersonFilterResolution(
      personFilterText: personFilterText,
      resolvedPersonIdentifiers: resolvedPersonIdentifiers)
  }
}

extension Trawl_Search_V1_SearchMatchPresentation {
  fileprivate func decodedSearchMatchPresentation() -> SearchMatchPresentation {
    SearchMatchPresentation(
      matchingRecordAssociatedTime:
        hasMatchingRecordAssociatedTime
        ? matchingRecordAssociatedTime.decodedArchiveRecordAssociatedTimeForDisplay() : nil,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      matchingRecordDisplayName: matchingRecordDisplayName,
      peopleRelatedToMatchingRecord:
        peopleRelatedToMatchingRecord.map { $0.decodedPersonRelatedToArchiveRecord() },
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
  fileprivate func decodedSearchMatch() throws -> SearchMatch {
    let globallyRoutableTrawlLink = trawlLink.decodedGloballyRoutableTrawlLink
    let matchingRecordAnchorIdentifier = recordAnchor.decodedRecordAnchorIdentifier
    guard
      hasSearchMatchPresentation,
      parseGloballyRoutableTrawlLink(globallyRoutableTrawlLink) != nil,
      isValidAnchorIdentifier(matchingRecordAnchorIdentifier)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return SearchMatch(
      trawlLink: globallyRoutableTrawlLink,
      recordAnchor: matchingRecordAnchorIdentifier,
      searchMatchPresentation: searchMatchPresentation.decodedSearchMatchPresentation())
  }
}

extension Trawl_Federation_V1_TrawlerSearchResult {
  fileprivate func decodedTrawlerSearchResult() throws -> TrawlerSearchResult {
    TrawlerSearchResult(
      registeredTrawler: registeredTrawler.decodedRegisteredTrawlerIdentity,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      searchPersonFilterResolution:
        hasSearchPersonFilterResolution
        ? searchPersonFilterResolution.decodedSearchPersonFilterResolution() : nil,
      searchMatchesFromTrawlerInDisplayOrder:
        try searchMatchesFromTrawlerInDisplayOrder.map { try $0.decodedSearchMatch() },
      totalSearchMatches: totalSearchMatches,
      totalSearchMatchesIsLowerBound: totalSearchMatchesIsLowerBound,
      moreSearchMatchesExist: moreSearchMatchesExist)
  }
}

extension Trawl_Federation_V1_FederatedTrawlerSearchOperation {
  func decodedSearchResponse() throws -> SearchResponse {
    SearchResponse(
      trawlerSearchResults:
        try trawlerSearchResults.map { try $0.decodedTrawlerSearchResult() },
      searchMatchesInDisplayOrder: try searchMatchesInDisplayOrder.map {
        try $0.decodedSearchMatch()
      },
      operationFailures:
        try operationFailures.map { try $0.decodedTrawlerOperationFailure() },
      trawlersSkippedFromOperation:
        trawlersSkippedFromOperation.map { $0.decodedTrawlerSkippedFromOperation() },
      outcome: try outcome.decodedOperationOutcome(),
      resultLimit: resultLimit,
      moreSearchMatchesExist: moreSearchMatchesExist)
  }
}

extension Trawl_Federation_V1_FederatedTrawlerArchiveSyncOperation {
  func decodedSyncResponse() throws -> SyncResponse {
    let trawlerArchiveSyncResults = self.trawlerArchiveSyncResults.map {
      TrawlerArchiveSyncResult(
        registeredTrawler: $0.registeredTrawler.decodedRegisteredTrawlerIdentity,
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
    let peopleArchiveUpdateFailuresAfterTrawlerArchiveSync =
      self.peopleArchiveUpdateFailuresAfterTrawlerArchiveSync.map {
        PeopleArchiveUpdateFailureAfterTrawlerArchiveSync(
          successfullySyncedTrawler:
            $0.successfullySyncedTrawler.decodedRegisteredTrawlerIdentity,
          successfullySyncedTrawlerDisplayName:
            $0.successfullySyncedTrawlerDisplayName)
      }
    return SyncResponse(
      trawlerArchiveSyncResults: trawlerArchiveSyncResults,
      operationFailures:
        try operationFailures.map { try $0.decodedTrawlerOperationFailure() },
      peopleArchiveUpdateFailuresAfterTrawlerArchiveSync:
        peopleArchiveUpdateFailuresAfterTrawlerArchiveSync,
      outcome: try outcome.decodedOperationOutcome())
  }
}
