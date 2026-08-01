import Foundation

public struct OpenRecord: Sendable, Equatable {
  public let recordTrawler: RegisteredTrawlerIdentity
  public let canonicalRecordReference: CanonicalArchiveRecordReference
  public let openedRecordContent: OpenedRecordContent
}

public enum OpenedRecordContent: Sendable, Equatable {
  case messageWithConversationContext(OpenedMessageRecordWithConversationContext)
  case conversation(ConversationRecord)
  case person(PersonRecord)
  case calendarEvent(CalendarEventRecord)
  case note(OpenedNoteRecord)
  case trawlerSpecificRecordPresentation(TrawlerSpecificOpenedRecordPresentation)

  func containsAnchor(_ wantedAnchor: RecordAnchorIdentifier) -> Bool {
    switch self {
    case .messageWithConversationContext(let openedMessage):
      openedMessage.openedMessageRecordAnchor == wantedAnchor
    case .person(let personRecord):
      personRecord.containsAnchor(wantedAnchor)
    case .note(let openedNoteRecord):
      openedNoteRecord.noteDisplayNameAnchor == wantedAnchor
        || openedNoteRecord.openedNoteBodyAnchor == wantedAnchor
    case .trawlerSpecificRecordPresentation(let openedRecord):
      openedRecord.detailPresentation.containsAnchor(wantedAnchor)
    case .conversation, .calendarEvent:
      true
    }
  }
}

public enum OpenedNoteBody: Sendable, Equatable {
  case available(noteBodyText: String)
  case unavailable
}

public struct OpenedNoteRecord: Sendable, Equatable {
  public let canonicalNoteRecordReference: CanonicalArchiveRecordReference
  public let canonicalOpenedNoteVersionRecordReference: CanonicalArchiveRecordReference
  public let noteDisplayName: String
  public let noteFolderDisplayName: String
  public let noteCreatedTime: Date?
  public let noteModifiedTime: Date?
  public let openedNoteVersionTime: Date?
  public let recoveredNoteVersionCount: UInt64
  public let openedNoteBody: OpenedNoteBody
  public let specificRecoveredNoteVersionWasOpened: Bool
  public let noteDisplayNameAnchor: RecordAnchorIdentifier
  public let openedNoteBodyAnchor: RecordAnchorIdentifier
}

public enum ArchiveRecordAssociatedTimeForDisplay: Sendable, Equatable {
  case exactTime(Date)
  case calendarDate(Date)
}

public struct MessageRecord: Sendable, Equatable, Identifiable {
  public let messageTime: ArchiveRecordAssociatedTimeForDisplay?
  public let canonicalRecordReference: CanonicalArchiveRecordReference
  public let peopleRelatedToMessage: [PersonRelatedToArchiveRecord]
  public let displayedMessageOrMediaText: String
  public let conversationDisplayContext: String

  public var id: CanonicalArchiveRecordReference {
    canonicalRecordReference
  }
}

public enum MessageMediaContentKind: Sendable, Equatable {
  case image
  case video
  case audio
  case file
}

public struct MessageMedia: Sendable, Equatable {
  public let messageMediaContentKind: MessageMediaContentKind?
  public let messageMediaTitle: String
  public let messageMediaByteCount: UInt64?
  public let messageMediaHTTPSURL: URL?
  public let messageMediaMetadataHTTPSURL: URL?
}

public struct OpenedMessageRecordWithConversationContext: Sendable, Equatable {
  public let conversationDisplayName: String
  public let conversationParticipantDisplayNames: [String]
  public let conversationContextMessageRecordsInDisplayOrder: [MessageRecord]
  public let openedMessageRecordReference: CanonicalArchiveRecordReference
  public let openedMessageRecordAnchor: RecordAnchorIdentifier
  public let earlierConversationContextMessagesOmitted: Bool
  public let laterConversationContextMessagesOmitted: Bool
  public let conversationRecordReference: CanonicalArchiveRecordReference
  public let openedMessageMedia: MessageMedia?
  public let conversationTrawlLink: GloballyRoutableTrawlLink
}

public struct ConversationParticipantIdentityObservedByTrawlerArchive:
  Sendable, Equatable, Identifiable
{
  public let personDisplayName: String
  public let exactPersonFilterIdentifiersObservedByTrawlerArchive: [ExactPersonFilterIdentifier]

  public var id: String {
    ([personDisplayName]
      + exactPersonFilterIdentifiersObservedByTrawlerArchive.map {
        $0.exactPersonFilterIdentifier
      })
      .joined(separator: "\u{0}")
  }
}

public struct ConversationRecord: Sendable, Equatable {
  public let canonicalRecordReference: CanonicalArchiveRecordReference
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

public struct CalendarOwnerOrPurposeAnnotation: Sendable, Equatable {
  public let calendarOwnerOrPurposeDescription: String
  public let calendarOwnerOrPurposeDescriptionStatedDate:
    CalendarOwnerOrPurposeDescriptionStatedDate
}

public struct CalendarOwnerOrPurposeDescriptionStatedDate: Sendable, Equatable {
  public let calendarYear: Int32
  public let calendarMonthNumber: Int32
  public let calendarDayOfMonth: Int32
}

public struct CalendarEventRecord: Sendable, Equatable {
  public let canonicalRecordReference: CanonicalArchiveRecordReference
  public let calendarEventStartTime: ArchiveRecordAssociatedTimeForDisplay?
  public let calendarEventEndTime: ArchiveRecordAssociatedTimeForDisplay?
  public let calendarEventDisplayName: String
  public let calendarDisplayName: String
  public let calendarAccountDisplayName: String
  public let calendarOwnerOrPurposeAnnotation: CalendarOwnerOrPurposeAnnotation?
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
  case globallyRoutableTrawlLink(GloballyRoutableTrawlLink)
}

public struct TrawlerSpecificCommandDetailPresentationField: Sendable, Equatable {
  public let fieldDisplayName: String
  public let fieldValue: TrawlerSpecificCommandPresentationValue
  public let fieldAnchor: RecordAnchorIdentifier?
}

public enum TrawlerSpecificCommandDetailPresentationBody: Sendable, Equatable {
  case text(String)
  case unavailableExplanation(String)
}

public struct TrawlerSpecificCommandDetailPresentation: Sendable, Equatable {
  public let detailDisplayName: String
  public let detailDisplayNameAnchor: RecordAnchorIdentifier?
  public let fieldsInDisplayOrder: [TrawlerSpecificCommandDetailPresentationField]
  public let body: TrawlerSpecificCommandDetailPresentationBody?
  public let bodyAnchor: RecordAnchorIdentifier?

  func containsAnchor(_ wantedAnchor: RecordAnchorIdentifier) -> Bool {
    detailDisplayNameAnchor == wantedAnchor
      || bodyAnchor == wantedAnchor
      || fieldsInDisplayOrder.contains {
        $0.fieldAnchor == wantedAnchor
      }
  }
}

public struct TrawlerSpecificOpenedRecordPresentation: Sendable, Equatable {
  public let detailPresentation: TrawlerSpecificCommandDetailPresentation
}

public struct OpenResponse: Sendable, Equatable {
  public let outcome: OperationOutcome
  public let requestedTrawlLink: GloballyRoutableTrawlLink
  public let requestedRecordAnchor: RecordAnchorIdentifier
  public let record: OpenRecord?
  public let failure: TrawlerOperationFailure?
}
