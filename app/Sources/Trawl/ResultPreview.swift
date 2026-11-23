import SwiftUI
import TrawlClient
import TrawlCore

struct ResultPreview: View {
  let phase: SearchOpenPhase
  var trawlerDisplayName: ((RegisteredTrawlerIdentity) -> String?)? = nil
  var helperController: TrawlHelperController? = nil
  var onOpenLink: ((GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void)?

  var body: some View {
    Group {
      switch phase {
      case .idle:
        VStack(spacing: 10) {
          Image(systemName: "doc.text.magnifyingglass")
            .font(.system(size: 32, weight: .light))
            .foregroundStyle(.quaternary)
          Text(OperationalCopy.Record.selectMatchPrompt)
            .font(.callout.weight(.medium))
            .foregroundStyle(.secondary)
          Text(OperationalCopy.Record.selectMatchHint)
            .font(.caption)
            .foregroundStyle(.tertiary)
        }
        .padding()
      case .loading:
        ProgressView("Opening result")
      case .loaded(let response, let record):
        let resolvedName = trawlerDisplayName?(record.recordTrawler)
        switch record.openedRecordContent {
        case .messageWithConversationContext(let openedMessage):
          OpenedMessageRecordWithConversationContextView(
            openedMessage: openedMessage,
            recordTrawler: record.recordTrawler,
            trawlerDisplayName: resolvedName,
            targetAnchor: response.requestedRecordAnchor,
            trawlLink: response.requestedTrawlLink,
            helperController: helperController,
            onOpenLink: onOpenLink
          )
        case .conversation(let conversationRecord):
          ConversationRecordView(
            conversationRecord: conversationRecord,
            recordTrawler: record.recordTrawler,
            trawlerDisplayName: resolvedName,
            trawlLink: response.requestedTrawlLink,
            helperController: helperController,
            onOpenLink: onOpenLink
          )
        case .person(let personRecord):
          PersonRecordView(
            personRecord: personRecord,
            recordTrawler: record.recordTrawler,
            trawlerDisplayName: resolvedName,
            targetAnchor: response.requestedRecordAnchor,
            trawlLink: response.requestedTrawlLink,
            helperController: helperController,
            onOpenLink: onOpenLink
          )
        case .calendarEvent(let calendarEventRecord):
          CalendarEventRecordView(
            calendarEventRecord: calendarEventRecord,
            recordTrawler: record.recordTrawler,
            trawlerDisplayName: resolvedName,
            trawlLink: response.requestedTrawlLink,
            helperController: helperController,
            onOpenLink: onOpenLink
          )
        case .calendar(let calendarRecord):
          CalendarRecordView(
            calendarRecord: calendarRecord,
            recordTrawler: record.recordTrawler,
            trawlerDisplayName: resolvedName,
            trawlLink: response.requestedTrawlLink
          )
        case .note(let openedNoteRecord):
          OpenedNoteRecordView(
            openedNoteRecord: openedNoteRecord,
            recordTrawler: record.recordTrawler,
            trawlerDisplayName: resolvedName,
            targetAnchor: response.requestedRecordAnchor,
            trawlLink: response.requestedTrawlLink,
            helperController: helperController,
            onOpenLink: onOpenLink
          )
        case .trawlerSpecificRecordPresentation(let openedRecord):
          TrawlerSpecificOpenedRecordPresentationView(
            openedRecord: openedRecord,
            recordTrawler: record.recordTrawler,
            trawlerDisplayName: resolvedName,
            targetAnchor: response.requestedRecordAnchor,
            trawlLink: response.requestedTrawlLink,
            helperController: helperController,
            onOpenLink: onOpenLink
          )
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
