import SwiftUI
import TrawlClient

struct OpenedNoteRecordView: View {
  let openedNoteRecord: OpenedNoteRecord
  let targetAnchor: RecordAnchorIdentifier

  var body: some View {
    ScrollViewReader { proxy in
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          OpenedNoteRecordHeader(
            noteDisplayName: openedNoteRecord.noteDisplayName,
            noteFolderDisplayName: openedNoteRecord.noteFolderDisplayName,
            noteCreatedTime: openedNoteRecord.noteCreatedTime,
            noteModifiedTime: openedNoteRecord.noteModifiedTime,
            openedNoteVersionTime: openedNoteRecord.openedNoteVersionTime,
            recoveredNoteVersionCount: openedNoteRecord.recoveredNoteVersionCount,
            specificRecoveredNoteVersionWasOpened:
              openedNoteRecord.specificRecoveredNoteVersionWasOpened,
            noteDisplayNameAnchor: openedNoteRecord.noteDisplayNameAnchor)
          OpenedNoteRecordBody(
            openedNoteBody: openedNoteRecord.openedNoteBody,
            openedNoteBodyAnchor: openedNoteRecord.openedNoteBodyAnchor)
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

private struct OpenedNoteRecordHeader: View {
  let noteDisplayName: String
  let noteFolderDisplayName: String
  let noteCreatedTime: Date?
  let noteModifiedTime: Date?
  let openedNoteVersionTime: Date?
  let recoveredNoteVersionCount: UInt64
  let specificRecoveredNoteVersionWasOpened: Bool
  let noteDisplayNameAnchor: RecordAnchorIdentifier

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      Text(noteDisplayName.isEmpty ? "Note" : noteDisplayName)
        .font(.title2)
        .textSelection(.enabled)
        .id(noteDisplayNameAnchor.recordAnchorIdentifier)
      if !noteFolderDisplayName.isEmpty {
        LabeledContent("Folder", value: noteFolderDisplayName)
      }
      if let noteCreatedTime {
        OpenedNoteRecordTime(label: "Created", time: noteCreatedTime)
      }
      if let noteModifiedTime {
        OpenedNoteRecordTime(label: "Modified", time: noteModifiedTime)
      }
      if specificRecoveredNoteVersionWasOpened, let openedNoteVersionTime {
        OpenedNoteRecordTime(label: "Recovered version", time: openedNoteVersionTime)
      }
      LabeledContent("Versions", value: recoveredNoteVersionCount.formatted())
    }
  }
}

private struct OpenedNoteRecordTime: View {
  let label: LocalizedStringKey
  let time: Date

  var body: some View {
    LabeledContent {
      Text(time, format: .dateTime.year().month().day().hour().minute())
    } label: {
      Text(label)
    }
  }
}

private struct OpenedNoteRecordBody: View {
  let openedNoteBody: OpenedNoteBody
  let openedNoteBodyAnchor: RecordAnchorIdentifier

  var body: some View {
    switch openedNoteBody {
    case .available(let noteBodyText):
      Text(noteBodyText)
        .textSelection(.enabled)
        .id(openedNoteBodyAnchor.recordAnchorIdentifier)
    case .unavailable:
      ContentUnavailableView(
        "Note unavailable",
        systemImage: "doc.questionmark",
        description: Text(OperationalCopy.Record.noteBodyUnavailable)
      )
        .id(openedNoteBodyAnchor.recordAnchorIdentifier)
    }
  }
}
