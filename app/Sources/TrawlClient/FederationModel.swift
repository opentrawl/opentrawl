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
  case alreadyUpdating
}

public struct TrawlerOperationFailure: Sendable, Equatable, Identifiable {
  public let failedTrawler: RegisteredTrawlerIdentity
  public let registeredTrawlerDisplayName: String
  public let failureCode: TrawlerFailureCode
  public let failureMessage: String

  public var id: String {
    "\(failedTrawler.registeredTrawlerIdentity):\(failureCode):\(failureMessage)"
  }
}

public struct TrawlerSkippedFromOperation: Sendable, Equatable, Identifiable {
  public let skippedTrawler: RegisteredTrawlerIdentity
  public let registeredTrawlerDisplayName: String
  public let skipReason: String

  public var id: RegisteredTrawlerIdentity { skippedTrawler }
}

public struct TrawlerBranding: Sendable, Equatable {
  public let symbolName: String
  public let accentColor: String
  public let iconPath: String
  public let bundleIdentifier: String
  public let artworkBundleIdentifier: String
}

public struct TrawlerPrivacyBoundary: Sendable, Equatable {
  public let archiveContentReadByTrawler: String
  public let archiveContentThatLeavesMachine: String
  public let networkRequestsMadeByTrawler: String
}

public enum SharedTrawlerOperation: String, Sendable, Equatable {
  case metadata
  case status
  case update
  case search
  case open
  case who
  case conversations
  case messages
}

public enum RegisteredTrawlerCommand: Sendable, Equatable {
  case sharedTrawlerOperation(SharedTrawlerOperation)
  case bespokeTrawlerCommandName(String)
}

public enum RegisteredTrawlerCommandHelpPlacement: Sendable, Equatable {
  case listedInNormalTrawlerHelp
  case listedOnlyUnderMoreTrawlerCommands
  case hiddenFromHumanHelp
}

public struct RegisteredTrawlerCommandFlagDeclaration: Sendable, Equatable {
  public let trawlerCommandFlagName: String
  public let trawlerCommandFlagHelpDescription: String
  public let trawlerCommandFlagDefaultValue: String
}

public struct RegisteredTrawlerCommandDeclaration: Sendable, Equatable {
  public let registeredTrawlerCommand: RegisteredTrawlerCommand
  public let trawlerCommandHelpDescription: String
  public let trawlerCommandPositionalArgumentNames: [String]
  public let trawlerCommandFlagDeclarations: [RegisteredTrawlerCommandFlagDeclaration]
  public let trawlerCommandHelpPlacement: RegisteredTrawlerCommandHelpPlacement
  public let trawlerCommandIsShownInBareTrawlOverview: Bool
}

public struct RegisteredTrawlerManifest: Sendable, Equatable {
  public let registeredTrawler: RegisteredTrawlerIdentity
  public let registeredTrawlerCommandName: String
  public let registeredTrawlerDisplayName: String
  public let trawlerBranding: TrawlerBranding?
  public let registeredTrawlerAliases: [String]
  public let registeredTrawlerPrivacyBoundary: TrawlerPrivacyBoundary
  public let registeredTrawlerCommandDeclarations: [RegisteredTrawlerCommandDeclaration]
}

public enum RegisteredTrawlerReleaseState: Sendable, Equatable {
  case available
  case comingSoon
}

public struct RegisteredTrawlerCatalogEntry: Sendable, Equatable, Identifiable {
  public let registeredTrawlerManifest: RegisteredTrawlerManifest
  public let registeredTrawlerReleaseState: RegisteredTrawlerReleaseState
  public let registeredTrawlerIsEnabled: Bool

  public var id: RegisteredTrawlerIdentity {
    registeredTrawlerManifest.registeredTrawler
  }
}

public struct ArchiveContentCountAfterLastSuccessfullyCompletedUpdate:
  Sendable, Equatable, Identifiable
{
  public let archiveContentKindName: String
  public let archiveContentKindDisplayName: String
  public let archiveContentCount: UInt64

  public var id: String { archiveContentKindName }
}

