import Foundation

extension Trawl_Open_OpenRecord {
  fileprivate func decodedOpenRecord(
    requestedTrawlLink: GloballyRoutableTrawlLink
  ) throws -> OpenRecord {
    let registeredTrawler = recordTrawler.decodedRegisteredTrawlerIdentity
    let canonicalOpenedRecordReference =
      canonicalRecordReference.decodedCanonicalArchiveRecordReference
    guard
      isCanonicalTrawlerRecordReference(
        canonicalOpenedRecordReference,
        registeredTrawler: registeredTrawler)
    else {
      throw TrawlClientError.invalidProtobuf
    }

    let openedRecordContent: OpenedRecordContent
    switch typedOpenedRecord {
    case .openedMessageRecordWithConversationContext(let openedMessage):
      openedRecordContent = .messageWithConversationContext(
        try openedMessage.decodedOpenedMessageRecordWithConversationContext(
          canonicalOpenedRecordReference: canonicalOpenedRecordReference,
          registeredTrawler: registeredTrawler))
    case .conversationRecord(let conversationRecord):
      openedRecordContent = .conversation(
        try conversationRecord.decodedConversationRecord(
          canonicalOpenedRecordReference: canonicalOpenedRecordReference,
          registeredTrawler: registeredTrawler))
    case .personRecord(let personRecord):
      openedRecordContent = .person(
        try personRecord.decodedPersonRecord(
          canonicalOpenedRecordReference: canonicalOpenedRecordReference,
          registeredTrawler: registeredTrawler))
    case .calendarEventRecord(let calendarEventRecord):
      openedRecordContent = .calendarEvent(
        try calendarEventRecord.decodedCalendarEventRecord(
          canonicalOpenedRecordReference: canonicalOpenedRecordReference,
          registeredTrawler: registeredTrawler))
    case .trawlerSpecificOpenedRecordPresentation(let trawlerSpecificOpenedRecordPresentation):
      openedRecordContent = .trawlerSpecificRecordPresentation(
        try trawlerSpecificOpenedRecordPresentation.decodedTrawlerSpecificOpenedRecordPresentation(
          canonicalOpenedRecordReference: canonicalOpenedRecordReference,
          requestedTrawlLink: requestedTrawlLink))
    case nil:
      throw TrawlClientError.invalidProtobuf
    }

    return OpenRecord(
      recordTrawler: registeredTrawler,
      canonicalRecordReference: canonicalOpenedRecordReference,
      openedRecordContent: openedRecordContent)
  }
}

extension Trawl_Open_OpenResponse {
  func decodedOpenResponse() throws -> OpenResponse {
    let requestedTrawlLink = requestedTrawlLink.decodedGloballyRoutableTrawlLink
    let requestedRecordAnchor = requestedRecordAnchor.decodedRecordAnchorIdentifier
    let operationOutcome = try outcome.decodedOperationOutcome()
    let openedRecord =
      hasRecord
      ? try record.decodedOpenRecord(
        requestedTrawlLink: requestedTrawlLink)
      : nil
    let operationFailure =
      hasFailure ? try failure.decodedTrawlerOperationFailure() : nil
    guard
      (operationOutcome == .complete && openedRecord != nil && operationFailure == nil
        && isValidAnchorIdentifier(requestedRecordAnchor)
        && openedRecord?.openedRecordContent.containsAnchor(
          requestedRecordAnchor) == true)
        || (operationOutcome == .failed && openedRecord == nil && operationFailure != nil)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return OpenResponse(
      outcome: operationOutcome,
      requestedTrawlLink: requestedTrawlLink,
      requestedRecordAnchor: requestedRecordAnchor,
      record: openedRecord,
      failure: operationFailure)
  }
}

