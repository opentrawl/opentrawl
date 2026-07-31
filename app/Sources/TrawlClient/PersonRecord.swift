import Foundation

public enum PersonRecordAnchorIdentifier {
  public static let personDisplayName = RecordAnchorIdentifier(recordAnchorIdentifier: "person_display_name")
  public static let alternativePersonDisplayName = RecordAnchorIdentifier(recordAnchorIdentifier: "alternative_person_display_name")
  public static let emailAddress = RecordAnchorIdentifier(recordAnchorIdentifier: "email_address")
  public static let phoneNumber = RecordAnchorIdentifier(recordAnchorIdentifier: "phone_number")
  public static let postalAddress = RecordAnchorIdentifier(recordAnchorIdentifier: "postal_address")
  public static let accountIdentifier = RecordAnchorIdentifier(recordAnchorIdentifier: "account_identifier")
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

public struct PersonRecord: Sendable, Equatable {
  public let canonicalRecordReference: CanonicalArchiveRecordReference
  public let personDisplayName: String
  public let alternativePersonDisplayNames: [String]
  public let personContactMethodsInDisplayOrder: [PersonContactMethod]
  public let personFactContributingTrawlerDisplayNames: [String]
  public let personMessageCountsFromTrawlerArchives: [PersonMessageCountFromTrawlerArchive]
  public let messageCountInvolvingPersonAcrossTrawlers: UInt64

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
