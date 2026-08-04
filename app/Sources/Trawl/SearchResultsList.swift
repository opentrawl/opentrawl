import SwiftUI
import TrawlClient
import TrawlCore

enum SearchResultBounds {
  static func copy(resultCount: Int, resultLimit: UInt32) -> String {
    let shown = min(resultCount, Int(resultLimit))
    return switch shown {
    case 0: "Showing no results"
    case 1: "Showing 1 result"
    default: "Showing \(shown) results"
    }
  }
}

struct SearchResultsList: View {
  let phase: SearchPhase
  let searchMatches: [SearchMatch]
  let trawlerDisplayName: (RegisteredTrawlerIdentity?) -> String
  let showsTrawlerDisplayName: Bool
  let committedQuery: String?
  let resultLimit: UInt32
  let title: (SearchMatch) -> String
  @Binding var selectedSearchMatchIdentifier: SearchMatch.ID?
  @FocusState.Binding var focus: SearchFocus?
  let onReturn: () -> Void
  let onEscape: () -> Void
  let onOpen: (SearchMatch) -> Void
  let onSelectionChanged: (SearchMatch) -> Void

  var body: some View {
    GeometryReader { proxy in
      ScrollView {
        LazyVStack(spacing: 0) {
          SearchResultsContext(
            phase: phase,
            resultCount: searchMatches.count,
            resultLimit: resultLimit,
            committedQuery: committedQuery
          )
          ForEach(searchMatches) { searchMatch in
            Button {
              selectedSearchMatchIdentifier = searchMatch.id
              onOpen(searchMatch)
            } label: {
              SearchResultRow(
                searchMatch: searchMatch,
                title: title(searchMatch),
                registeredTrawlerDisplayName: trawlerDisplayName(
                  searchMatch.registeredTrawler),
                showsTrawlerDisplayName: showsTrawlerDisplayName,
                isSelected: selectedSearchMatchIdentifier == searchMatch.id
              )
            }
            .buttonStyle(.plain)
            Divider()
          }
        }
        .frame(
          width: min(proxy.size.width, TrawlDesign.recordReadingWidth),
          alignment: .leading
        )
      }
      .frame(maxWidth: .infinity, alignment: .leading)
    }
    .focused($focus, equals: .results)
    .onKeyPress(.upArrow) {
      moveSelection(by: -1)
      return .handled
    }
    .onKeyPress(.downArrow) {
      moveSelection(by: 1)
      return .handled
    }
    .onKeyPress(.return) {
      guard selectedSearchMatchIdentifier != nil else { return .ignored }
      onReturn()
      return .handled
    }
    .onExitCommand(perform: onEscape)
    .frame(maxWidth: .infinity, maxHeight: .infinity)
  }

  private func moveSelection(by offset: Int) {
    guard !searchMatches.isEmpty else { return }
    let currentIndex =
      selectedSearchMatchIdentifier.flatMap { selectedIdentifier in
        searchMatches.firstIndex(where: { $0.id == selectedIdentifier })
      } ?? (offset > 0 ? -1 : searchMatches.count)
    let nextIndex = min(max(currentIndex + offset, 0), searchMatches.count - 1)
    selectedSearchMatchIdentifier = searchMatches[nextIndex].id
    onSelectionChanged(searchMatches[nextIndex])
  }
}

private struct SearchResultsContext: View {
  let phase: SearchPhase
  let resultCount: Int
  let resultLimit: UInt32
  let committedQuery: String?

  @ViewBuilder
  var body: some View {
    VStack(alignment: .leading, spacing: 4) {
      if let retained = OperationalCopy.Search.retainedResults(
        for: phase,
        query: committedQuery
      ) {
        Label(retained, systemImage: "magnifyingglass")
      } else if resultLimit > 0 {
        Text(SearchResultBounds.copy(resultCount: resultCount, resultLimit: resultLimit))
      }
      if case .partial = phase {
        Label(OperationalCopy.Search.partialResults, systemImage: "exclamationmark.triangle")
          .font(.caption)
      }
    }
    .font(.callout)
    .foregroundStyle(.secondary)
    .fixedSize(horizontal: false, vertical: true)
    .padding(.horizontal, 14)
    .padding(.vertical, 8)
  }
}