extension Trawl_Presentation_ArchiveRecordAssociatedTimeForDisplay {
  func decodedArchiveRecordAssociatedTimeForDisplay()
    -> ArchiveRecordAssociatedTimeForDisplay?
  {
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

extension Trawl_Message_MessageRecord {
  fileprivate func decodedMessageRecord(
    registeredTrawler: RegisteredTrawlerIdentity
  ) throws -> MessageRecord {
    let canonicalMessageRecordReference =
      canonicalRecordReference.decodedCanonicalArchiveRecordReference
    guard
      isCanonicalTrawlerRecordReference(
        canonicalMessageRecordReference,
        registeredTrawler: registeredTrawler)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return MessageRecord(
      messageTime:
        hasMessageTime ? messageTime.decodedArchiveRecordAssociatedTimeForDisplay() : nil,
      canonicalRecordReference: canonicalMessageRecordReference,
      peopleRelatedToMessage:
        peopleRelatedToMessage.map { $0.decodedPersonRelatedToArchiveRecord() },
      displayedMessageOrMediaText: displayedMessageOrMediaText,
      conversationDisplayContext: conversationDisplayContext)
  }
}

extension Trawl_Message_MessageMedia {
  fileprivate func decodedMessageMedia() throws -> MessageMedia {
    MessageMedia(
      messageMediaContentKind: messageMediaContentKind.decodedMessageMediaContentKind(),
      messageMediaTitle: messageMediaTitle,
      messageMediaByteCount: hasMessageMediaByteCount ? messageMediaByteCount : nil,
      messageMediaHTTPSURL: try validatedOptionalHTTPSURL(messageMediaHTTPSURL),
      messageMediaMetadataHTTPSURL: try validatedOptionalHTTPSURL(messageMediaMetadataHTTPSURL))
  }
}

extension Trawl_Message_MessageMediaContentKind {
  fileprivate func decodedMessageMediaContentKind() -> MessageMediaContentKind? {
    switch self {
    case .image: .image
    case .video: .video
    case .audio: .audio
    case .file: .file
    case .unspecified, .UNRECOGNIZED: nil
    }
  }
}

extension Trawl_Message_OpenedMessageRecordWithConversationContext {
  fileprivate func decodedOpenedMessageRecordWithConversationContext(
    canonicalOpenedRecordReference: CanonicalArchiveRecordReference,
    registeredTrawler: RegisteredTrawlerIdentity
  ) throws -> OpenedMessageRecordWithConversationContext {
    let canonicalOpenedMessageRecordReference =
      openedMessageRecordReference.decodedCanonicalArchiveRecordReference
    let openedMessageRecordAnchor =
      openedMessageRecordAnchor.decodedRecordAnchorIdentifier
    let canonicalConversationRecordReference =
      conversationRecordReference.decodedCanonicalArchiveRecordReference
    let conversationTrawlLink =
      conversationTrawlLink.decodedGloballyRoutableTrawlLink
    let conversationContextMessageRecords = try
      conversationContextMessageRecordsInDisplayOrder.map {
        try $0.decodedMessageRecord(
          registeredTrawler: registeredTrawler)
      }
    let openedMessageCount = conversationContextMessageRecords.count {
      $0.canonicalRecordReference
        == canonicalOpenedMessageRecordReference
    }
    guard
      canonicalOpenedMessageRecordReference == canonicalOpenedRecordReference,
      isValidAnchorIdentifier(openedMessageRecordAnchor),
      isCanonicalTrawlerRecordReference(
        canonicalConversationRecordReference,
        registeredTrawler: registeredTrawler),
      let conversationLinkRoute = parseGloballyRoutableTrawlLink(
        conversationTrawlLink),
      conversationLinkRoute.registeredTrawler == registeredTrawler,
      openedMessageCount == 1
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return OpenedMessageRecordWithConversationContext(
      conversationDisplayName: conversationDisplayName,
      conversationParticipantDisplayNames: conversationParticipantDisplayNames,
      conversationContextMessageRecordsInDisplayOrder: conversationContextMessageRecords,
      openedMessageRecordReference: canonicalOpenedMessageRecordReference,
      openedMessageRecordAnchor: openedMessageRecordAnchor,
      earlierConversationContextMessagesOmitted: earlierConversationContextMessagesOmitted,
      laterConversationContextMessagesOmitted: laterConversationContextMessagesOmitted,
      conversationRecordReference: canonicalConversationRecordReference,
      openedMessageMedia:
        hasOpenedMessageMedia ? try openedMessageMedia.decodedMessageMedia() : nil,
      conversationTrawlLink: conversationTrawlLink)
  }
}

extension Trawl_Conversation_ConversationRecord {
  fileprivate func decodedConversationRecord(
    canonicalOpenedRecordReference: CanonicalArchiveRecordReference,
    registeredTrawler: RegisteredTrawlerIdentity
  ) throws -> ConversationRecord {
    let canonicalConversationRecordReference =
      canonicalRecordReference.decodedCanonicalArchiveRecordReference
    guard
      canonicalConversationRecordReference == canonicalOpenedRecordReference,
      isCanonicalTrawlerRecordReference(
        canonicalConversationRecordReference,
        registeredTrawler: registeredTrawler)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return ConversationRecord(
      canonicalRecordReference: canonicalConversationRecordReference,
      conversationDisplayName: conversationDisplayName,
      conversationParticipantIdentitiesObservedByTrawlerArchive:
        conversationParticipantIdentitiesObservedByTrawlerArchive.map {
          ConversationParticipantIdentityObservedByTrawlerArchive(
            personDisplayName: $0.personDisplayName,
            exactPersonFilterIdentifiersObservedByTrawlerArchive:
              $0.exactPersonFilterIdentifiersObservedByTrawlerArchive.map {
                $0.decodedExactPersonFilterIdentifier
              })
        },
      mostRecentConversationActivityTime:
        hasMostRecentConversationActivityTime ? mostRecentConversationActivityTime.date : nil,
      unreadMessageCount: hasUnreadMessageCount ? unreadMessageCount : nil,
      numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive:
        hasNumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive
        ? numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive : nil)
  }
}

extension Trawl_CalendarEvent_CalendarEventAvailability {
  fileprivate func decodedCalendarEventAvailability() -> CalendarEventAvailability? {
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

extension Trawl_CalendarEvent_CalendarEventStatus {
  fileprivate func decodedCalendarEventStatus() -> CalendarEventStatus? {
    switch self {
    case .confirmed: .confirmed
    case .tentative: .tentative
    case .cancelled: .cancelled
    case .unknown: .unknown
    case .unspecified, .UNRECOGNIZED: nil
    }
  }
}

extension Trawl_CalendarEvent_CalendarEventAttendeeAttendanceStatus {
  fileprivate func decodedCalendarEventAttendeeAttendanceStatus()
    -> CalendarEventAttendeeAttendanceStatus?
  {
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

extension Trawl_CalendarEvent_CalendarEventRecord {
  fileprivate func decodedCalendarEventRecord(
    canonicalOpenedRecordReference: CanonicalArchiveRecordReference,
    registeredTrawler: RegisteredTrawlerIdentity
  ) throws -> CalendarEventRecord {
    let canonicalCalendarEventRecordReference =
      canonicalRecordReference.decodedCanonicalArchiveRecordReference
    guard
      canonicalCalendarEventRecordReference == canonicalOpenedRecordReference,
      isCanonicalTrawlerRecordReference(
        canonicalCalendarEventRecordReference,
        registeredTrawler: registeredTrawler)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return CalendarEventRecord(
      canonicalRecordReference: canonicalCalendarEventRecordReference,
      calendarEventStartTime:
        hasCalendarEventStartTime
        ? calendarEventStartTime.decodedArchiveRecordAssociatedTimeForDisplay() : nil,
      calendarEventEndTime:
        hasCalendarEventEndTime
        ? calendarEventEndTime.decodedArchiveRecordAssociatedTimeForDisplay() : nil,
      calendarEventDisplayName: calendarEventDisplayName,
      calendarDisplayName: calendarDisplayName,
      calendarAccountDisplayName: calendarAccountDisplayName,
      calendarEventAvailability:
        calendarEventAvailability.decodedCalendarEventAvailability(),
      calendarEventLocation:
        hasCalendarEventLocation
        ? CalendarEventLocation(
          calendarEventLocationDisplayName:
            calendarEventLocation.calendarEventLocationDisplayName,
          calendarEventLocationAddress: calendarEventLocation.calendarEventLocationAddress)
        : nil,
      calendarEventOrganizer:
        hasCalendarEventOrganizer
        ? calendarEventOrganizer.decodedPersonRelatedToArchiveRecord() : nil,
      calendarEventAttendees: calendarEventAttendees.map {
        CalendarEventAttendee(
          personRelatedToCalendarEvent:
            $0.personRelatedToCalendarEvent.decodedPersonRelatedToArchiveRecord(),
          attendeeAttendanceStatus:
            $0.attendeeAttendanceStatus.decodedCalendarEventAttendeeAttendanceStatus())
      },
      calendarEventHTTPSURL: try validatedOptionalHTTPSURL(calendarEventHTTPSURL),
      calendarEventStatus: calendarEventStatus.decodedCalendarEventStatus(),
      calendarEventIsRecurring: calendarEventIsRecurring,
      calendarEventDescription: calendarEventDescription,
      calendarEventDescriptionIsTruncated: calendarEventDescriptionIsTruncated)
  }
}

extension Trawl_Open_TrawlerSpecificOpenedRecordPresentation {
  fileprivate func decodedTrawlerSpecificOpenedRecordPresentation(
    canonicalOpenedRecordReference: CanonicalArchiveRecordReference,
    requestedTrawlLink: GloballyRoutableTrawlLink
  ) throws -> TrawlerSpecificOpenedRecordPresentation {
    guard hasDetailPresentation
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return TrawlerSpecificOpenedRecordPresentation(
      detailPresentation:
        try detailPresentation
          .decodedTrawlerSpecificCommandDetailPresentation(
            canonicalOpenedRecordReference: canonicalOpenedRecordReference,
            requestedTrawlLink: requestedTrawlLink))
  }
}

extension Trawl_Presentation_TrawlerSpecificCommandPresentationValue {
  fileprivate func decodedTrawlerSpecificCommandPresentationValue(
    canonicalOpenedRecordReference: CanonicalArchiveRecordReference,
    requestedTrawlLink: GloballyRoutableTrawlLink
  ) throws -> TrawlerSpecificCommandPresentationValue {
    switch typedValue {
    case .text(let text):
      return .text(text)
    case .unsignedCount(let unsignedCount):
      return .unsignedCount(unsignedCount)
    case .archiveRecordAssociatedTimeForDisplay(let associatedTime):
      guard
        let mappedTime = associatedTime.decodedArchiveRecordAssociatedTimeForDisplay()
      else {
        throw TrawlClientError.invalidProtobuf
      }
      return .archiveRecordAssociatedTime(mappedTime)
    case .canonicalRecordReference(
      let canonicalRecordReference):
      let canonicalRecordReference =
        canonicalRecordReference.decodedCanonicalArchiveRecordReference
      guard
        canonicalRecordReference == canonicalOpenedRecordReference,
        parseGloballyRoutableTrawlLink(requestedTrawlLink) != nil
      else {
        throw TrawlClientError.invalidProtobuf
      }
      return .globallyRoutableTrawlLink(requestedTrawlLink)
    case nil:
      throw TrawlClientError.invalidProtobuf
    }
  }
}

extension Trawl_Presentation_TrawlerSpecificCommandDetailPresentation {
  fileprivate func decodedTrawlerSpecificCommandDetailPresentation(
    canonicalOpenedRecordReference: CanonicalArchiveRecordReference,
    requestedTrawlLink: GloballyRoutableTrawlLink
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
      detailDisplayNameAnchor:
        hasDetailDisplayNameAnchor
        ? detailDisplayNameAnchor.decodedRecordAnchorIdentifier : nil,
      fieldsInDisplayOrder: try fieldsInDisplayOrder.map { field in
        guard field.hasFieldValue else {
          throw TrawlClientError.invalidProtobuf
        }
        return TrawlerSpecificCommandDetailPresentationField(
          fieldDisplayName: field.fieldDisplayName,
          fieldValue:
            try field.fieldValue.decodedTrawlerSpecificCommandPresentationValue(
              canonicalOpenedRecordReference: canonicalOpenedRecordReference,
              requestedTrawlLink: requestedTrawlLink),
          fieldAnchor:
            field.hasFieldAnchor ? field.fieldAnchor.decodedRecordAnchorIdentifier : nil)
      },
      body: mappedBody,
      bodyAnchor:
        hasBodyAnchor ? bodyAnchor.decodedRecordAnchorIdentifier : nil)
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
