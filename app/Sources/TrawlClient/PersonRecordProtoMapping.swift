extension Trawl_Person_PersonContactMethodKind {
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

extension Trawl_Person_PersonRoleInArchiveRecord {
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

extension Trawl_Person_PersonRelatedToArchiveRecord {
  func decodedPersonRelatedToArchiveRecord() -> PersonRelatedToArchiveRecord {
    PersonRelatedToArchiveRecord(
      personDisplayName: personDisplayName,
      personRoleInArchiveRecord: personRoleInArchiveRecord.decodedPersonRoleInArchiveRecord())
  }
}

extension Trawl_Person_PersonContactMethod {
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

extension Trawl_Person_PersonMessageCountFromTrawlerArchive {
  func decodedPersonMessageCountFromTrawlerArchive() throws
    -> PersonMessageCountFromTrawlerArchive
  {
    let registeredTrawler = registeredTrawler.decodedRegisteredTrawlerIdentity
    guard isNonBlank(registeredTrawler.registeredTrawlerIdentity),
      messageCountInvolvingPersonInTrawlerArchive > 0
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return PersonMessageCountFromTrawlerArchive(
      registeredTrawler: registeredTrawler,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName,
      messageCountInvolvingPersonInTrawlerArchive:
        messageCountInvolvingPersonInTrawlerArchive)
  }
}

extension Trawl_Person_TrawlerContributingFactsToPersonRecord {
  func decodedTrawlerContributingFactsToPersonRecord() throws
    -> TrawlerContributingFactsToPersonRecord
  {
    let registeredTrawler = registeredTrawler.decodedRegisteredTrawlerIdentity
    guard isNonBlank(registeredTrawler.registeredTrawlerIdentity),
      isNonBlank(registeredTrawlerDisplayName)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return TrawlerContributingFactsToPersonRecord(
      registeredTrawler: registeredTrawler,
      registeredTrawlerDisplayName: registeredTrawlerDisplayName)
  }
}

extension Trawl_Presentation_CalendarDate {
  func decodedCalendarDate() throws -> CalendarDate {
    guard calendarYear > 0,
      (1...12).contains(calendarMonthNumber),
      (1...31).contains(calendarDayOfMonth)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return CalendarDate(
      calendarYear: calendarYear,
      calendarMonthNumber: calendarMonthNumber,
      calendarDayOfMonth: calendarDayOfMonth)
  }
}

extension Trawl_Person_PersonRelationshipOrContextAnnotation {
  func decodedPersonRelationshipOrContextAnnotation() throws
    -> PersonRelationshipOrContextAnnotation
  {
    guard isNonBlank(personRelationshipOrContextDescription),
      hasPersonRelationshipOrContextDescriptionStatedDate
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return PersonRelationshipOrContextAnnotation(
      personRelationshipOrContextDescription: personRelationshipOrContextDescription,
      personRelationshipOrContextDescriptionStatedDate:
        try personRelationshipOrContextDescriptionStatedDate.decodedCalendarDate())
  }
}

extension Trawl_Person_PersonRecord {
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
      trawlersContributingFactsToPersonRecord:
        try trawlersContributingFactsToPersonRecord.map {
          try $0.decodedTrawlerContributingFactsToPersonRecord()
        },
      personMessageCountsFromTrawlerArchives:
        try personMessageCountsFromTrawlerArchives.map {
          try $0.decodedPersonMessageCountFromTrawlerArchive()
        },
      messageCountInvolvingPersonAcrossTrawlers:
        messageCountInvolvingPersonAcrossTrawlers,
      personRelationshipOrContextAnnotation:
        hasPersonRelationshipOrContextAnnotation
        ? try personRelationshipOrContextAnnotation
          .decodedPersonRelationshipOrContextAnnotation()
        : nil)
  }
}
