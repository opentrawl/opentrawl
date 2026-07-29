import Foundation

extension Trawl_Open_V1_OpenRecord {
  fileprivate func model(
    requestedGloballyRoutableTrawlLink: String
  ) throws -> OpenRecord {
    let registeredTrawlerManifestIdentity = self.registeredTrawlerManifestIdentity
    guard
      isCanonicalTrawlerRecordReference(
        canonicalOpenedRecordReference,
        registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity)
    else {
      throw TrawlClientError.invalidProtobuf
    }

    let openedRecordContent: OpenedRecordContent
    switch typedOpenedRecord {
    case .openedMessageRecordWithConversationContext(let openedMessage):
      openedRecordContent = .messageWithConversationContext(
        try openedMessage.model(
          canonicalOpenedRecordReference: canonicalOpenedRecordReference,
          registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity))
    case .conversationRecord(let conversationRecord):
      openedRecordContent = .conversation(
        try conversationRecord.model(
          canonicalOpenedRecordReference: canonicalOpenedRecordReference,
          registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity))
    case .personRecord(let personRecord):
      openedRecordContent = .person(
        try personRecord.model(
          canonicalOpenedRecordReference: canonicalOpenedRecordReference,
          registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity))
    case .calendarEventRecord(let calendarEventRecord):
      openedRecordContent = .calendarEvent(
        try calendarEventRecord.model(
          canonicalOpenedRecordReference: canonicalOpenedRecordReference,
          registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity))
    case .trawlerSpecificOpenedRecord(let trawlerSpecificOpenedRecord):
      openedRecordContent = .trawlerSpecificRecord(
        try trawlerSpecificOpenedRecord.model(
          canonicalOpenedRecordReference: canonicalOpenedRecordReference,
          requestedGloballyRoutableTrawlLink: requestedGloballyRoutableTrawlLink))
    case nil:
      throw TrawlClientError.invalidProtobuf
    }

    return OpenRecord(
      registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity,
      canonicalOpenedRecordReference: canonicalOpenedRecordReference,
      openedRecordContent: openedRecordContent)
  }
}

extension Trawl_Open_V1_OpenResponse {
  func model() throws -> OpenResponse {
    let operationOutcome = try outcome.model()
    let openedRecord =
      hasRecord
      ? try record.model(
        requestedGloballyRoutableTrawlLink: requestedGloballyRoutableTrawlLink)
      : nil
    let operationFailure = hasFailure ? try failure.model() : nil
    guard
      (operationOutcome == .complete && openedRecord != nil && operationFailure == nil
        && isValidAnchorIdentifier(requestedRecordAnchorIdentifier)
        && openedRecord?.openedRecordContent.containsAnchor(
          requestedRecordAnchorIdentifier) == true)
        || (operationOutcome == .failed && openedRecord == nil && operationFailure != nil)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return OpenResponse(
      outcome: operationOutcome,
      requestedGloballyRoutableTrawlLink: requestedGloballyRoutableTrawlLink,
      requestedRecordAnchorIdentifier: requestedRecordAnchorIdentifier,
      record: openedRecord,
      failure: operationFailure)
  }
}

extension Trawl_Presentation_V1_ArchiveRecordAssociatedTimeForDisplay {
  func model() -> ArchiveRecordAssociatedTimeForDisplay? {
    switch archiveRecordAssociatedTime {
    case .exactTime(let timestamp):
      .exactTime(timestamp.date)
    case .calendarDate(let calendarDate):
      Calendar(identifier: .gregorian).date(
        from: DateComponents(
          calendar: Calendar(identifier: .gregorian),
          timeZone: .gmt,
          year: Int(calendarDate.calendarYear),
          month: Int(calendarDate.calendarMonthNumber),
          day: Int(calendarDate.calendarDayOfMonth)
        )
      ).map(ArchiveRecordAssociatedTimeForDisplay.calendarDate)
    case nil:
      nil
    }
  }
}

