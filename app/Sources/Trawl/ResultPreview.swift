import AppKit
import SwiftUI
import TrawlClient
import TrawlCore

struct ResultPreview: View {
  let phase: SearchOpenPhase
  var isRemoteArchive: Bool = false
  var highlightedTerms: [String] = []
  var loadPhotoImageData: ((GloballyRoutableTrawlLink) async throws -> Data?)?
  var onOpenLink: ((GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void)?

  @State private var copiedLink = false

  var body: some View {
    Group {
      switch phase {
      case .idle:
        EmptyView()
      case .loading:
        ProgressView("Opening result")
      case .loaded(let response, let record):
        VStack(spacing: 0) {
          recordActionBar(link: response.requestedTrawlLink)
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
                recordTrawler: record.recordTrawler,
                trawlLink: response.requestedTrawlLink,
                targetAnchor: response.requestedRecordAnchor,
                isRemoteArchive: isRemoteArchive,
                loadPhotoImageData: loadPhotoImageData,
                onOpenLink: onOpenLink)
            }
          }
          .id(
            SearchMatchIdentifier(
              trawlLink: response.requestedTrawlLink,
              recordAnchor: response.requestedRecordAnchor))
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
    .onChange(of: phase) { _, _ in
      copiedLink = false
    }
  }

  private func recordActionBar(link: GloballyRoutableTrawlLink) -> some View {
    HStack(spacing: 10) {
      Spacer()
      if !link.globallyRoutableTrawlLink.isEmpty {
        Button {
          NSPasteboard.general.clearContents()
          NSPasteboard.general.setString(link.globallyRoutableTrawlLink, forType: .string)
          copiedLink = true
          Task { @MainActor in
            try? await Task.sleep(for: .seconds(2))
            copiedLink = false
          }
        } label: {
          Label(
            copiedLink ? "Link copied" : "Copy link for AI",
            systemImage: copiedLink ? "checkmark" : "link"
          )
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .help("Copy a link your AI can open to read this item")
      }
    }
    .padding(.horizontal, 16)
    .padding(.vertical, 8)
    .background(TrawlDesign.Colors.surface.opacity(0.65))
  }
}
