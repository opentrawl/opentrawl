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

  static func summary(
    shownCount: Int,
    totalCount: UInt64,
    isLowerBound: Bool,
    isTruncated: Bool
  ) -> String {
    guard shownCount > 0 else { return "Showing no results" }
    let effectiveTotal = max(UInt64(shownCount), totalCount)
    if isTruncated || effectiveTotal > UInt64(shownCount) {
      if isLowerBound {
        return "Showing \(shownCount.formatted()) of at least \(effectiveTotal.formatted())"
      }
      return "Showing \(shownCount.formatted()) of \(effectiveTotal.formatted())"
    }
    return shownCount == 1 ? "Showing 1 result" : "Showing \(shownCount.formatted()) results"
  }
}

struct SearchResultsList: View {
  let phase: SearchPhase
  let searchMatches: [SearchMatch]
  let totalMatchedRecordCount: UInt64
  let totalMatchedCountIsLowerBound: Bool
  let isTruncated: Bool
  let isLoadingMore: Bool
  let canLoadMore: Bool
  let loadMoreFailureMessage: String?
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
  let onLoadMore: () -> Void

  private struct MonthSection: Identifiable {
    let id: String
    let title: String
    let matches: [SearchMatch]
  }

  private var groupedSections: [MonthSection] {
    var sections: [MonthSection] = []
    var currentKey: String?
    var currentMatches: [SearchMatch] = []

    for match in searchMatches {
      let key: String
      if let time = match.time {
        key = time.formatted(.dateTime.month(.wide).year())
      } else {
        key = "Undated"
      }
      if key == currentKey {
        currentMatches.append(match)
      } else {
        if let existingKey = currentKey {
          sections.append(
            MonthSection(
              id: "\(sections.count)-\(existingKey)",
              title: existingKey,
              matches: currentMatches
            )
          )
        }
        currentKey = key
        currentMatches = [match]
      }
    }
    if let existingKey = currentKey {
      sections.append(
        MonthSection(
          id: "\(sections.count)-\(existingKey)",
          title: existingKey,
          matches: currentMatches
        )
      )
    }
    return sections
  }