extension Trawl_Message_V1_MessageRecord {
  fileprivate func model(
    registeredTrawlerManifestIdentity: String
  ) throws -> MessageRecord {
    guard
      isCanonicalTrawlerRecordReference(
        canonicalMessageRecordReferenceForGloballyRoutableTrawlLinkAssignment,
        registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return MessageRecord(
      messageTime: hasMessageTime ? messageTime.model() : nil,
      canonicalMessageRecordReferenceForGloballyRoutableTrawlLinkAssignment:
        canonicalMessageRecordReferenceForGloballyRoutableTrawlLinkAssignment,
      peopleRelatedToMessage: peopleRelatedToMessage.map { $0.model() },
      displayedMessageOrMediaText: displayedMessageOrMediaText,
      conversationDisplayContext: conversationDisplayContext)
  }
}

extension Trawl_Message_V1_MessageMedia {
  fileprivate func model() throws -> MessageMedia {
    MessageMedia(
      messageMediaKind: messageMediaKind,
      messageMediaTitle: messageMediaTitle,
      messageMediaByteCount: hasMessageMediaByteCount ? messageMediaByteCount : nil,
      messageMediaHTTPSURL: try validatedOptionalHTTPSURL(messageMediaHTTPSURL),
      messageMediaMetadataHTTPSURL: try validatedOptionalHTTPSURL(messageMediaMetadataHTTPSURL))
  }
}

extension Trawl_Message_V1_OpenedMessageRecordWithConversationContext {
  fileprivate func model(
    canonicalOpenedRecordReference: String,
    registeredTrawlerManifestIdentity: String
  ) throws -> OpenedMessageRecordWithConversationContext {
    let conversationContextMessageRecords = try
      conversationContextMessageRecordsInDisplayOrder.map {
        try $0.model(
          registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity)
      }
    let openedMessageCount = conversationContextMessageRecords.count {
      $0.canonicalMessageRecordReferenceForGloballyRoutableTrawlLinkAssignment
        == canonicalOpenedMessageRecordReference
    }
    guard
      canonicalOpenedMessageRecordReference == canonicalOpenedRecordReference,
      isValidAnchorIdentifier(openedMessageRecordFixedAnchorIdentifier),
      isCanonicalTrawlerRecordReference(
        canonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment,
        registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity),
      let conversationLinkRoute = parseGloballyRoutableTrawlLink(
        globallyRoutableTrawlLinkForConversationContainingOpenedMessage),
      conversationLinkRoute.registeredTrawlerManifestIdentity
        == registeredTrawlerManifestIdentity,
      openedMessageCount == 1
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return OpenedMessageRecordWithConversationContext(
      conversationDisplayName: conversationDisplayName,
      conversationParticipantDisplayNames: conversationParticipantDisplayNames,
      conversationContextMessageRecordsInDisplayOrder: conversationContextMessageRecords,
      canonicalOpenedMessageRecordReference: canonicalOpenedMessageRecordReference,
      openedMessageRecordFixedAnchorIdentifier: openedMessageRecordFixedAnchorIdentifier,
      earlierConversationContextMessagesOmitted: earlierConversationContextMessagesOmitted,
      laterConversationContextMessagesOmitted: laterConversationContextMessagesOmitted,
      canonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment:
        canonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment,
      openedMessageMedia: hasOpenedMessageMedia ? try openedMessageMedia.model() : nil,
      globallyRoutableTrawlLinkForConversationContainingOpenedMessage:
        globallyRoutableTrawlLinkForConversationContainingOpenedMessage)
  }
}

extension Trawl_Conversation_V1_ConversationRecord {
  fileprivate func model(
    canonicalOpenedRecordReference: String,
    registeredTrawlerManifestIdentity: String
  ) throws -> ConversationRecord {
    let canonicalConversationRecordReference =
      canonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment
    guard
      canonicalConversationRecordReference == canonicalOpenedRecordReference,
      isCanonicalTrawlerRecordReference(
        canonicalConversationRecordReference,
        registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return ConversationRecord(
      canonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment:
        canonicalConversationRecordReference,
      conversationDisplayName: conversationDisplayName,
      conversationParticipantIdentitiesObservedByTrawlerArchive:
        conversationParticipantIdentitiesObservedByTrawlerArchive.map {
          ConversationParticipantIdentityObservedByTrawlerArchive(
            personDisplayName: $0.personDisplayName,
            exactPersonFilterIdentifiersObservedByTrawlerArchive:
              $0.exactPersonFilterIdentifiersObservedByTrawlerArchive)
        },
      mostRecentConversationActivityTime:
        hasMostRecentConversationActivityTime ? mostRecentConversationActivityTime.date : nil,
      unreadMessageCount: hasUnreadMessageCount ? unreadMessageCount : nil,
      numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive:
        hasNumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive
        ? numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive : nil)
  }
}

extension Trawl_CalendarEvent_V1_CalendarEventAvailability {
  fileprivate func model() -> CalendarEventAvailability? {
    switch self {
    case .notSupported: .notSupported
    case .busy: .busy
    case .free: .free
    case .tentative: .tentative
    case .unavailable: .unavailable
    case .unknown: .unknown
    case .unspecified, .UNRECOGNIZED: nil
    }
  }
}

extension Trawl_CalendarEvent_V1_CalendarEventStatus {
  fileprivate func model() -> CalendarEventStatus? {
    switch self {
    case .confirmed: .confirmed
    case .tentative: .tentative
    case .cancelled: .cancelled
    case .unknown: .unknown
    case .unspecified, .UNRECOGNIZED: nil
    }
  }
}

extension Trawl_CalendarEvent_V1_CalendarEventAttendeeAttendanceStatus {
  fileprivate func model() -> CalendarEventAttendeeAttendanceStatus? {
    switch self {
    case .pending: .pending
    case .accepted: .accepted
    case .declined: .declined
    case .tentative: .tentative
    case .delegated: .delegated
    case .completed: .completed
    case .inProcess: .inProcess
    case .unknown: .unknown
    case .unspecified, .UNRECOGNIZED: nil
    }
  }
}

extension Trawl_CalendarEvent_V1_CalendarEventRecord {
  fileprivate func model(
    canonicalOpenedRecordReference: String,
    registeredTrawlerManifestIdentity: String
  ) throws -> CalendarEventRecord {
    let canonicalCalendarEventRecordReference =
      canonicalCalendarEventRecordReferenceForGloballyRoutableTrawlLinkAssignment
    guard
      canonicalCalendarEventRecordReference == canonicalOpenedRecordReference,
      isCanonicalTrawlerRecordReference(
        canonicalCalendarEventRecordReference,
        registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return CalendarEventRecord(
      canonicalCalendarEventRecordReferenceForGloballyRoutableTrawlLinkAssignment:
        canonicalCalendarEventRecordReference,
      calendarEventStartTime: hasCalendarEventStartTime ? calendarEventStartTime.model() : nil,
      calendarEventEndTime: hasCalendarEventEndTime ? calendarEventEndTime.model() : nil,
      calendarEventDisplayName: calendarEventDisplayName,
      calendarDisplayName: calendarDisplayName,
      calendarAccountDisplayName: calendarAccountDisplayName,
      calendarEventAvailability: calendarEventAvailability.model(),
      calendarEventLocation:
        hasCalendarEventLocation
        ? CalendarEventLocation(
          calendarEventLocationDisplayName:
            calendarEventLocation.calendarEventLocationDisplayName,
          calendarEventLocationAddress: calendarEventLocation.calendarEventLocationAddress)
        : nil,
      calendarEventOrganizer:
        hasCalendarEventOrganizer ? calendarEventOrganizer.model() : nil,
      calendarEventAttendees: calendarEventAttendees.map {
        CalendarEventAttendee(
          personRelatedToCalendarEvent: $0.personRelatedToCalendarEvent.model(),
          attendeeAttendanceStatus: $0.attendeeAttendanceStatus.model())
      },
      calendarEventHTTPSURL: try validatedOptionalHTTPSURL(calendarEventHTTPSURL),
      calendarEventStatus: calendarEventStatus.model(),
      calendarEventIsRecurring: calendarEventIsRecurring,
      calendarEventDescription: calendarEventDescription,
      calendarEventDescriptionIsTruncated: calendarEventDescriptionIsTruncated)
  }
}

extension Trawl_Open_V1_TrawlerSpecificOpenedRecord {
  fileprivate func model(
    canonicalOpenedRecordReference: String,
    requestedGloballyRoutableTrawlLink: String
  ) throws -> TrawlerSpecificOpenedRecord {
    guard
      hasTypedTrawlerSpecificOpenedRecord,
      isNonBlank(typedTrawlerSpecificOpenedRecord.typeURL),
      hasTrawlerSpecificOpenedRecordDetailPresentation
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return TrawlerSpecificOpenedRecord(
      typedTrawlerSpecificOpenedRecordTypeURL: typedTrawlerSpecificOpenedRecord.typeURL,
      typedTrawlerSpecificOpenedRecordData: typedTrawlerSpecificOpenedRecord.value,
      detailPresentation: try trawlerSpecificOpenedRecordDetailPresentation.model(
        canonicalOpenedRecordReference: canonicalOpenedRecordReference,
        requestedGloballyRoutableTrawlLink: requestedGloballyRoutableTrawlLink))
  }
}

extension Trawl_Presentation_V1_TrawlerSpecificCommandPresentationValue {
  fileprivate func model(
    canonicalOpenedRecordReference: String,
    requestedGloballyRoutableTrawlLink: String
  ) throws -> TrawlerSpecificCommandPresentationValue {
    switch typedValue {
    case .text(let text):
      return .text(text)
    case .unsignedCount(let unsignedCount):
      return .unsignedCount(unsignedCount)
    case .archiveRecordAssociatedTimeForDisplay(let associatedTime):
      guard let mappedTime = associatedTime.model() else {
        throw TrawlClientError.invalidProtobuf
      }
      return .archiveRecordAssociatedTime(mappedTime)
    case .canonicalRecordReferenceForGloballyRoutableTrawlLinkAssignment(
      let canonicalRecordReference):
      guard
        canonicalRecordReference == canonicalOpenedRecordReference,
        parseGloballyRoutableTrawlLink(requestedGloballyRoutableTrawlLink) != nil
      else {
        throw TrawlClientError.invalidProtobuf
      }
      return .globallyRoutableTrawlLink(requestedGloballyRoutableTrawlLink)
    case nil:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_Presentation_V1_TrawlerSpecificCommandDetailPresentation {
  fileprivate func model(
    canonicalOpenedRecordReference: String,
    requestedGloballyRoutableTrawlLink: String
  ) throws -> TrawlerSpecificCommandDetailPresentation {
    let mappedBody: TrawlerSpecificCommandDetailPresentationBody?
    switch body {
    case .bodyText(let bodyText):
      mappedBody = .text(bodyText)
    case .bodyUnavailableExplanation(let bodyUnavailableExplanation):
      mappedBody = .unavailableExplanation(bodyUnavailableExplanation)
    case nil:
      mappedBody = nil
    }
    return TrawlerSpecificCommandDetailPresentation(
      detailDisplayName: detailDisplayName,
      detailDisplayNameFixedAnchorIdentifier:
        hasDetailDisplayNameFixedAnchorIdentifier
        ? detailDisplayNameFixedAnchorIdentifier : nil,
      fieldsInDisplayOrder: try fieldsInDisplayOrder.map { field in
        guard field.hasFieldValue else {
          throw TrawlClientError.invalidProtobuf
        }
        return TrawlerSpecificCommandDetailPresentationField(
          fieldDisplayName: field.fieldDisplayName,
          fieldValue: try field.fieldValue.model(
            canonicalOpenedRecordReference: canonicalOpenedRecordReference,
            requestedGloballyRoutableTrawlLink: requestedGloballyRoutableTrawlLink),
          fieldFixedAnchorIdentifier:
            field.hasFieldFixedAnchorIdentifier ? field.fieldFixedAnchorIdentifier : nil)
      },
      body: mappedBody,
      bodyFixedAnchorIdentifier:
        hasBodyFixedAnchorIdentifier ? bodyFixedAnchorIdentifier : nil)
  }
}

private func validatedOptionalHTTPSURL(_ value: String) throws -> URL? {
  guard !value.isEmpty else { return nil }
  guard
    let url = URL(string: value),
    url.scheme?.lowercased() == "https",
    url.host() != nil
  else {
    throw TrawlClientError.invalidProtobuf
  }
  return url
}
