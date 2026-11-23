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
  case calendar(CalendarRecord)
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
    case .calendarEvent(let calendarEventRecord):
      switch wantedAnchor.recordAnchorIdentifier {
      case "summary":
        true
      case "description":
        !calendarEventRecord.calendarEventDescription.trimmingCharacters(in: .whitespacesAndNewlines)
          .isEmpty
      case "location":
        calendarEventRecord.calendarEventLocation.map {
          !$0.calendarEventLocationDisplayName.trimmingCharacters(in: .whitespacesAndNewlines)
            .isEmpty
            || !$0.calendarEventLocationAddress.trimmingCharacters(in: .whitespacesAndNewlines)
              .isEmpty
        } ?? false
      case "participant":
        calendarEventRecord.calendarEventOrganizer != nil
          || !calendarEventRecord.calendarEventAttendees.isEmpty
      default:
        false
      }
    case .calendar:
      wantedAnchor.recordAnchorIdentifier == "summary"
        || wantedAnchor.recordAnchorIdentifier == "calendar"
    case .conversation:
      wantedAnchor.recordAnchorIdentifier == "match"
        || wantedAnchor.recordAnchorIdentifier == "conversation"
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
  public let messageText: String
  public let conversationDisplayName: String
  public let messageMedia: MessageMedia?

  public var messageTextAndMediaDescription: String {
    let trimmedMessageText = messageText.trimmingCharacters(in: .whitespacesAndNewlines)
    guard let messageMedia else {
      return trimmedMessageText
    }
    let mediaKind =
      messageMedia.messageMediaContentKind?.messageMediaContentKindDisplayName ?? "Attachment"
    let mediaTitle = messageMedia.messageMediaTitle.trimmingCharacters(in: .whitespacesAndNewlines)
    let mediaDescription = mediaTitle.isEmpty ? mediaKind : "\(mediaKind): \(mediaTitle)"
    return trimmedMessageText.isEmpty
      ? mediaDescription : "\(trimmedMessageText) · \(mediaDescription)"
  }

  public var id: CanonicalArchiveRecordReference {
    canonicalRecordReference
  }
}

public enum MessageMediaContentKind: Sendable, Equatable {
  case attachment
  case image
  case video
  case audio
  case file
  case gif
  case sticker
  case link
  case photoOrVideo
  case voiceOrInstantVideo

  public var messageMediaContentKindDisplayName: String {
    switch self {
    case .attachment: "Attachment"
    case .image: "Image"
    case .video: "Video"
    case .audio: "Audio"
    case .file: "File"
    case .gif: "GIF"
    case .sticker: "Sticker"
    case .link: "Link"
    case .photoOrVideo: "Photo or video"
    case .voiceOrInstantVideo: "Voice message or instant video"
    }
  }
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
  public let conversationContextMessageRecordsNewestFirst: [MessageRecord]
  public let openedMessageRecordReference: CanonicalArchiveRecordReference
  public let openedMessageRecordAnchor: RecordAnchorIdentifier
  public let earlierConversationContextMessagesOmitted: Bool
  public let laterConversationContextMessagesOmitted: Bool
  public let conversationRecordReference: CanonicalArchiveRecordReference
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

public struct CalendarRecord: Sendable, Equatable {
  public let canonicalRecordReference: CanonicalArchiveRecordReference
  public let calendarDisplayName: String
  public let calendarAccountDisplayName: String
  public let calendarOwnerOrPurposeAnnotation: CalendarOwnerOrPurposeAnnotation?
  public let activeOrFutureCalendarEventCount: UInt64
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

public enum MonetaryAmountDisplaySignStyle: Sendable, Equatable {
  case balance
  case signedMovement
}

public struct ExactMonetaryAmount: Sendable, Equatable {
  public let exactDecimalAmount: String
  public let iso4217CurrencyCode: String
  public let monetaryAmountDisplaySignStyle: MonetaryAmountDisplaySignStyle

  public init(
    exactDecimalAmount: String,
    iso4217CurrencyCode: String,
    monetaryAmountDisplaySignStyle: MonetaryAmountDisplaySignStyle
  ) {
    self.exactDecimalAmount = exactDecimalAmount
    self.iso4217CurrencyCode = iso4217CurrencyCode
    self.monetaryAmountDisplaySignStyle = monetaryAmountDisplaySignStyle
  }

  public var formattedDisplayString: String {
    guard let decimal = Decimal(string: exactDecimalAmount, locale: Locale(identifier: "en_US_POSIX")) else {
      return iso4217CurrencyCode.isEmpty
        ? exactDecimalAmount : "\(exactDecimalAmount) \(iso4217CurrencyCode)"
    }
    let formatter = NumberFormatter()
    formatter.numberStyle = .currency
    formatter.currencyCode = iso4217CurrencyCode
    formatter.locale = .autoupdatingCurrent
    let isNegative = decimal < 0
    let magnitude = isNegative ? -decimal : decimal
    let formattedMagnitude = formatter.string(from: magnitude as NSDecimalNumber)
      ?? "\(exactDecimalAmount) \(iso4217CurrencyCode)"
    if isNegative {
      return "-\(formattedMagnitude)"
    }
    if monetaryAmountDisplaySignStyle == .signedMovement, decimal > 0 {
      return "+\(formattedMagnitude)"
    }
    return formattedMagnitude
  }
}

public enum TrawlerSpecificCommandPresentationValue: Sendable, Equatable {
  case text(String)
  case unsignedCount(UInt64)
  case archiveRecordAssociatedTime(ArchiveRecordAssociatedTimeForDisplay)
  case globallyRoutableTrawlLink(GloballyRoutableTrawlLink)
  case exactMonetaryAmount(ExactMonetaryAmount)
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
  public let detailSubtitle: String?
  public let fieldsInDisplayOrder: [TrawlerSpecificCommandDetailPresentationField]
  public let body: TrawlerSpecificCommandDetailPresentationBody?
  public let bodyAnchor: RecordAnchorIdentifier?
  public let detailFooterText: String?

  public init(
    detailDisplayName: String,
    detailDisplayNameAnchor: RecordAnchorIdentifier?,
    detailSubtitle: String? = nil,
    fieldsInDisplayOrder: [TrawlerSpecificCommandDetailPresentationField],
    body: TrawlerSpecificCommandDetailPresentationBody?,
    bodyAnchor: RecordAnchorIdentifier?,
    detailFooterText: String? = nil
  ) {
    self.detailDisplayName = detailDisplayName
    self.detailDisplayNameAnchor = detailDisplayNameAnchor
    self.detailSubtitle = detailSubtitle
    self.fieldsInDisplayOrder = fieldsInDisplayOrder
    self.body = body
    self.bodyAnchor = bodyAnchor
    self.detailFooterText = detailFooterText
  }

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
