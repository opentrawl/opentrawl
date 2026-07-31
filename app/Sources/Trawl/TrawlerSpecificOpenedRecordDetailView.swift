import SwiftUI
import TrawlClient

struct TrawlerSpecificOpenedRecordDetailView: View {
  let openedRecord: TrawlerSpecificOpenedRecord
  let targetAnchor: RecordAnchorIdentifier

  var body: some View {
    ScrollViewReader { proxy in
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          Text(openedRecord.detailPresentation.detailDisplayName)
            .font(.title2)
            .textSelection(.enabled)
            .id(
              openedRecord.detailPresentation.detailDisplayNameAnchor?.recordAnchorIdentifier
                ?? openedRecord.detailPresentation.detailDisplayName)
          ForEach(
            Array(openedRecord.detailPresentation.fieldsInDisplayOrder.enumerated()),
            id: \.offset
          ) { fieldIndex, field in
            TrawlerSpecificOpenedRecordFieldView(field: field)
              .id(
                field.fieldAnchor?.recordAnchorIdentifier
                  ?? "trawler_specific_opened_record_field_\(fieldIndex)")
          }
          if let body = openedRecord.detailPresentation.body {
            TrawlerSpecificOpenedRecordBodyView(detailBody: body)
              .id(
                openedRecord.detailPresentation.bodyAnchor?.recordAnchorIdentifier
                  ?? openedRecord.detailPresentation.detailDisplayName)
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

private struct TrawlerSpecificOpenedRecordFieldView: View {
  let field: TrawlerSpecificCommandDetailPresentationField

  var body: some View {
    LabeledContent {
      TrawlerSpecificOpenedRecordFieldValueView(fieldValue: field.fieldValue)
    } label: {
      Text(field.fieldDisplayName)
        .foregroundStyle(.secondary)
    }
    .accessibilityElement(children: .combine)
  }
}

private struct TrawlerSpecificOpenedRecordFieldValueView: View {
  let fieldValue: TrawlerSpecificCommandPresentationValue

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
      case .globallyRoutableTrawlLink(let link):
        Text(link.globallyRoutableTrawlLink)
      }
    }
    .textSelection(.enabled)
  }
}

private struct TrawlerSpecificOpenedRecordBodyView: View {
  let detailBody: TrawlerSpecificCommandDetailPresentationBody

  var body: some View {
    switch detailBody {
    case .text(let text):
      Text(text)
        .textSelection(.enabled)
    case .unavailableExplanation(let explanation):
      ContentUnavailableView(
        "Record unavailable",
        systemImage: "doc.questionmark",
        description: Text(explanation))
    }
  }
}
