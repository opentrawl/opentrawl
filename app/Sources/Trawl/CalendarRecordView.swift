import SwiftUI
import TrawlClient

struct CalendarRecordView: View {
  let calendarRecord: CalendarRecord
  var recordTrawler: RegisteredTrawlerIdentity? = nil
  var trawlerDisplayName: String? = nil
  var trawlLink: GloballyRoutableTrawlLink? = nil

  var body: some View {
    ScrollView {
      VStack(alignment: .leading, spacing: 16) {
        RecordDetailHeader(
          registeredTrawler: recordTrawler,
          trawlerDisplayName: trawlerDisplayName,
          title: calendarRecord.calendarDisplayName,
          subtitle: nil,
          trawlLink: trawlLink,
          showsAroundToggle: false,
          isShowingAround: false,
          onToggleAround: nil
        )
        RecordMetadataCard {
          LabeledContent("Account", value: calendarRecord.calendarAccountDisplayName)
          if let calendarOwnerOrPurposeAnnotation = calendarRecord.calendarOwnerOrPurposeAnnotation {
            LabeledContent(
              "Owner or purpose",
              value: calendarOwnerOrPurposeAnnotation.calendarOwnerOrPurposeDescription)
          }
          LabeledContent("Events") {
            Text(calendarRecord.activeOrFutureCalendarEventCount, format: .number)
              .monospacedDigit()
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