public struct TrawlerStatus: Sendable, Equatable, Identifiable {
  public let registeredTrawlerManifest: RegisteredTrawlerManifest
  public let archiveContentCountsAfterLastSuccessfullyCompletedUpdate:
    [ArchiveContentCountAfterLastSuccessfullyCompletedUpdate]
  public let lastSuccessfullyCompletedArchiveUpdateTime: Date?
  public let trawlerArchiveCanAnswerCurrentCommands: Bool

  public var id: RegisteredTrawlerIdentity {
    registeredTrawlerManifest.registeredTrawler
  }
}

public struct FederatedTrawlerStatusOperation: Sendable, Equatable {
  public let trawlerStatuses: [TrawlerStatus]
  public let operationFailures: [TrawlerOperationFailure]
  public let trawlersSkippedFromOperation: [TrawlerSkippedFromOperation]
  public let outcome: OperationOutcome
  public let registeredTrawlerCatalog: [RegisteredTrawlerCatalogEntry]
}

public struct SearchPersonFilterResolution: Sendable, Equatable {
  public let personFilterText: String
  public let resolvedExactPersonFilterIdentifiers: [ExactPersonFilterIdentifier]
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
  public let trawlLink: GloballyRoutableTrawlLink
  public let recordAnchor: RecordAnchorIdentifier
}

public struct SearchMatch: Sendable, Equatable, Identifiable {
  public let trawlLink: GloballyRoutableTrawlLink
  public let recordAnchor: RecordAnchorIdentifier
  public let searchMatchPresentation: SearchMatchPresentation

  public var id: SearchMatchIdentifier {
    SearchMatchIdentifier(
      trawlLink: trawlLink,
      recordAnchor: recordAnchor)
  }

  public var registeredTrawler: RegisteredTrawlerIdentity? {
    parseGloballyRoutableTrawlLink(trawlLink)?.registeredTrawler
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
  public let registeredTrawler: RegisteredTrawlerIdentity
  public let registeredTrawlerDisplayName: String
  public let searchPersonFilterResolution: SearchPersonFilterResolution?
  public let searchMatchesFromTrawlerInDisplayOrder: [SearchMatch]
  public let totalSearchMatches: UInt64
  public let totalSearchMatchesIsLowerBound: Bool
  public let moreSearchMatchesExist: Bool
}

public struct TrawlArchiveSearchRequest: Sendable, Equatable {
  public static let defaultMaximumReturnedSearchMatchCount: UInt32 = 20

  public let searchQueryText: String
  public let onlySearchThisRegisteredTrawler: RegisteredTrawlerIdentity?
  public let earliestMatchingArchiveRecordTimeInclusive: Date?
  public let latestMatchingArchiveRecordTimeInclusive: Date?
  public let maximumReturnedSearchMatchCount: UInt32

  public init(
    searchQueryText: String = "",
    onlySearchThisRegisteredTrawler: RegisteredTrawlerIdentity? = nil,
    earliestMatchingArchiveRecordTimeInclusive: Date? = nil,
    latestMatchingArchiveRecordTimeInclusive: Date? = nil,
    maximumReturnedSearchMatchCount: UInt32 = Self.defaultMaximumReturnedSearchMatchCount
  ) {
    self.searchQueryText = searchQueryText
    self.onlySearchThisRegisteredTrawler = onlySearchThisRegisteredTrawler
    self.earliestMatchingArchiveRecordTimeInclusive = earliestMatchingArchiveRecordTimeInclusive
    self.latestMatchingArchiveRecordTimeInclusive = latestMatchingArchiveRecordTimeInclusive
    self.maximumReturnedSearchMatchCount = maximumReturnedSearchMatchCount
  }
}

public struct FederatedTrawlerSearchOperation: Sendable, Equatable {
  public let trawlerSearchResults: [TrawlerSearchResult]
  public let searchMatchesInDisplayOrder: [SearchMatch]
  public let operationFailures: [TrawlerOperationFailure]
  public let trawlersSkippedFromOperation: [TrawlerSkippedFromOperation]
  public let outcome: OperationOutcome
  public let resultLimit: UInt32
  public let moreSearchMatchesExist: Bool
}