private struct SearchResultRow: View {
  @Environment(\.locale) private var locale

  let searchMatch: SearchMatch
  let title: String
  let registeredTrawlerDisplayName: String
  let showsTrawlerDisplayName: Bool
  let isSelected: Bool

  var body: some View {
    HStack(alignment: .top, spacing: 10) {
      if let registeredTrawler = searchMatch.registeredTrawler {
        TrawlerIconView(registeredTrawler: registeredTrawler, size: 24)
      }
      VStack(alignment: .leading, spacing: 3) {
        if showsTrawlerDisplayName {
          Text(registeredTrawlerDisplayName)
            .font(.caption.weight(.semibold))
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }
        HStack(alignment: .firstTextBaseline, spacing: 8) {
          Text(title)
            .font(.body.weight(.semibold))
            .lineLimit(2)
          Spacer(minLength: 4)
          if let time = searchMatch.time {
            Text(
              time,
              format: searchMatch.associatedTimeHasNoTimeOfDay
                ? .dateTime.year().month().day()
                : .dateTime.month().day().hour().minute()
            )
            .font(.caption)
            .foregroundStyle(.tertiary)
          }
        }
        if !peopleRelatedToMatchingRecordText.isEmpty {
          Text(peopleRelatedToMatchingRecordText)
            .font(.callout)
            .foregroundStyle(.secondary)
            .lineLimit(2)
        }
        if !physicalPlaceNamesSpecificToBroadestText.isEmpty {
          Text("Place: \(physicalPlaceNamesSpecificToBroadestText)")
            .font(.callout)
            .foregroundStyle(.secondary)
            .lineLimit(2)
        }
        if !digitalContainerNamesNearestToBroadestText.isEmpty {
          Text("In: \(digitalContainerNamesNearestToBroadestText)")
            .font(.callout)
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }
        ForEach(
          Array(
            searchMatch.searchMatchPresentation.searchMatchTextFieldsInDisplayOrder.enumerated()),
          id: \.offset
        ) { _, searchMatchTextField in
          if let fieldLabelledSearchMatchTextEvidence =
            fieldLabelledSearchMatchTextEvidence(searchMatchTextField)
          {
            fieldLabelledSearchMatchTextEvidence
              .font(.callout)
              .foregroundStyle(.secondary)
              .lineLimit(2)
          }
        }
      }
    }
    .padding(.vertical, 7)
    .padding(.horizontal, 10)
    .background(
      isSelected ? TrawlDesign.brandRed.opacity(0.12) : .clear,
      in: RoundedRectangle(cornerRadius: 8)
    )
    .overlay {
      if isSelected {
        RoundedRectangle(cornerRadius: 8)
          .stroke(TrawlDesign.brandRed.opacity(0.28), lineWidth: 1)
      }
    }
    .padding(.horizontal, 5)
    .contentShape(.rect)
    .accessibilityElement(children: .combine)
    .accessibilityLabel(accessibilityLabel)
    .accessibilityValue(isSelected ? "Selected" : "Not selected")
    .accessibilityAddTraits(isSelected ? .isSelected : [])
  }

  private var accessibilityLabel: String {
    var accessibleSearchResultFacts = [
      showsTrawlerDisplayName ? registeredTrawlerDisplayName : nil,
      title,
      formattedTime,
      peopleRelatedToMatchingRecordText,
      physicalPlaceNamesSpecificToBroadestText,
      digitalContainerNamesNearestToBroadestText,
    ]
      .compactMap { $0 }
      .filter { !$0.isEmpty }
    accessibleSearchResultFacts.append(
      contentsOf: fieldLabelledSearchMatchTextEvidenceInDisplayOrder)
    return accessibleSearchResultFacts.joined(separator: ". ")
  }

  private var peopleRelatedToMatchingRecordText: String {
    searchMatch.searchMatchPresentation.peopleRelatedToMatchingRecord.compactMap {
      personRelatedToMatchingRecord in
      let personDisplayName =
        personRelatedToMatchingRecord.personDisplayName
        .trimmingCharacters(in: .whitespacesAndNewlines)
      guard !personDisplayName.isEmpty else { return nil }
      guard
        let personRoleDisplayName = personRoleDisplayName(
          personRelatedToMatchingRecord.personRoleInArchiveRecord)
      else { return personDisplayName }
      return "\(personRoleDisplayName): \(personDisplayName)"
    }
    .joined(separator: " · ")
  }

