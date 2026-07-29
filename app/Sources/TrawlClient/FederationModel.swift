import Foundation

public enum OperationOutcome: Sendable, Equatable {
  case complete
  case partial
  case failed
}

public enum TrawlerFailureCode: Sendable, Equatable {
  case unavailable
  case permission
  case authentication
  case invalidInput
  case notFound
  case timeout
  case internalError
  case cancelled
  case alreadySyncing
}

public struct TrawlerOperationFailure: Sendable, Equatable, Identifiable {
  public let registeredTrawlerManifestIdentity: String
  public let registeredTrawlerDisplayName: String
  public let failureCode: TrawlerFailureCode
  public let failureMessage: String

  public var id: String {
    "\(registeredTrawlerManifestIdentity):\(failureCode):\(failureMessage)"
  }
}

public struct TrawlerSkippedFromOperation: Sendable, Equatable, Identifiable {
  public let registeredTrawlerManifestIdentity: String
  public let registeredTrawlerDisplayName: String
  public let skipReason: String

  public var id: String { registeredTrawlerManifestIdentity }
}

public struct TrawlerBranding: Sendable, Equatable {
  public let symbolName: String
  public let accentColor: String
  public let iconPath: String
  public let bundleIdentifier: String
  public let artworkBundleIdentifier: String
}

public struct RegisteredTrawlerManifest: Sendable, Equatable {
  public let registeredTrawlerManifestIdentity: String
  public let registeredTrawlerCommandName: String
  public let registeredTrawlerDisplayName: String
  public let registeredTrawlerAliases: [String]
  public let trawlerBranding: TrawlerBranding?
  public let trawlerCommandNamesShownInBareTrawlOverview: [String]
  public let trawlerCapabilities: [String]
}

public enum RegisteredTrawlerReleaseState: Sendable, Equatable {
  case available
  case comingSoon
}

public struct RegisteredTrawlerCatalogEntry: Sendable, Equatable, Identifiable {
  public let registeredTrawlerManifest: RegisteredTrawlerManifest
  public let registeredTrawlerReleaseState: RegisteredTrawlerReleaseState
  public let registeredTrawlerIsEnabled: Bool

  public var id: String {
    registeredTrawlerManifest.registeredTrawlerManifestIdentity
  }
}

public struct ArchiveContentCountAfterLastSuccessfullyCompletedSync:
  Sendable, Equatable, Identifiable
{
  public let archiveContentKindName: String
  public let archiveContentKindDisplayName: String
  public let archiveContentCount: UInt64

  public var id: String { archiveContentKindName }
}

public struct TrawlerStatus: Sendable, Equatable, Identifiable {
  public let registeredTrawlerManifest: RegisteredTrawlerManifest
  public let archiveContentCountsAfterLastSuccessfullyCompletedSync:
    [ArchiveContentCountAfterLastSuccessfullyCompletedSync]
  public let lastSuccessfullyCompletedArchiveSyncTime: Date?
  public let trawlerArchiveCanAnswerCurrentCommands: Bool

  public var id: String {
    registeredTrawlerManifest.registeredTrawlerManifestIdentity
  }
}

public struct StatusResponse: Sendable, Equatable {
  public let trawlerStatuses: [TrawlerStatus]
  public let operationFailures: [TrawlerOperationFailure]
  public let trawlersSkippedFromOperation: [TrawlerSkippedFromOperation]
  public let outcome: OperationOutcome
  public let registeredTrawlerCatalog: [RegisteredTrawlerCatalogEntry]
}

public struct SearchPersonFilterResolution: Sendable, Equatable {
  public let personFilterText: String
  public let resolvedPersonIdentifiers: [String]
}

public struct SearchMatchTextFragment: Sendable, Equatable, Identifiable {
  public let searchMatchTextFragmentContent: String
  public let searchMatchTextFragmentMatchesSearchQuery: Bool
  public let displayOrder: Int

  public var id: Int { displayOrder }
}

public struct SearchMatchTextField: Sendable, Equatable, Identifiable {
  public let searchMatchTextFieldName: String
  public let searchMatchTextFragmentsInDisplayOrder: [SearchMatchTextFragment]
  public let displayOrder: Int

  public var id: Int { displayOrder }
}

public struct SearchMatchPresentation: Sendable, Equatable {
  public let matchingRecordAssociatedTime: ArchiveRecordAssociatedTimeForDisplay?
  public let registeredTrawlerDisplayName: String
  public let matchingRecordDisplayName: String
  public let peopleRelatedToMatchingRecord: [PersonRelatedToArchiveRecord]
  public let digitalContainerNamesNearestToBroadest: [String]
  public let physicalPlaceNamesSpecificToBroadest: [String]
  public let searchMatchTextFieldsInDisplayOrder: [SearchMatchTextField]
  public let matchingRecordKindDisplayName: String
}

public struct SearchMatchIdentifier: Sendable, Hashable {
  public let globallyRoutableTrawlLink: String
  public let matchingRecordAnchorIdentifier: String
}

public struct SearchMatch: Sendable, Equatable, Identifiable {
  public let globallyRoutableTrawlLink: String
  public let matchingRecordAnchorIdentifier: String
  public let searchMatchPresentation: SearchMatchPresentation

  public var id: SearchMatchIdentifier {
    SearchMatchIdentifier(
      globallyRoutableTrawlLink: globallyRoutableTrawlLink,
      matchingRecordAnchorIdentifier: matchingRecordAnchorIdentifier)
  }

  public var registeredTrawlerManifestIdentity: String {
    parseGloballyRoutableTrawlLink(globallyRoutableTrawlLink)?
      .registeredTrawlerManifestIdentity ?? ""
  }

  public var title: String {
    searchMatchPresentation.matchingRecordDisplayName
  }

  public var matchingRecordKindDisplayName: String {
    searchMatchPresentation.matchingRecordKindDisplayName
  }

  public var time: Date? {
    switch searchMatchPresentation.matchingRecordAssociatedTime {
    case .exactTime(let exactTime), .calendarDate(let exactTime):
      exactTime
    case nil:
      nil
    }
  }

  public var associatedTimeHasNoTimeOfDay: Bool {
    if case .calendarDate = searchMatchPresentation.matchingRecordAssociatedTime {
      return true
    }
    return false
  }
}

public struct TrawlerSearchResult: Sendable, Equatable {
  public let registeredTrawlerManifestIdentity: String
  public let registeredTrawlerDisplayName: String
  public let searchPersonFilterResolution: SearchPersonFilterResolution?
  public let searchMatchesFromTrawlerInDisplayOrder: [SearchMatch]
  public let totalSearchMatches: UInt64
  public let totalSearchMatchesIsLowerBound: Bool
  public let moreSearchMatchesExist: Bool
}

public struct SearchResponse: Sendable, Equatable {
  public static let maximumResults: UInt32 = 20
  public let trawlerSearchResults: [TrawlerSearchResult]
  public let searchMatchesInDisplayOrder: [SearchMatch]
  public let operationFailures: [TrawlerOperationFailure]
  public let trawlersSkippedFromOperation: [TrawlerSkippedFromOperation]
  public let outcome: OperationOutcome
  public let resultLimit: UInt32
  public let moreSearchMatchesExist: Bool
}
