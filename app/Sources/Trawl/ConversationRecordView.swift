import SwiftUI
import TrawlClient

struct ConversationRecordView: View {
  let conversationRecord: ConversationRecord
  var recordTrawler: RegisteredTrawlerIdentity? = nil
  var trawlerDisplayName: String? = nil
  let trawlLink: GloballyRoutableTrawlLink
  var helperController: TrawlHelperController? = nil
  var onOpenLink: ((GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void)? = nil

  @State private var isShowingAround = false

  var body: some View {
    ScrollView {
      VStack(alignment: .leading, spacing: 16) {
        RecordDetailHeader(
          registeredTrawler: recordTrawler,
          trawlerDisplayName: trawlerDisplayName,
          title: conversationRecord.conversationDisplayName,
          subtitle: nil,
          trawlLink: trawlLink,
          showsAroundToggle: helperController != nil,
          isShowingAround: isShowingAround,
          onToggleAround: { isShowingAround.toggle() }
        )
        if isShowingAround, let helperController {
          AroundRelatedRecordsSection(
            trawlLink: trawlLink,
            helperController: helperController,
            onOpenLink: onOpenLink
          )
        }
        RecordMetadataCard {
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
                .monospacedDigit()
            } label: {
              Text("Last activity")
                .foregroundStyle(.secondary)
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
        }
        .font(.callout)
      }
      .textSelection(.enabled)
      .padding(20)
      .frame(maxWidth: TrawlDesign.recordReadingWidth, alignment: .leading)
      .frame(maxWidth: .infinity, alignment: .leading)
    }
  }
}
