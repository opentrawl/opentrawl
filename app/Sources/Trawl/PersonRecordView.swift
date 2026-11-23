import SwiftUI
import TrawlClient

struct PersonRecordView: View {
  let personRecord: PersonRecord
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
            title: personRecord.personDisplayName,
            subtitle: personRecord.alternativePersonDisplayNames.isEmpty
              ? nil : personRecord.alternativePersonDisplayNames.formatted(),
            trawlLink: trawlLink,
            showsAroundToggle: helperController != nil && trawlLink != nil,
            isShowingAround: isShowingAround,
            onToggleAround: { isShowingAround.toggle() }
          )
          .id(PersonRecordAnchorIdentifier.personDisplayName)

          if isShowingAround, let helperController, let trawlLink {
            AroundRelatedRecordsSection(
              trawlLink: trawlLink,
              helperController: helperController,
              onOpenLink: onOpenLink
            )
          }

          if personRecord.personRelationshipOrContextAnnotation != nil
            || !personRecord.trawlersContributingFactsToPersonRecord.isEmpty
          {
            RecordMetadataCard {
              if let annotation = personRecord.personRelationshipOrContextAnnotation {
                LabeledContent(
                  "Relationship or context",
                  value: annotation.personRelationshipOrContextDescription
                )
                .id(
                  PersonRecordAnchorIdentifier.personRelationshipOrContextDescription
                    .recordAnchorIdentifier)
                LabeledContent(
                  "Stated",
                  value: String(
                    format: "%04d-%02d-%02d",
                    annotation.personRelationshipOrContextDescriptionStatedDate.calendarYear,
                    annotation.personRelationshipOrContextDescriptionStatedDate.calendarMonthNumber,
                    annotation.personRelationshipOrContextDescriptionStatedDate.calendarDayOfMonth)
                )
              }
              if !personRecord.trawlersContributingFactsToPersonRecord.isEmpty {
                LabeledContent(
                  "Apps",
                  value: personRecord.trawlersContributingFactsToPersonRecord.map(
                    \.registeredTrawlerDisplayName
                  ).formatted()
                )
              }
            }
            .font(.callout)
          }

          if !personRecord.personContactMethodsInDisplayOrder.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
              Text(
                "Contact",
                comment: "Heading above a person's contact methods."
              )
              .font(.caption.weight(.semibold))
              .foregroundStyle(.secondary)
              RecordMetadataCard {
                ForEach(personRecord.personContactMethodsInDisplayOrder) { contactMethod in
                  PersonContactMethodRow(contactMethod: contactMethod)
                    .id(
                      contactMethod.personContactMethodKind.recordAnchor.recordAnchorIdentifier)
                }
              }
            }
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

private struct PersonContactMethodRow: View {
  let contactMethod: PersonContactMethod

  var body: some View {
    HStack(alignment: .firstTextBaseline, spacing: 16) {
      VStack(alignment: .leading, spacing: 2) {
        if !contactMethod.personContactMethodLabel.isEmpty {
          Text(contactMethod.personContactMethodLabel)
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        switch contactMethod.personContactMethodKind {
        case .emailAddress:
          Text(
            "Email",
            comment: "Label for a person's email address.")
        case .phoneNumber:
          Text(
            "Phone",
            comment: "Label for a person's phone number.")
        case .postalAddress:
          Text(
            "Address",
            comment: "Label for a person's postal address.")
        case .accountIdentifier:
          Text(
            "Account",
            comment: "Label for a person's account handle.")
        }
      }
      .font(.callout)
      .foregroundStyle(.secondary)
      .frame(width: 112, alignment: .leading)
      Text(contactMethod.personContactMethodDisplayValue)
        .font(.callout)
        .textSelection(.enabled)
      Spacer(minLength: 0)
    }
  }
}
