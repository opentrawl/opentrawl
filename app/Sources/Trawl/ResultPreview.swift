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
          VStack(spacing: 0) {
            if let requestedOpenedRecordText = response.requestedOpenedRecordText {
              MatchedArchiveRecordTextPassageView(text: requestedOpenedRecordText)
              Divider()
            }
            openedRecordView(response: response, record: record)
          }
        } else {
          ContentUnavailableView(
            OperationalCopy.Record.unavailableTitle,
            systemImage: "exclamationmark.circle",
            description: Text(OperationalCopy.Record.unavailableDetail)
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
    }.frame(maxWidth: .infinity, maxHeight: .infinity)
  }

  @ViewBuilder
  private func openedRecordView(response: OpenResponse, record: OpenRecord) -> some View {
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
  }
}

private struct MatchedArchiveRecordTextPassageView: View {
  let text: String

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      Text("Matched passage")
        .font(.headline)
      ScrollView {
        Text(text)
          .frame(maxWidth: .infinity, alignment: .leading)
          .textSelection(.enabled)
      }
      .frame(maxHeight: 180)
    }
    .padding(18)
    .frame(maxWidth: TrawlDesign.recordReadingWidth, alignment: .leading)
    .frame(maxWidth: .infinity, alignment: .leading)
  }
}