  var body: some View {
    GeometryReader { proxy in
      ScrollViewReader { scrollProxy in
        ScrollView {
          LazyVStack(alignment: .leading, spacing: 0) {
            SearchResultsContext(
              phase: phase,
              resultCount: searchMatches.count,
              totalMatchedRecordCount: totalMatchedRecordCount,
              totalMatchedCountIsLowerBound: totalMatchedCountIsLowerBound,
              isTruncated: isTruncated,
              resultLimit: resultLimit,
              committedQuery: committedQuery
            )
            ForEach(groupedSections) { section in
              HStack {
                Text(section.title)
                  .font(TrawlDesign.Typography.caption.weight(.semibold))
                  .foregroundStyle(TrawlDesign.Colors.mutedInk)
                Spacer()
              }
              .padding(.horizontal, 14)
              .padding(.vertical, 6)
              .background(TrawlDesign.Colors.surface.opacity(0.65))

              ForEach(section.matches) { searchMatch in
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
                .id(searchMatch.id)
                .buttonStyle(.plain)
                Rectangle()
                  .fill(TrawlDesign.Colors.hairline.opacity(0.75))
                  .frame(height: 0.5)
                  .padding(.leading, 44)
              }
            }

            paginationFooter
          }
          .frame(
            width: min(proxy.size.width, TrawlDesign.recordReadingWidth),
            alignment: .leading
          )
        }
        .onChange(of: selectedSearchMatchIdentifier) { _, selectedID in
          guard let selectedID else { return }
          withAnimation(.easeInOut(duration: 0.14)) {
            scrollProxy.scrollTo(selectedID, anchor: .center)
          }
        }
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

  @ViewBuilder
  private var paginationFooter: some View {
    if let loadMoreFailureMessage {
      HStack(spacing: 8) {
        Image(systemName: "exclamationmark.triangle.fill")
          .foregroundStyle(TrawlDesign.Colors.warningAmber)
        Text(loadMoreFailureMessage)
          .font(.caption)
          .foregroundStyle(.secondary)
        Spacer()
        Button("Try again", action: onLoadMore)
          .buttonStyle(.bordered)
          .controlSize(.small)
      }
      .padding(12)
    } else if isLoadingMore {
      HStack(spacing: 8) {
        Spacer()
        ProgressView()
          .controlSize(.small)
        Text("Loading more results…")
          .font(.caption)
          .foregroundStyle(.secondary)
        Spacer()
      }
      .padding(.vertical, 14)
    } else if canLoadMore {
      HStack {
        Spacer()
        Button("Load 50 more", action: onLoadMore)
          .buttonStyle(.bordered)
          .controlSize(.small)
        Spacer()
      }
      .padding(.vertical, 14)
    }
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
  let totalMatchedRecordCount: UInt64
  let totalMatchedCountIsLowerBound: Bool
  let isTruncated: Bool
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
        Text(
          SearchResultBounds.summary(
            shownCount: resultCount,
            totalCount: totalMatchedRecordCount,
            isLowerBound: totalMatchedCountIsLowerBound,
            isTruncated: isTruncated
          )
        )
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
        TrawlerIconView(registeredTrawler: registeredTrawler, size: 22)
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
          if let formattedTime {
            Text(formattedTime)
              .font(.caption)
              .foregroundStyle(.tertiary)
              .fixedSize()
          }
        }
        if !peopleRelatedToMatchingRecordText.isEmpty {
          Text(peopleRelatedToMatchingRecordText)
            .font(.callout)
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }
        if !physicalPlaceNamesSpecificToBroadestText.isEmpty {
          Text(physicalPlaceNamesSpecificToBroadestText)
            .font(.callout)
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }
        if !digitalContainerNamesNearestToBroadestText.isEmpty,
          digitalContainerNamesNearestToBroadestText.localizedCaseInsensitiveCompare(title)
            != .orderedSame
        {
          Text(digitalContainerNamesNearestToBroadestText)
            .font(.caption)
            .foregroundStyle(.tertiary)
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
    .padding(.vertical, 9)
    .padding(.horizontal, 12)
    .frame(minHeight: 62, alignment: .topLeading)
    .background(
      isSelected ? TrawlDesign.Colors.selectBlueSurface : .clear,
      in: RoundedRectangle(cornerRadius: TrawlDesign.Radius.control)
    )
    .overlay {
      if isSelected {
        RoundedRectangle(cornerRadius: TrawlDesign.Radius.control)
          .stroke(TrawlDesign.Colors.selectBlue.opacity(0.32), lineWidth: 1)
      }
    }
    .padding(.horizontal, 6)
    .padding(.vertical, 2)
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
    var senders: [String] = []
    var recipients: [String] = []
    var others: [String] = []
    for person in searchMatch.searchMatchPresentation.peopleRelatedToMatchingRecord {
      let name = person.personDisplayName.trimmingCharacters(in: .whitespacesAndNewlines)
      guard !name.isEmpty else { continue }
      switch person.personRoleInArchiveRecord {
      case .sender:
        senders.append(name)
      case .recipient:
        recipients.append(name)
      case .organizer:
        others.append("Organizer: \(name)")
      case .attendee:
        others.append("Attendee: \(name)")
      case .author, .participant, nil:
        others.append(name)
      }
    }
    var segments: [String] = []
    if !senders.isEmpty && !recipients.isEmpty {
      segments.append("\(senders.joined(separator: ", ")) → \(recipients.joined(separator: ", "))")
    } else if !senders.isEmpty {
      segments.append(senders.joined(separator: ", "))
    } else if !recipients.isEmpty {
      segments.append("To \(recipients.joined(separator: ", "))")
    }
    segments.append(contentsOf: others)
    return segments.joined(separator: " · ")
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
          : "\(searchMatchTextFieldName): "
      )
      .foregroundStyle(.tertiary)
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

  private var formattedTime: String? {
    guard let time = searchMatch.time else { return nil }
    if searchMatch.associatedTimeHasNoTimeOfDay {
      return time.formatted(.dateTime.year().month().day().locale(locale))
    }
    let isCurrentYear = Calendar.current.isDate(time, equalTo: Date(), toGranularity: .year)
    if isCurrentYear {
      return time.formatted(.dateTime.month().day().hour().minute().locale(locale))
    }
    return time.formatted(.dateTime.year().month(.abbreviated).day().locale(locale))
  }
}
