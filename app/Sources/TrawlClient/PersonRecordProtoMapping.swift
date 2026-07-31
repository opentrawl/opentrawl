extension Trawl_Person_V1_PersonContactMethodKind {
  func decodedPersonContactMethodKind() throws -> PersonContactMethodKind {
    switch self {
    case .emailAddress: .emailAddress
    case .phoneNumber: .phoneNumber
    case .postalAddress: .postalAddress
    case .accountIdentifier: .accountIdentifier
    case .unspecified, .UNRECOGNIZED:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_Person_V1_PersonRoleInArchiveRecord {
  func decodedPersonRoleInArchiveRecord() -> PersonRoleInArchiveRecord? {
    switch self {
    case .sender: .sender
    case .recipient: .recipient
    case .author: .author
    case .organizer: .organizer
    case .attendee: .attendee
    case .participant: .participant
    case .unspecified, .UNRECOGNIZED: nil
    }
  }
}

extension Trawl_Person_V1_PersonRelatedToArchiveRecord {
  func decodedPersonRelatedToArchiveRecord() -> PersonRelatedToArchiveRecord {
    PersonRelatedToArchiveRecord(
      personDisplayName: personDisplayName,
      personRoleInArchiveRecord: personRoleInArchiveRecord.decodedPersonRoleInArchiveRecord())
  }
}

extension Trawl_Person_V1_PersonContactMethod {
  func decodedPersonContactMethod() throws -> PersonContactMethod {
    guard isNonBlank(personContactMethodDisplayValue) else {
      throw TrawlClientError.invalidProtobuf
    }
    return PersonContactMethod(
      personContactMethodKind: try personContactMethodKind.decodedPersonContactMethodKind(),
      personContactMethodLabel: personContactMethodLabel,
      personContactMethodDisplayValue: personContactMethodDisplayValue)
  }
}

extension Trawl_Person_V1_PersonRecord {
  func decodedPersonRecord(
    canonicalOpenedRecordReference: CanonicalArchiveRecordReference,
    registeredTrawler: RegisteredTrawlerIdentity
  ) throws -> PersonRecord {
    let canonicalPersonRecordReference =
      self.canonicalRecordReference.decodedCanonicalArchiveRecordReference
    guard canonicalPersonRecordReference == canonicalOpenedRecordReference,
      isCanonicalTrawlerRecordReference(
        canonicalPersonRecordReference,
        registeredTrawler: registeredTrawler),
      isNonBlank(personDisplayName)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return PersonRecord(
      canonicalRecordReference: canonicalPersonRecordReference,
      personDisplayName: personDisplayName,
      alternativePersonDisplayNames: alternativePersonDisplayNames,
      personContactMethodsInDisplayOrder: try personContactMethodsInDisplayOrder.map {
        try $0.decodedPersonContactMethod()
      },
      personFactContributingTrawlerDisplayNames:
        personFactContributingTrawlerDisplayNames)
  }
}
