import SwiftUI
import TrawlClient

struct OpenedNoteRecordView: View {
  let openedNoteRecord: OpenedNoteRecord
  var recordTrawler: RegisteredTrawlerIdentity? = nil
  var trawlerDisplayName: String? = nil
  let targetAnchor: RecordAnchorIdentifier
  var trawlLink: GloballyRoutableTrawlLink? = nil
  var helperController: TrawlHelperController? = nil
  var onOpenLink: ((GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void)? = nil

  @State private var isShowingAround = false

  var body: some View {
    ScrollViewReader { proxy in
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          RecordDetailHeader(
            registeredTrawler: recordTrawler,
            trawlerDisplayName: trawlerDisplayName,
            title: openedNoteRecord.noteDisplayName.isEmpty ? "Note" : openedNoteRecord.noteDisplayName,
            subtitle: openedNoteRecord.noteFolderDisplayName.isEmpty
              ? nil : openedNoteRecord.noteFolderDisplayName,
            trawlLink: trawlLink,
            showsAroundToggle: helperController != nil && trawlLink != nil,
            isShowingAround: isShowingAround,
            onToggleAround: { isShowingAround.toggle() }
          )
          .id(openedNoteRecord.noteDisplayNameAnchor.recordAnchorIdentifier)

          if isShowingAround, let helperController, let trawlLink {
            AroundRelatedRecordsSection(
              trawlLink: trawlLink,
              helperController: helperController,
              onOpenLink: onOpenLink
            )
          }

          OpenedNoteRecordMetadata(
            noteFolderDisplayName: openedNoteRecord.noteFolderDisplayName,
            noteCreatedTime: openedNoteRecord.noteCreatedTime,
            noteModifiedTime: openedNoteRecord.noteModifiedTime,
            openedNoteVersionTime: openedNoteRecord.openedNoteVersionTime,
            recoveredNoteVersionCount: openedNoteRecord.recoveredNoteVersionCount,
            specificRecoveredNoteVersionWasOpened:
              openedNoteRecord.specificRecoveredNoteVersionWasOpened
          )

          OpenedNoteRecordBody(
            openedNoteBody: openedNoteRecord.openedNoteBody,
            openedNoteBodyAnchor: openedNoteRecord.openedNoteBodyAnchor
          )
        }
        .padding(20)
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

private struct OpenedNoteRecordMetadata: View {
  let noteFolderDisplayName: String
  let noteCreatedTime: Date?
  let noteModifiedTime: Date?
  let openedNoteVersionTime: Date?
  let recoveredNoteVersionCount: UInt64
  let specificRecoveredNoteVersionWasOpened: Bool

  var body: some View {
    RecordMetadataCard {
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
    .font(.callout)
  }
}

private struct OpenedNoteRecordTime: View {
  let label: LocalizedStringKey
  let time: Date

  var body: some View {
    LabeledContent {
      Text(time, format: .dateTime.year().month().day().hour().minute())
        .monospacedDigit()
    } label: {
      Text(label)
        .foregroundStyle(.secondary)
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
        .font(.body)
        .lineSpacing(4)
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
