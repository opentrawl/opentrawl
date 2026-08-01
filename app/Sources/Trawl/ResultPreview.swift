import SwiftUI
import TrawlClient
import TrawlCore

struct ResultPreview: View {
  let phase: SearchOpenPhase
  let response: OpenResponse?
  var body: some View {
    Group {
      switch phase {
      case .idle:
        EmptyView()
      case .loading: ProgressView("Opening result")
      case .output:
        if let response, let record = response.record {
          switch record.openedRecordContent {
          case .messageWithConversationContext(let openedMessage):
            OpenedMessageRecordWithConversationContextView(
              openedMessage: openedMessage,
              targetAnchor: response.requestedRecordAnchor)
          case .conversation(let conversationRecord):
            ConversationRecordView(
              conversationRecord: conversationRecord,
              trawlLink: response.requestedTrawlLink)
          case .person(let personRecord):
            PersonRecordView(
              personRecord: personRecord,
              targetAnchor: response.requestedRecordAnchor)
          case .calendarEvent(let calendarEventRecord):
            CalendarEventRecordView(calendarEventRecord: calendarEventRecord)
          case .note(let openedNoteRecord):
            OpenedNoteRecordView(
              openedNoteRecord: openedNoteRecord,
              targetAnchor: response.requestedRecordAnchor)
          case .trawlerSpecificRecordPresentation(let openedRecord):
            TrawlerSpecificOpenedRecordPresentationView(
              openedRecord: openedRecord,
              targetAnchor: response.requestedRecordAnchor)
          }
        } else {
          ContentUnavailableView("Result unavailable", systemImage: "exclamationmark.circle")
        }
      case .failed(let message), .timedOut(let message):
        ContentUnavailableView(
          "Result unavailable", systemImage: "exclamationmark.circle", description: Text(message))
      }
    }.frame(maxWidth: .infinity, maxHeight: .infinity)
  }
}
