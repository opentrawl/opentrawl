extension Trawl_Person_V1_PersonContactMethodKind {
  func model() throws -> PersonContactMethodKind {
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
  func model() -> PersonRoleInArchiveRecord? {
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
  func model() -> PersonRelatedToArchiveRecord {
    PersonRelatedToArchiveRecord(
      personDisplayName: personDisplayName,
      personRoleInArchiveRecord: personRoleInArchiveRecord.model())
  }
}

extension Trawl_Person_V1_PersonContactMethod {
  func model() throws -> PersonContactMethod {
    guard isNonBlank(personContactMethodDisplayValue) else {
      throw TrawlClientError.invalidProtobuf
    }
    return PersonContactMethod(
      personContactMethodKind: try personContactMethodKind.model(),
      personContactMethodLabel: personContactMethodLabel,
      personContactMethodDisplayValue: personContactMethodDisplayValue)
  }
}

extension Trawl_Person_V1_PersonRecord {
  func model(
    canonicalOpenedRecordReference: String,
    registeredTrawlerManifestIdentity: String
  ) throws -> PersonRecord {
    let canonicalPersonRecordReference =
      canonicalPersonRecordReferenceForGloballyRoutableTrawlLinkAssignment
    guard canonicalPersonRecordReference == canonicalOpenedRecordReference,
      isCanonicalTrawlerRecordReference(
        canonicalPersonRecordReference,
        registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity),
      isNonBlank(personDisplayName)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return PersonRecord(
      canonicalPersonRecordReferenceForGloballyRoutableTrawlLinkAssignment:
        canonicalPersonRecordReference,
      personDisplayName: personDisplayName,
      alternativePersonDisplayNames: alternativePersonDisplayNames,
      personContactMethodsInDisplayOrder: try personContactMethodsInDisplayOrder.map {
        try $0.model()
      },
      personFactContributingTrawlerDisplayNames:
        personFactContributingTrawlerDisplayNames)
  }
}
