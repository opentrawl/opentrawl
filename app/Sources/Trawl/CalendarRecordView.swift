import SwiftUI
import TrawlClient

struct CalendarRecordView: View {
  let calendarRecord: CalendarRecord

  var body: some View {
    ScrollView {
      VStack(alignment: .leading, spacing: 16) {
        Text(calendarRecord.calendarDisplayName)
          .font(.title2)
        LabeledContent("Account", value: calendarRecord.calendarAccountDisplayName)
        if let calendarOwnerOrPurposeAnnotation = calendarRecord.calendarOwnerOrPurposeAnnotation {
          LabeledContent(
            "Owner or purpose",
            value: calendarOwnerOrPurposeAnnotation.calendarOwnerOrPurposeDescription)
        }
        LabeledContent("Events") {
          Text(calendarRecord.activeOrFutureCalendarEventCount, format: .number)
        }
      }
      .textSelection(.enabled)
      .padding(18)
      .frame(maxWidth: TrawlDesign.recordReadingWidth, alignment: .leading)
      .frame(maxWidth: .infinity, alignment: .leading)
    }
  }
}
