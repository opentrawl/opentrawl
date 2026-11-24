import SwiftUI
import TrawlClient

struct CalendarEventRecordView: View {
  let calendarEventRecord: CalendarEventRecord
  var targetAnchor: RecordAnchorIdentifier = RecordAnchorIdentifier(recordAnchorIdentifier: "summary")

  var body: some View {
    ScrollViewReader { proxy in
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          Text(calendarEventRecord.calendarEventDisplayName)
            .font(TrawlDesign.Typography.pageTitle)
            .foregroundStyle(TrawlDesign.Colors.ink)
            .textSelection(.enabled)
            .id("summary")
          RecordDetailsGrid {
            CalendarEventTimeRange(
              startTime: calendarEventRecord.calendarEventStartTime,
              endTime: calendarEventRecord.calendarEventEndTime)
            if !calendarEventRecord.calendarDisplayName.isEmpty {
              LabeledContent("Calendar", value: calendarEventRecord.calendarDisplayName)
            }
            if !calendarEventRecord.calendarAccountDisplayName.isEmpty {
              LabeledContent("Account", value: calendarEventRecord.calendarAccountDisplayName)
            }
            if let calendarOwnerOrPurposeAnnotation =
              calendarEventRecord.calendarOwnerOrPurposeAnnotation
            {
              LabeledContent(
                "Owner or purpose",
                value: calendarOwnerOrPurposeAnnotation.calendarOwnerOrPurposeDescription)
            }
            if let location = calendarEventRecord.calendarEventLocation {
              VStack(alignment: .leading, spacing: 0) {
                if !location.calendarEventLocationDisplayName.isEmpty {
                  LabeledContent("Location", value: location.calendarEventLocationDisplayName)
                }
                if !location.calendarEventLocationAddress.isEmpty {
                  LabeledContent("Address", value: location.calendarEventLocationAddress)
                }
              }
              .id("location")
            }
            if calendarEventRecord.calendarEventOrganizer != nil
              || !calendarEventRecord.calendarEventAttendees.isEmpty
            {
              VStack(alignment: .leading, spacing: 0) {
                if let organizer = calendarEventRecord.calendarEventOrganizer {
                  LabeledContent("Organizer", value: organizer.personDisplayName)
                }
                if !calendarEventRecord.calendarEventAttendees.isEmpty {
                  LabeledContent(
                    "Attendees",
                    value: calendarEventRecord.calendarEventAttendees
                      .map(\.personRelatedToCalendarEvent.personDisplayName)
                      .filter { !$0.isEmpty }
                      .formatted())
                }
              }
              .id("participant")
            }
            if calendarEventRecord.calendarEventIsRecurring {
              LabeledContent("Repeats", value: "Yes")
            }
          }
          if let calendarEventHTTPSURL = calendarEventRecord.calendarEventHTTPSURL {
            Link("Open event", destination: calendarEventHTTPSURL)
          }
          if !calendarEventRecord.calendarEventDescription.isEmpty {
            Text(calendarEventRecord.calendarEventDescription)
              .textSelection(.enabled)
              .id("description")
          }
        }
        .padding(18)
        .frame(maxWidth: TrawlDesign.recordReadingWidth, alignment: .leading)
        .frame(maxWidth: .infinity, alignment: .leading)
      }
      .onAppear {
        guard !targetAnchor.recordAnchorIdentifier.isEmpty else { return }
        proxy.scrollTo(targetAnchor.recordAnchorIdentifier, anchor: .center)
      }
    }
  }
}

private struct CalendarEventTimeRange: View {
  let startTime: ArchiveRecordAssociatedTimeForDisplay?
  let endTime: ArchiveRecordAssociatedTimeForDisplay?

  var body: some View {
    VStack(alignment: .leading, spacing: 0) {
      if let startTime {
        CalendarEventAssociatedTime(label: "Starts", associatedTime: startTime)
      }
      if let endTime {
        CalendarEventAssociatedTime(label: "Ends", associatedTime: endTime)
      }
    }
  }
}

private struct CalendarEventAssociatedTime: View {
  let label: String
  let associatedTime: ArchiveRecordAssociatedTimeForDisplay

  var body: some View {
    LabeledContent {
      switch associatedTime {
      case .exactTime(let time):
        Text(time, format: .dateTime.year().month().day().hour().minute())
      case .calendarDate(let date):
        Text(date, format: .dateTime.year().month().day())
      }
    } label: {
      Text(label)
    }
  }
}
