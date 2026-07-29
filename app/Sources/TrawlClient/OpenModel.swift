import Foundation

public struct OpenRecord: Sendable, Equatable {
  public let registeredTrawlerManifestIdentity: String
  public let canonicalOpenedRecordReference: String
  public let openedRecordContent: OpenedRecordContent
}

public enum OpenedRecordContent: Sendable, Equatable {
  case messageWithConversationContext(OpenedMessageRecordWithConversationContext)
  case conversation(ConversationRecord)
  case person(PersonRecord)
  case calendarEvent(CalendarEventRecord)
  case trawlerSpecificRecord(TrawlerSpecificOpenedRecord)

  func containsAnchor(_ wantedAnchorIdentifier: String) -> Bool {
    switch self {
    case .messageWithConversationContext(let openedMessage):
      openedMessage.openedMessageRecordFixedAnchorIdentifier == wantedAnchorIdentifier
    case .person(let personRecord):
      personRecord.containsAnchor(wantedAnchorIdentifier)
    case .trawlerSpecificRecord(let openedRecord):
      openedRecord.detailPresentation.containsAnchor(wantedAnchorIdentifier)
    case .conversation, .calendarEvent:
      true
    }
  }
}

public enum ArchiveRecordAssociatedTimeForDisplay: Sendable, Equatable {
  case exactTime(Date)
  case calendarDate(Date)
}

public struct MessageRecord: Sendable, Equatable, Identifiable {
  public let messageTime: ArchiveRecordAssociatedTimeForDisplay?
  public let canonicalMessageRecordReferenceForGloballyRoutableTrawlLinkAssignment: String
  public let peopleRelatedToMessage: [PersonRelatedToArchiveRecord]
  public let displayedMessageOrMediaText: String
  public let conversationDisplayContext: String

  public var id: String {
    canonicalMessageRecordReferenceForGloballyRoutableTrawlLinkAssignment
  }
}

public struct MessageMedia: Sendable, Equatable {
  public let messageMediaKind: String
  public let messageMediaTitle: String
  public let messageMediaByteCount: UInt64?
  public let messageMediaHTTPSURL: URL?
  public let messageMediaMetadataHTTPSURL: URL?
}

public struct OpenedMessageRecordWithConversationContext: Sendable, Equatable {
  public let conversationDisplayName: String
  public let conversationParticipantDisplayNames: [String]
  public let conversationContextMessageRecordsInDisplayOrder: [MessageRecord]
  public let canonicalOpenedMessageRecordReference: String
  public let openedMessageRecordFixedAnchorIdentifier: String
  public let earlierConversationContextMessagesOmitted: Bool
  public let laterConversationContextMessagesOmitted: Bool
  public let canonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment: String
  public let openedMessageMedia: MessageMedia?
  public let globallyRoutableTrawlLinkForConversationContainingOpenedMessage: String
}

public struct ConversationParticipantIdentityObservedByTrawlerArchive:
  Sendable, Equatable, Identifiable
{
  public let personDisplayName: String
  public let exactPersonFilterIdentifiersObservedByTrawlerArchive: [String]

  public var id: String {
    ([personDisplayName] + exactPersonFilterIdentifiersObservedByTrawlerArchive)
      .joined(separator: "\u{0}")
  }
}

public struct ConversationRecord: Sendable, Equatable {
  public let canonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment: String
  public let conversationDisplayName: String
  public let conversationParticipantIdentitiesObservedByTrawlerArchive:
    [ConversationParticipantIdentityObservedByTrawlerArchive]
  public let mostRecentConversationActivityTime: Date?
  public let unreadMessageCount: UInt64?
  public let numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive: UInt64?
}

public enum CalendarEventAvailability: Sendable, Equatable {
  case notSupported
  case busy
  case free
  case tentative
  case unavailable
  case unknown
}

public enum CalendarEventStatus: Sendable, Equatable {
  case confirmed
  case tentative
  case cancelled
  case unknown
}

public enum CalendarEventAttendeeAttendanceStatus: Sendable, Equatable {
  case pending
  case accepted
  case declined
  case tentative
  case delegated
  case completed
  case inProcess
  case unknown
}

public struct CalendarEventLocation: Sendable, Equatable {
  public let calendarEventLocationDisplayName: String
  public let calendarEventLocationAddress: String
}

public struct CalendarEventAttendee: Sendable, Equatable {
  public let personRelatedToCalendarEvent: PersonRelatedToArchiveRecord
  public let attendeeAttendanceStatus: CalendarEventAttendeeAttendanceStatus?
}

public struct CalendarEventRecord: Sendable, Equatable {
  public let canonicalCalendarEventRecordReferenceForGloballyRoutableTrawlLinkAssignment: String
  public let calendarEventStartTime: ArchiveRecordAssociatedTimeForDisplay?
  public let calendarEventEndTime: ArchiveRecordAssociatedTimeForDisplay?
  public let calendarEventDisplayName: String
  public let calendarDisplayName: String
  public let calendarAccountDisplayName: String
  public let calendarEventAvailability: CalendarEventAvailability?
  public let calendarEventLocation: CalendarEventLocation?
  public let calendarEventOrganizer: PersonRelatedToArchiveRecord?
  public let calendarEventAttendees: [CalendarEventAttendee]
  public let calendarEventHTTPSURL: URL?
  public let calendarEventStatus: CalendarEventStatus?
  public let calendarEventIsRecurring: Bool
  public let calendarEventDescription: String
  public let calendarEventDescriptionIsTruncated: Bool
}

public enum TrawlerSpecificCommandPresentationValue: Sendable, Equatable {
  case text(String)
  case unsignedCount(UInt64)
  case archiveRecordAssociatedTime(ArchiveRecordAssociatedTimeForDisplay)
  case globallyRoutableTrawlLink(String)
}

public struct TrawlerSpecificCommandDetailPresentationField: Sendable, Equatable {
  public let fieldDisplayName: String
  public let fieldValue: TrawlerSpecificCommandPresentationValue
  public let fieldFixedAnchorIdentifier: String?
}

public enum TrawlerSpecificCommandDetailPresentationBody: Sendable, Equatable {
  case text(String)
  case unavailableExplanation(String)
}

public struct TrawlerSpecificCommandDetailPresentation: Sendable, Equatable {
  public let detailDisplayName: String
  public let detailDisplayNameFixedAnchorIdentifier: String?
  public let fieldsInDisplayOrder: [TrawlerSpecificCommandDetailPresentationField]
  public let body: TrawlerSpecificCommandDetailPresentationBody?
  public let bodyFixedAnchorIdentifier: String?

  func containsAnchor(_ wantedAnchorIdentifier: String) -> Bool {
    detailDisplayNameFixedAnchorIdentifier == wantedAnchorIdentifier
      || bodyFixedAnchorIdentifier == wantedAnchorIdentifier
      || fieldsInDisplayOrder.contains {
        $0.fieldFixedAnchorIdentifier == wantedAnchorIdentifier
      }
  }
}

public struct TrawlerSpecificOpenedRecord: Sendable, Equatable {
  public let typedTrawlerSpecificOpenedRecordTypeURL: String
  public let typedTrawlerSpecificOpenedRecordData: Data
  public let detailPresentation: TrawlerSpecificCommandDetailPresentation
}

public struct OpenResponse: Sendable, Equatable {
  public let outcome: OperationOutcome
  public let requestedGloballyRoutableTrawlLink: String
  public let requestedRecordAnchorIdentifier: String
  public let record: OpenRecord?
  public let failure: TrawlerOperationFailure?
}
