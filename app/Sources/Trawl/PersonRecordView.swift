import SwiftUI
import TrawlClient

struct PersonRecordView: View {
  let personRecord: PersonRecord
  let targetAnchor: RecordAnchorIdentifier

  var body: some View {
    ScrollViewReader { proxy in
      ScrollView {
        VStack(alignment: .leading, spacing: 16) {
          Text(personRecord.personDisplayName)
            .font(.title2)
            .textSelection(.enabled)
            .id(PersonRecordAnchorIdentifier.personDisplayName)
          if !personRecord.alternativePersonDisplayNames.isEmpty {
            LabeledContent(
              "Known as",
              value: personRecord.alternativePersonDisplayNames.formatted()
            )
            .id(PersonRecordAnchorIdentifier.alternativePersonDisplayName)
          }
          if !personRecord.personFactContributingTrawlerDisplayNames.isEmpty {
            LabeledContent(
              "Trawlers",
              value: personRecord.personFactContributingTrawlerDisplayNames.formatted()
            )
          }
          if !personRecord.personContactMethodsInDisplayOrder.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
              Text(
                "Contact", bundle: #bundle,
                comment: "Heading above a person's contact methods."
              )
              .font(.headline)
              ForEach(personRecord.personContactMethodsInDisplayOrder) { contactMethod in
                PersonContactMethodRow(contactMethod: contactMethod)
                  .id(
                    contactMethod.personContactMethodKind.recordAnchor.recordAnchorIdentifier)
              }
            }
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
            "Email", bundle: #bundle,
            comment: "Label for a person's email address.")
        case .phoneNumber:
          Text(
            "Phone", bundle: #bundle,
            comment: "Label for a person's phone number.")
        case .postalAddress:
          Text(
            "Address", bundle: #bundle,
            comment: "Label for a person's postal address.")
        case .accountIdentifier:
          Text(
            "Account", bundle: #bundle,
            comment: "Label for a person's account handle.")
        }
      }
      .frame(width: 112, alignment: .leading)
      Text(contactMethod.personContactMethodDisplayValue)
        .textSelection(.enabled)
      Spacer(minLength: 0)
    }
  }
}
