import Foundation

public enum PersonRecordAnchorIdentifier {
  public static let personDisplayName = RecordAnchorIdentifier(
    recordAnchorIdentifier: "person_display_name")
  public static let alternativePersonDisplayName = RecordAnchorIdentifier(
    recordAnchorIdentifier: "alternative_person_display_name")
  public static let emailAddress = RecordAnchorIdentifier(recordAnchorIdentifier: "email_address")
  public static let phoneNumber = RecordAnchorIdentifier(recordAnchorIdentifier: "phone_number")
  public static let postalAddress = RecordAnchorIdentifier(recordAnchorIdentifier: "postal_address")
  public static let accountIdentifier = RecordAnchorIdentifier(
    recordAnchorIdentifier: "account_identifier")
  public static let personRelationshipOrContextDescription = RecordAnchorIdentifier(
    recordAnchorIdentifier: "person_relationship_or_context_description")
}

public enum PersonContactMethodKind: Sendable, Equatable, Hashable {
  case emailAddress
  case phoneNumber
  case postalAddress
  case accountIdentifier

  public var recordAnchor: RecordAnchorIdentifier {
    switch self {
    case .emailAddress: PersonRecordAnchorIdentifier.emailAddress
    case .phoneNumber: PersonRecordAnchorIdentifier.phoneNumber
    case .postalAddress: PersonRecordAnchorIdentifier.postalAddress
    case .accountIdentifier: PersonRecordAnchorIdentifier.accountIdentifier
    }
  }
}

public enum PersonRoleInArchiveRecord: Sendable, Equatable {
  case sender
  case recipient
  case author
  case organizer
  case attendee
  case participant
}

public struct PersonRelatedToArchiveRecord: Sendable, Equatable {
  public let personDisplayName: String
  public let personRoleInArchiveRecord: PersonRoleInArchiveRecord?
}

public struct PersonContactMethod: Sendable, Equatable, Hashable, Identifiable {
  public let personContactMethodKind: PersonContactMethodKind
  public let personContactMethodLabel: String
  public let personContactMethodDisplayValue: String

  public var id: Self { self }
}

public struct PersonMessageCountFromTrawlerArchive: Sendable, Equatable {
  public let registeredTrawler: RegisteredTrawlerIdentity
  public let registeredTrawlerDisplayName: String
  public let messageCountInvolvingPersonInTrawlerArchive: UInt64
}

public struct TrawlerContributingFactsToPersonRecord: Sendable, Equatable {
  public let registeredTrawler: RegisteredTrawlerIdentity
  public let registeredTrawlerDisplayName: String
}

public struct CalendarDate: Sendable, Equatable {
  public let calendarYear: Int32
  public let calendarMonthNumber: Int32
  public let calendarDayOfMonth: Int32
}

public struct PersonRelationshipOrContextAnnotation: Sendable, Equatable {
  public let personRelationshipOrContextDescription: String
  public let personRelationshipOrContextDescriptionStatedDate: CalendarDate
}

public struct PersonRecord: Sendable, Equatable {
  public let canonicalRecordReference: CanonicalArchiveRecordReference
  public let personDisplayName: String
  public let alternativePersonDisplayNames: [String]
  public let personContactMethodsInDisplayOrder: [PersonContactMethod]
  public let trawlersContributingFactsToPersonRecord: [TrawlerContributingFactsToPersonRecord]
  public let personMessageCountsFromTrawlerArchives: [PersonMessageCountFromTrawlerArchive]
  public let messageCountInvolvingPersonAcrossTrawlers: UInt64
  public let personRelationshipOrContextAnnotation: PersonRelationshipOrContextAnnotation?

  func containsAnchor(_ wantedAnchor: RecordAnchorIdentifier) -> Bool {
    switch wantedAnchor {
    case PersonRecordAnchorIdentifier.personDisplayName:
      isNonBlank(personDisplayName)
    case PersonRecordAnchorIdentifier.alternativePersonDisplayName:
      !alternativePersonDisplayNames.isEmpty
    case PersonRecordAnchorIdentifier.emailAddress:
      containsContactMethod(.emailAddress)
    case PersonRecordAnchorIdentifier.phoneNumber:
      containsContactMethod(.phoneNumber)
    case PersonRecordAnchorIdentifier.postalAddress:
      containsContactMethod(.postalAddress)
    case PersonRecordAnchorIdentifier.accountIdentifier:
      containsContactMethod(.accountIdentifier)
    case PersonRecordAnchorIdentifier.personRelationshipOrContextDescription:
      personRelationshipOrContextAnnotation.map {
        isNonBlank($0.personRelationshipOrContextDescription)
      } == true
    default:
      false
    }
  }

  private func containsContactMethod(_ wantedKind: PersonContactMethodKind) -> Bool {
    personContactMethodsInDisplayOrder.contains {
      $0.personContactMethodKind == wantedKind
    }
  }
}
