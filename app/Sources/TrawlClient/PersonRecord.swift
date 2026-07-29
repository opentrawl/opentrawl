import Foundation

public enum PersonRecordAnchorIdentifier {
  public static let personDisplayName = "person_display_name"
  public static let alternativePersonDisplayName = "alternative_person_display_name"
  public static let emailAddress = "email_address"
  public static let phoneNumber = "phone_number"
  public static let postalAddress = "postal_address"
  public static let accountIdentifier = "account_identifier"
}

public enum PersonContactMethodKind: Sendable, Equatable, Hashable {
  case emailAddress
  case phoneNumber
  case postalAddress
  case accountIdentifier

  public var recordAnchorIdentifier: String {
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

public struct PersonRecord: Sendable, Equatable {
  public let canonicalPersonRecordReferenceForGloballyRoutableTrawlLinkAssignment: String
  public let personDisplayName: String
  public let alternativePersonDisplayNames: [String]
  public let personContactMethodsInDisplayOrder: [PersonContactMethod]
  public let personFactContributingTrawlerDisplayNames: [String]

  func containsAnchor(_ wantedAnchorIdentifier: String) -> Bool {
    switch wantedAnchorIdentifier {
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
