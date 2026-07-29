import SwiftUI
import TrawlClient

struct ConversationRecordView: View {
  let conversationRecord: ConversationRecord
  let globallyRoutableTrawlLink: String

  var body: some View {
    ScrollView {
      VStack(alignment: .leading, spacing: 16) {
        Text(conversationRecord.conversationDisplayName)
          .font(.title2)
          .textSelection(.enabled)
        if !conversationRecord.conversationParticipantIdentitiesObservedByTrawlerArchive.isEmpty {
          LabeledContent(
            "People",
            value: conversationRecord.conversationParticipantIdentitiesObservedByTrawlerArchive
              .map(\.personDisplayName)
              .filter { !$0.isEmpty }
              .formatted())
        }
        if let mostRecentActivity = conversationRecord.mostRecentConversationActivityTime {
          LabeledContent {
            Text(
              mostRecentActivity,
              format: .dateTime.year().month().day().hour().minute())
          } label: {
            Text("Last activity")
          }
        }
        if let unreadMessageCount = conversationRecord.unreadMessageCount {
          LabeledContent("Unread messages", value: unreadMessageCount.formatted())
        }
        if conversationRecord.conversationParticipantIdentitiesObservedByTrawlerArchive.isEmpty,
          let participantCount =
          conversationRecord
          .numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive
        {
          LabeledContent("People", value: participantCount.formatted())
        }
        if !globallyRoutableTrawlLink.isEmpty {
          LabeledContent("Link", value: globallyRoutableTrawlLink)
            .font(.caption)
            .foregroundStyle(.secondary)
        }
      }
      .textSelection(.enabled)
      .padding(18)
      .frame(maxWidth: TrawlDesign.recordReadingWidth, alignment: .leading)
      .frame(maxWidth: .infinity, alignment: .leading)
    }
  }
}
