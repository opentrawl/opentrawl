import SwiftUI
import TrawlClient

struct TrawlerSpecificOpenedRecordPresentationView: View {
  let openedRecord: TrawlerSpecificOpenedRecordPresentation
  var recordTrawler: RegisteredTrawlerIdentity? = nil
  var trawlerDisplayName: String? = nil
  let targetAnchor: RecordAnchorIdentifier
  var trawlLink: GloballyRoutableTrawlLink? = nil
  var helperController: TrawlHelperController? = nil
  var onOpenLink: ((GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void)?

  @State private var isShowingAround = false

  private var defaultLinkAnchor: RecordAnchorIdentifier {
    openedRecord.detailPresentation.detailDisplayNameAnchor
      ?? RecordAnchorIdentifier(recordAnchorIdentifier: "detail")
  }

  var body: some View {
    ScrollViewReader { proxy in
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          RecordDetailHeader(
            registeredTrawler: recordTrawler,
            trawlerDisplayName: trawlerDisplayName,
            title: openedRecord.detailPresentation.detailDisplayName,
            subtitle: openedRecord.detailPresentation.detailSubtitle,
            trawlLink: trawlLink,
            showsAroundToggle: helperController != nil && trawlLink != nil,
            isShowingAround: isShowingAround,
            onToggleAround: { isShowingAround.toggle() }
          )
          .id(
            openedRecord.detailPresentation.detailDisplayNameAnchor?.recordAnchorIdentifier
              ?? openedRecord.detailPresentation.detailDisplayName
          )

          if isShowingAround, let helperController, let trawlLink {
            AroundRelatedRecordsSection(
              trawlLink: trawlLink,
              helperController: helperController,
              onOpenLink: onOpenLink
            )
          }

          if !openedRecord.detailPresentation.fieldsInDisplayOrder.isEmpty {
            RecordMetadataCard {
              ForEach(
                Array(openedRecord.detailPresentation.fieldsInDisplayOrder.enumerated()),
                id: \.offset
              ) { fieldIndex, field in
                TrawlerSpecificOpenedRecordPresentationFieldView(
                  field: field,
                  onOpenLink: onOpenLink.map { openLink in
                    { link in openLink(link, defaultLinkAnchor) }
                  }
                )
                .id(
                  field.fieldAnchor?.recordAnchorIdentifier
                    ?? "trawler_specific_opened_record_presentation_field_\(fieldIndex)")
              }
            }
            .font(.callout)
          }

          if let body = openedRecord.detailPresentation.body {
            TrawlerSpecificOpenedRecordPresentationBodyView(detailBody: body)
              .id(
                openedRecord.detailPresentation.bodyAnchor?.recordAnchorIdentifier
                  ?? openedRecord.detailPresentation.detailDisplayName)
          }

          if let footer = openedRecord.detailPresentation.detailFooterText {
            Text(footer)
              .font(.footnote)
              .foregroundStyle(.secondary)
              .textSelection(.enabled)
          }
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

private struct TrawlerSpecificOpenedRecordPresentationFieldView: View {
  let field: TrawlerSpecificCommandDetailPresentationField
  let onOpenLink: ((GloballyRoutableTrawlLink) -> Void)?

  var body: some View {
    LabeledContent {
      TrawlerSpecificOpenedRecordPresentationFieldValueView(
        fieldValue: field.fieldValue,
        onOpenLink: onOpenLink)
    } label: {
      Text(field.fieldDisplayName)
        .foregroundStyle(.secondary)
    }
    .accessibilityElement(children: .combine)
  }
}

private struct TrawlerSpecificOpenedRecordPresentationFieldValueView: View {
  let fieldValue: TrawlerSpecificCommandPresentationValue
  let onOpenLink: ((GloballyRoutableTrawlLink) -> Void)?

  var body: some View {
    Group {
      switch fieldValue {
      case .text(let text):
        Text(text)
      case .unsignedCount(let count):
        Text(count.formatted())
          .monospacedDigit()
      case .archiveRecordAssociatedTime(let associatedTime):
        switch associatedTime {
        case .exactTime(let time):
          Text(time, format: .dateTime.year().month().day().hour().minute())
            .monospacedDigit()
        case .calendarDate(let date):
          Text(date, format: .dateTime.year().month().day())
            .monospacedDigit()
        }
      case .exactMonetaryAmount(let amount):
        Text(amount.formattedDisplayString)
          .monospacedDigit()
          .fontWeight(.medium)
      case .globallyRoutableTrawlLink(let link):
        if let onOpenLink {
          Button(link.globallyRoutableTrawlLink) {
            onOpenLink(link)
          }
          .buttonStyle(.link)
        } else {
          Text(link.globallyRoutableTrawlLink)
            .font(.callout.monospaced())
        }
      }
    }
    .textSelection(.enabled)
  }
}

private struct TrawlerSpecificOpenedRecordPresentationBodyView: View {
  let detailBody: TrawlerSpecificCommandDetailPresentationBody

  var body: some View {
    switch detailBody {
    case .text(let text):
      Text(text)
        .font(.body)
        .lineSpacing(4)
        .textSelection(.enabled)
    case .unavailableExplanation:
      ContentUnavailableView(
        OperationalCopy.Record.unavailableTitle,
        systemImage: "doc.questionmark",
        description: Text(OperationalCopy.Record.unavailableDetail))
    }
  }
}
