import SwiftUI
import TrawlClient

struct TrawlerSpecificOpenedRecordPresentationView: View {
  let openedRecord: TrawlerSpecificOpenedRecordPresentation
  let recordTrawler: RegisteredTrawlerIdentity
  let trawlLink: GloballyRoutableTrawlLink
  let targetAnchor: RecordAnchorIdentifier
  var isRemoteArchive: Bool = false
  var loadPhotoImageData: ((GloballyRoutableTrawlLink) async throws -> Data?)?
  var onRetryOpen: (() -> Void)?
  var onOpenLink: ((GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void)?

  private var isPhotoRecord: Bool {
    recordTrawler.registeredTrawlerIdentity == "photos"
  }

  private var visibleFieldsInDisplayOrder: [TrawlerSpecificCommandDetailPresentationField] {
    openedRecord.detailPresentation.fieldsInDisplayOrder.filter { field in
      !OpenedPhotoPreviewView.shouldHideRawPicturePathField(field, isPhotoRecord: isPhotoRecord)
    }
  }

  private var defaultLinkAnchor: RecordAnchorIdentifier {
    openedRecord.detailPresentation.detailDisplayNameAnchor
      ?? RecordAnchorIdentifier(recordAnchorIdentifier: "detail")
  }

  var body: some View {
    ScrollViewReader { proxy in
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          VStack(alignment: .leading, spacing: 4) {
            Text(openedRecord.detailPresentation.detailDisplayName)
              .font(TrawlDesign.Typography.pageTitle)
              .textSelection(.enabled)
              .id(
                openedRecord.detailPresentation.detailDisplayNameAnchor?.recordAnchorIdentifier
                  ?? openedRecord.detailPresentation.detailDisplayName)
            if let subtitle = openedRecord.detailPresentation.detailSubtitle {
              Text(subtitle)
                .font(TrawlDesign.Typography.meta)
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
            }
          }
          if isPhotoRecord {
            OpenedPhotoPreviewView(
              trawlLink: trawlLink,
              localCachedPictureFilePath: OpenedPhotoPreviewView.localCachedPictureFilePath(
                from: openedRecord.detailPresentation.fieldsInDisplayOrder
              ),
              pictureStatusFieldText: OpenedPhotoPreviewView.pictureFieldText(
                from: openedRecord.detailPresentation.fieldsInDisplayOrder
              ),
              isRemoteArchive: isRemoteArchive,
              loadPhotoImageData: loadPhotoImageData,
              onRetryLocalOpen: onRetryOpen
            )
          }
          RecordDetailsGrid {
            ForEach(
              Array(visibleFieldsInDisplayOrder.enumerated()),
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
          if let body = openedRecord.detailPresentation.body {
            TrawlerSpecificOpenedRecordPresentationBodyView(detailBody: body)
              .id(
                openedRecord.detailPresentation.bodyAnchor?.recordAnchorIdentifier
                  ?? openedRecord.detailPresentation.detailDisplayName)
          }
          if let footer = openedRecord.detailPresentation.detailFooterText {
            Text(footer)
              .font(TrawlDesign.Typography.smallLabel)
              .foregroundStyle(.secondary)
              .textSelection(.enabled)
          }
        }
        .padding(18)
        .frame(maxWidth: TrawlDesign.recordReadingWidth, alignment: .leading)
        .frame(maxWidth: .infinity, alignment: .leading)
      }
      .onAppear {
        guard shouldScrollToTargetAnchor else { return }
        proxy.scrollTo(targetAnchor.recordAnchorIdentifier, anchor: .top)
      }
    }
  }

  /// A document record (email, post, transaction) opens at the top so the
  /// subject, metadata grid, and start of the body are immediately readable.
  /// Scrolling to `.center` on a long body drops the reader halfway down the
  /// page. Only jump when targeting a specific field below the header.
  private var shouldScrollToTargetAnchor: Bool {
    let anchor = targetAnchor.recordAnchorIdentifier
    guard !anchor.isEmpty else { return false }
    if anchor == openedRecord.detailPresentation.detailDisplayNameAnchor?.recordAnchorIdentifier {
      return false
    }
    if anchor == openedRecord.detailPresentation.bodyAnchor?.recordAnchorIdentifier {
      return false
    }
    return true
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
      case .archiveRecordAssociatedTime(let associatedTime):
        switch associatedTime {
        case .exactTime(let time):
          Text(time, format: .dateTime.year().month().day().hour().minute())
        case .calendarDate(let date):
          Text(date, format: .dateTime.year().month().day())
        }
      case .exactMonetaryAmount(let amount):
        Text(amount.formattedDisplayString)
          .monospacedDigit()
      case .globallyRoutableTrawlLink(let link):
        if let onOpenLink {
          Button(link.globallyRoutableTrawlLink) {
            onOpenLink(link)
          }
          .buttonStyle(.link)
        } else {
          Text(link.globallyRoutableTrawlLink)
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
      Text(RichBodyText.attributedString(for: text))
        .font(TrawlDesign.Typography.body)
        .lineSpacing(3)
        .textSelection(.enabled)
        .fixedSize(horizontal: false, vertical: true)
    case .unavailableExplanation:
      ContentUnavailableView(
        OperationalCopy.Record.unavailableTitle,
        systemImage: "doc.questionmark",
        description: Text(OperationalCopy.Record.unavailableDetail))
    }
  }
}
