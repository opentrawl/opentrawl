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
              targetAnchorIdentifier: response.requestedRecordAnchorIdentifier)
          case .conversation(let conversationRecord):
            ConversationRecordView(
              conversationRecord: conversationRecord,
              globallyRoutableTrawlLink: response.requestedGloballyRoutableTrawlLink)
          case .person(let personRecord):
            PersonRecordView(
              personRecord: personRecord,
              targetAnchorIdentifier: response.requestedRecordAnchorIdentifier)
          case .calendarEvent(let calendarEventRecord):
            CalendarEventRecordView(calendarEventRecord: calendarEventRecord)
          case .trawlerSpecificRecord(let openedRecord):
            TrawlerSpecificOpenedRecordDetailView(
              openedRecord: openedRecord,
              targetAnchorIdentifier: response.requestedRecordAnchorIdentifier)
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
