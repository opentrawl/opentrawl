import SwiftUI
import TrawlClient
import TrawlCore

struct ResultPreview: View {
  let phase: SearchOpenPhase
  var isRemoteArchive: Bool = false
  var highlightedTerms: [String] = []
  var loadPhotoImageData: ((GloballyRoutableTrawlLink) async throws -> Data?)?
  var onRetryOpen: ((GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void)?
  var onOpenLink: ((GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void)?

  @State private var isPhotoInspectorVisible = true

  var body: some View {
    Group {
      switch phase {
      case .idle:
        EmptyView()
      case .loading:
        ProgressView("Opening result")
      case .loaded(let response, let record):
        let matchIdentifier = SearchMatchIdentifier(
          trawlLink: response.requestedTrawlLink,
          recordAnchor: response.requestedRecordAnchor
        )
        if case .trawlerSpecificRecordPresentation(let openedRecord) = record.openedRecordContent,
          record.recordTrawler.registeredTrawlerIdentity == "photos"
        {
          OpenedPhotoRecordView(
            openedRecord: openedRecord,
            trawlLink: response.requestedTrawlLink,
            isRemoteArchive: isRemoteArchive,
            loadPhotoImageData: loadPhotoImageData,
            onRetryLocalOpen: onRetryOpen.map { retryOpen in
              { retryOpen(response.requestedTrawlLink, response.requestedRecordAnchor) }
            },
            isInspectorVisible: $isPhotoInspectorVisible
          )
          .id(matchIdentifier)
        } else {
          VStack(spacing: 0) {
            RecordActionBar(trawlLink: response.requestedTrawlLink)
            Divider()
            Group {
              switch record.openedRecordContent {
              case .messageWithConversationContext(let openedMessage):
                OpenedMessageRecordWithConversationContextView(
                  openedMessage: openedMessage,
                  targetAnchor: response.requestedRecordAnchor,
                  highlightedTerms: highlightedTerms)
              case .conversation(let conversationRecord):
                ConversationRecordView(
                  conversationRecord: conversationRecord,
                  trawlLink: response.requestedTrawlLink)
              case .person(let personRecord):
                PersonRecordView(
                  personRecord: personRecord,
                  targetAnchor: response.requestedRecordAnchor)
              case .calendarEvent(let calendarEventRecord):
                CalendarEventRecordView(
                  calendarEventRecord: calendarEventRecord,
                  targetAnchor: response.requestedRecordAnchor)
              case .calendar(let calendarRecord):
                CalendarRecordView(calendarRecord: calendarRecord)
              case .note(let openedNoteRecord):
                OpenedNoteRecordView(
                  openedNoteRecord: openedNoteRecord,
                  targetAnchor: response.requestedRecordAnchor)
              case .trawlerSpecificRecordPresentation(let openedRecord):
                TrawlerSpecificOpenedRecordPresentationView(
                  openedRecord: openedRecord,
                  targetAnchor: response.requestedRecordAnchor,
                  onOpenLink: onOpenLink)
              }
            }
            .id(matchIdentifier)
          }
        }
      case .failed:
        ContentUnavailableView(
          OperationalCopy.Record.unavailableTitle,
          systemImage: "exclamationmark.circle",
          description: Text(OperationalCopy.Record.unavailableDetail)
        )
      case .timedOut:
        ContentUnavailableView(
          OperationalCopy.Record.unavailableTitle,
          systemImage: "exclamationmark.circle",
          description: Text(OperationalCopy.Record.timedOutDetail)
        )
      }
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
  }
}

private struct RecordActionBar: View {
  let trawlLink: GloballyRoutableTrawlLink

  var body: some View {
    HStack(spacing: 10) {
      Spacer()
      CopyTrawlLinkButton(trawlLink: trawlLink)
    }
    .padding(.horizontal, 16)
    .padding(.vertical, 8)
    .background(TrawlDesign.Colors.surface.opacity(0.65))
  }
}
