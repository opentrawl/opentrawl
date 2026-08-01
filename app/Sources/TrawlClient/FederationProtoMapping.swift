import Foundation

extension Trawl_Federation_OperationOutcome {
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

extension Trawl_Federation_FailureCode {
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
    case .alreadyUpdating: .alreadyUpdating
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_Federation_TrawlerOperationFailure {
  func decodedTrawlerOperationFailure() throws -> TrawlerOperationFailure {
    TrawlerOperationFailure(
      failedTrawler: failedTrawler.decodedRegisteredTrawlerIdentity,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      failureCode: try failureCode.decodedTrawlerFailureCode(),
      failureMessage: failureMessage)
  }
}

extension Trawl_Federation_TrawlerSkippedFromOperation {
  fileprivate func decodedTrawlerSkippedFromOperation() -> TrawlerSkippedFromOperation {
    TrawlerSkippedFromOperation(
      skippedTrawler: skippedTrawler.decodedRegisteredTrawlerIdentity,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      skipReason: skipReason)
  }
}

extension Trawl_Federation_SharedTrawlerOperation {
  fileprivate func decodedSharedTrawlerOperation() throws -> SharedTrawlerOperation {
    switch self {
    case .metadata: .metadata
    case .status: .status
    case .update: .update
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

extension Trawl_Federation_TrawlerBranding {
  fileprivate func decodedTrawlerBranding() -> TrawlerBranding {
    TrawlerBranding(
      symbolName: symbolName,
      accentColor: accentColor,
      iconPath: iconPath,
      bundleIdentifier: bundleIdentifier,
      artworkBundleIdentifier: artworkBundleIdentifier)
  }
}

extension Trawl_Federation_TrawlerPrivacyBoundary {
  fileprivate func decodedTrawlerPrivacyBoundary() -> TrawlerPrivacyBoundary {
    TrawlerPrivacyBoundary(
      archiveContentReadByTrawler: archiveContentReadByTrawler,
      archiveContentThatLeavesMachine: archiveContentThatLeavesMachine,
      networkRequestsMadeByTrawler: networkRequestsMadeByTrawler)
  }
}

extension Trawl_Federation_RegisteredTrawlerCommandHelpPlacement {
  fileprivate func decodedRegisteredTrawlerCommandHelpPlacement() throws
    -> RegisteredTrawlerCommandHelpPlacement
  {
    switch self {
    case .listedInNormalTrawlerHelp: .listedInNormalTrawlerHelp
    case .listedOnlyUnderMoreTrawlerCommands: .listedOnlyUnderMoreTrawlerCommands
    case .hiddenFromHumanHelp: .hiddenFromHumanHelp
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_Federation_RegisteredTrawlerCommandFlagDeclaration {
  fileprivate func decodedRegisteredTrawlerCommandFlagDeclaration()
    -> RegisteredTrawlerCommandFlagDeclaration
  {
    RegisteredTrawlerCommandFlagDeclaration(
      trawlerCommandFlagName: trawlerCommandFlagName,
      trawlerCommandFlagHelpDescription: trawlerCommandFlagHelpDescription,
      trawlerCommandFlagDefaultValue: trawlerCommandFlagDefaultValue)
  }
}

extension Trawl_Federation_RegisteredTrawlerCommandDeclaration {
  fileprivate func decodedRegisteredTrawlerCommandDeclaration() throws
    -> RegisteredTrawlerCommandDeclaration
  {
    let decodedRegisteredTrawlerCommand: RegisteredTrawlerCommand
    switch registeredTrawlerCommand {
    case .sharedTrawlerOperation(let sharedTrawlerOperation):
      decodedRegisteredTrawlerCommand = .sharedTrawlerOperation(
        try sharedTrawlerOperation.decodedSharedTrawlerOperation())
    case .bespokeTrawlerCommandName(let bespokeTrawlerCommandName):
      decodedRegisteredTrawlerCommand = .bespokeTrawlerCommandName(bespokeTrawlerCommandName)
    case nil:
      throw TrawlClientError.invalidProtobuf
    }
    return RegisteredTrawlerCommandDeclaration(
      registeredTrawlerCommand: decodedRegisteredTrawlerCommand,
      trawlerCommandHelpDescription: trawlerCommandHelpDescription,
      trawlerCommandPositionalArgumentNames: trawlerCommandPositionalArgumentNames,
      trawlerCommandFlagDeclarations: trawlerCommandFlagDeclarations.map {
        $0.decodedRegisteredTrawlerCommandFlagDeclaration()
      },
      trawlerCommandHelpPlacement:
        try trawlerCommandHelpPlacement.decodedRegisteredTrawlerCommandHelpPlacement(),
      trawlerCommandIsShownInBareTrawlOverview: trawlerCommandIsShownInBareTrawlOverview)
  }
}

extension Trawl_Federation_RegisteredTrawlerManifest {
  fileprivate func decodedRegisteredTrawlerManifest() throws -> RegisteredTrawlerManifest {
    let registeredTrawlerIdentity = registeredTrawler.decodedRegisteredTrawlerIdentity
    guard
      isNonBlank(registeredTrawlerIdentity.registeredTrawlerIdentity),
      isNonBlank(registeredTrawlerDisplayName),
      hasRegisteredTrawlerPrivacyBoundary
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return RegisteredTrawlerManifest(
      registeredTrawler: registeredTrawlerIdentity,
      registeredTrawlerCommandName: registeredTrawlerCommandName,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      trawlerBranding: hasTrawlerBranding ? trawlerBranding.decodedTrawlerBranding() : nil,
      registeredTrawlerAliases: registeredTrawlerAliases,
      registeredTrawlerPrivacyBoundary:
        registeredTrawlerPrivacyBoundary.decodedTrawlerPrivacyBoundary(),
      registeredTrawlerCommandDeclarations:
        try registeredTrawlerCommandDeclarations.map {
          try $0.decodedRegisteredTrawlerCommandDeclaration()
        })
  }
}

extension Trawl_Federation_RegisteredTrawlerReleaseState {
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

extension Trawl_Federation_RegisteredTrawlerCatalogEntry {
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

extension Trawl_Federation_FederatedTrawlerStatusOperation {
  func decodedFederatedTrawlerStatusOperation() throws -> FederatedTrawlerStatusOperation {
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
        archiveContentCountsAfterLastSuccessfullyCompletedUpdate:
          trawlerArchiveStatus.archiveContentCountsAfterLastSuccessfullyCompletedUpdate.map {
            ArchiveContentCountAfterLastSuccessfullyCompletedUpdate(
              archiveContentKindName: $0.archiveContentKindName,
              archiveContentKindDisplayName: $0.archiveContentKindDisplayName,
              archiveContentCount: $0.archiveContentCount)
          },
        lastSuccessfullyCompletedArchiveUpdateTime:
          trawlerArchiveStatus.hasLastSuccessfullyCompletedArchiveUpdateTime
          ? trawlerArchiveStatus.lastSuccessfullyCompletedArchiveUpdateTime.date : nil,
        trawlerArchiveCanAnswerCurrentCommands:
          trawlerArchiveStatus.trawlerArchiveCanAnswerCurrentCommands)
    }
    return FederatedTrawlerStatusOperation(
      trawlerStatuses: trawlerStatuses,
      operationFailures:
        try operationFailures.map { try $0.decodedTrawlerOperationFailure() },
      trawlersSkippedFromOperation:
        trawlersSkippedFromOperation.map { $0.decodedTrawlerSkippedFromOperation() },
      outcome: try outcome.decodedOperationOutcome(),
      registeredTrawlerCatalog: registeredTrawlerCatalog)
  }
}

extension Trawl_Federation_SearchPersonFilterResolution {
  fileprivate func decodedSearchPersonFilterResolution() -> SearchPersonFilterResolution {
    SearchPersonFilterResolution(
      personFilterText: personFilterText,
      resolvedPersonIdentifiers: resolvedPersonIdentifiers)
  }
}

extension Trawl_Search_SearchMatchPresentation {
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

extension Trawl_Federation_FederatedSearchMatch {
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

extension Trawl_Federation_TrawlerSearchResult {
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

extension Trawl_Federation_FederatedTrawlerSearchOperation {
  func decodedFederatedTrawlerSearchOperation() throws -> FederatedTrawlerSearchOperation {
    FederatedTrawlerSearchOperation(
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

extension Trawl_Federation_FederatedTrawlerArchiveUpdateOperation {
  func decodedFederatedTrawlerArchiveUpdateOperation() throws
    -> FederatedTrawlerArchiveUpdateOperation
  {
    let trawlerArchiveUpdateResults = self.trawlerArchiveUpdateResults.map {
      TrawlerArchiveUpdateResult(
        registeredTrawler: $0.registeredTrawler.decodedRegisteredTrawlerIdentity,
        registeredTrawlerDisplayName: $0.registeredTrawlerDisplayName,
        archiveRecordCountAddedByThisUpdate:
          $0.trawlerArchiveUpdateReport.hasArchiveRecordCountAddedByThisUpdate
          ? $0.trawlerArchiveUpdateReport.archiveRecordCountAddedByThisUpdate : nil,
        archiveRecordCountUpdatedByThisUpdate:
          $0.trawlerArchiveUpdateReport.hasArchiveRecordCountUpdatedByThisUpdate
          ? $0.trawlerArchiveUpdateReport.archiveRecordCountUpdatedByThisUpdate : nil,
        archiveRecordCountRemovedByThisUpdate:
          $0.trawlerArchiveUpdateReport.hasArchiveRecordCountRemovedByThisUpdate
          ? $0.trawlerArchiveUpdateReport.archiveRecordCountRemovedByThisUpdate : nil)
    }
    let peopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate =
      self.peopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate.map {
        PeopleArchiveUpdateFailureAfterTrawlerArchiveUpdate(
          successfullyUpdatedTrawler:
            $0.successfullyUpdatedTrawler.decodedRegisteredTrawlerIdentity,
          successfullyUpdatedTrawlerDisplayName:
            $0.successfullyUpdatedTrawlerDisplayName)
      }
    return FederatedTrawlerArchiveUpdateOperation(
      trawlerArchiveUpdateResults: trawlerArchiveUpdateResults,
      operationFailures:
        try operationFailures.map { try $0.decodedTrawlerOperationFailure() },
      peopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate:
        peopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate,
      outcome: try outcome.decodedOperationOutcome())
  }
}