  private var physicalPlaceNamesSpecificToBroadestText: String {
    searchMatch.searchMatchPresentation.physicalPlaceNamesSpecificToBroadest
      .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
      .filter { !$0.isEmpty }
      .joined(separator: " › ")
  }

  private var digitalContainerNamesNearestToBroadestText: String {
    searchMatch.searchMatchPresentation.digitalContainerNamesNearestToBroadest
      .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
      .filter { !$0.isEmpty }
      .joined(separator: " › ")
  }

  private var fieldLabelledSearchMatchTextEvidenceInDisplayOrder: [String] {
    searchMatch.searchMatchPresentation.searchMatchTextFieldsInDisplayOrder.compactMap {
      searchMatchTextField in
      let searchMatchTextFieldName =
        searchMatchTextField.searchMatchTextFieldName
        .trimmingCharacters(in: .whitespacesAndNewlines)
      let searchMatchTextFieldContent =
        searchMatchTextField.searchMatchTextFragmentsInDisplayOrder
        .map(\.searchMatchTextFragmentContent)
        .joined()
        .trimmingCharacters(in: .whitespacesAndNewlines)
      switch (searchMatchTextFieldName.isEmpty, searchMatchTextFieldContent.isEmpty) {
      case (false, false): return "\(searchMatchTextFieldName): \(searchMatchTextFieldContent)"
      case (false, true): return searchMatchTextFieldName
      case (true, false): return searchMatchTextFieldContent
      case (true, true): return nil
      }
    }
  }

  private func fieldLabelledSearchMatchTextEvidence(
    _ searchMatchTextField: SearchMatchTextField
  ) -> Text? {
    let searchMatchTextFieldName =
      searchMatchTextField.searchMatchTextFieldName
      .trimmingCharacters(in: .whitespacesAndNewlines)
    let nonEmptySearchMatchTextFragments =
      searchMatchTextField.searchMatchTextFragmentsInDisplayOrder
      .filter { !$0.searchMatchTextFragmentContent.isEmpty }
    guard !searchMatchTextFieldName.isEmpty || !nonEmptySearchMatchTextFragments.isEmpty
    else { return nil }

    var fieldLabelledSearchMatchTextEvidence = Text("")
    if !searchMatchTextFieldName.isEmpty {
      fieldLabelledSearchMatchTextEvidence = Text(
        nonEmptySearchMatchTextFragments.isEmpty
          ? searchMatchTextFieldName
          : "\(searchMatchTextFieldName): ")
    }
    for searchMatchTextFragment in nonEmptySearchMatchTextFragments {
      var searchMatchTextFragmentText = Text(
        searchMatchTextFragment.searchMatchTextFragmentContent)
      if searchMatchTextFragment.searchMatchTextFragmentMatchesSearchQuery {
        searchMatchTextFragmentText = searchMatchTextFragmentText
          .bold()
          .foregroundColor(TrawlDesign.brandRed)
      }
      fieldLabelledSearchMatchTextEvidence =
        Text("\(fieldLabelledSearchMatchTextEvidence)\(searchMatchTextFragmentText)")
    }
    return fieldLabelledSearchMatchTextEvidence
  }

  private func personRoleDisplayName(
    _ personRole: PersonRoleInArchiveRecord?
  ) -> String? {
    switch personRole {
    case .sender: "Sender"
    case .recipient: "Recipient"
    case .author: "Author"
    case .organizer: "Organizer"
    case .attendee: "Attendee"
    case .participant: "Participant"
    case nil: nil
    }
  }

  private var formattedTime: String? {
    guard let time = searchMatch.time else { return nil }
    if searchMatch.associatedTimeHasNoTimeOfDay {
      return time.formatted(.dateTime.year().month().day().locale(locale))
    }
    return time.formatted(.dateTime.month().day().hour().minute().locale(locale))
  }
}
