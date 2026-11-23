import SwiftUI
import TrawlClient

struct CalendarEventRecordView: View {
  let calendarEventRecord: CalendarEventRecord

  var body: some View {
    ScrollView {
      VStack(alignment: .leading, spacing: 16) {
        Text(calendarEventRecord.calendarEventDisplayName)
          .font(.title2)
          .textSelection(.enabled)
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
          if !location.calendarEventLocationDisplayName.isEmpty {
            LabeledContent("Location", value: location.calendarEventLocationDisplayName)
          }
          if !location.calendarEventLocationAddress.isEmpty {
            LabeledContent("Address", value: location.calendarEventLocationAddress)
          }
        }
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
        if calendarEventRecord.calendarEventIsRecurring {
          LabeledContent("Repeats", value: "Yes")
        }
        if let calendarEventHTTPSURL = calendarEventRecord.calendarEventHTTPSURL {
          Link("Open event", destination: calendarEventHTTPSURL)
        }
        if !calendarEventRecord.calendarEventDescription.isEmpty {
          Text(calendarEventRecord.calendarEventDescription)
            .textSelection(.enabled)
        }
      }
      .padding(18)
      .frame(maxWidth: TrawlDesign.recordReadingWidth, alignment: .leading)
      .frame(maxWidth: .infinity, alignment: .leading)
    }
  }
}

private struct CalendarEventTimeRange: View {
  let startTime: ArchiveRecordAssociatedTimeForDisplay?
  let endTime: ArchiveRecordAssociatedTimeForDisplay?

  var body: some View {
    VStack(alignment: .leading, spacing: 4) {
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
