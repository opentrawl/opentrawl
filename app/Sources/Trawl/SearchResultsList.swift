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
  let showsUpToTodayContext: Bool
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
    VStack(alignment: .leading, spacing: 0) {
      SearchResultsContext(
        phase: phase,
        resultCount: searchMatches.count,
        totalMatchedRecordCount: totalMatchedRecordCount,
        totalMatchedCountIsLowerBound: totalMatchedCountIsLowerBound,
        isTruncated: isTruncated,
        resultLimit: resultLimit,
        committedQuery: committedQuery,
        showsUpToTodayContext: showsUpToTodayContext
      )

      Divider()

      List(selection: $selectedSearchMatchIdentifier) {
        ForEach(groupedSections) { section in
          Section(section.title) {
            ForEach(section.matches) { searchMatch in
              SearchResultRow(
                searchMatch: searchMatch,
                rawTitle: title(searchMatch),
                registeredTrawlerDisplayName: trawlerDisplayName(
                  searchMatch.registeredTrawler),
                showsTrawlerDisplayName: showsTrawlerDisplayName
              )
              .tag(searchMatch.id)
            }
          }
        }

        if loadMoreFailureMessage != nil || isLoadingMore || canLoadMore {
          Section {
            paginationFooter
          }
        }
      }
      .listStyle(.inset)
      .focused($focus, equals: .results)
      .onChange(of: selectedSearchMatchIdentifier) { _, newID in
        guard let newID,
          let match = searchMatches.first(where: { $0.id == newID })
        else { return }
        onSelectionChanged(match)
      }
      .onKeyPress(.return) {
        guard selectedSearchMatchIdentifier != nil else { return .ignored }
        onReturn()
        return .handled
      }
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
  }

  @ViewBuilder
  private var paginationFooter: some View {
    if let loadMoreFailureMessage {
      HStack(spacing: 8) {
        Image(systemName: "exclamationmark.triangle.fill")
          .foregroundStyle(.orange)
        Text(loadMoreFailureMessage)
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(.secondary)
        Spacer()
        Button("Try again", action: onLoadMore)
          .buttonStyle(.bordered)
          .controlSize(.small)
      }
      .padding(.vertical, 6)
    } else if isLoadingMore {
      HStack(spacing: 8) {
        Spacer()
        ProgressView()
          .controlSize(.small)
        Text("Loading more results…")
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(.secondary)
        Spacer()
      }
      .padding(.vertical, 8)
    } else if canLoadMore {
      HStack {
        Spacer()
        Button("Load 50 more", action: onLoadMore)
          .buttonStyle(.bordered)
          .controlSize(.small)
        Spacer()
      }
      .padding(.vertical, 8)
    }
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
  let showsUpToTodayContext: Bool

  @ViewBuilder
  var body: some View {
    VStack(alignment: .leading, spacing: 4) {
      if let retained = OperationalCopy.Search.retainedResults(
        for: phase,
        query: committedQuery
      ) {
        Label(retained, systemImage: "magnifyingglass")
      } else if resultLimit > 0 {
        let base = SearchResultBounds.summary(
          shownCount: resultCount,
          totalCount: totalMatchedRecordCount,
          isLowerBound: totalMatchedCountIsLowerBound,
          isTruncated: isTruncated
        )
        Text(showsUpToTodayContext ? "\(base) · Up to today" : base)
      }
      if case .partial = phase {
        Label(OperationalCopy.Search.partialResults, systemImage: "exclamationmark.triangle")
          .font(TrawlDesign.Typography.smallLabel)
      }
    }
    .font(TrawlDesign.Typography.meta)
    .foregroundStyle(.secondary)
    .fixedSize(horizontal: false, vertical: true)
    .padding(.horizontal, 14)
    .padding(.vertical, 10)
  }
}

private struct SearchResultRow: View {
  @Environment(\.locale) private var locale

  let searchMatch: SearchMatch
  let rawTitle: String
  let registeredTrawlerDisplayName: String
  let showsTrawlerDisplayName: Bool

  private var displayTitle: String {
    let trimmed = rawTitle.trimmingCharacters(in: .whitespacesAndNewlines)
    if Self.isRawChatIdentifier(trimmed) {
      if let firstPerson = searchMatch.searchMatchPresentation.peopleRelatedToMatchingRecord
        .map({ $0.personDisplayName.trimmingCharacters(in: .whitespacesAndNewlines) })
        .first(where: { !$0.isEmpty && !Self.isRawChatIdentifier($0) })
      {
        return firstPerson
      }
      return "\(registeredTrawlerDisplayName) conversation"
    }
    return trimmed.isEmpty ? registeredTrawlerDisplayName : trimmed
  }

  var body: some View {
    HStack(alignment: .top, spacing: TrawlDesign.Space.sm) {
      if let registeredTrawler = searchMatch.registeredTrawler {
        TrawlerIconView(registeredTrawler: registeredTrawler, size: 22)
          .padding(.top, 1)
      }
      VStack(alignment: .leading, spacing: 4) {
        Text(displayTitle)
          .font(TrawlDesign.Typography.itemTitle)
          .foregroundStyle(.primary)
          .lineLimit(2)

        if !metadataLine.isEmpty {
          Text(metadataLine)
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }

        if !physicalPlaceNamesSpecificToBroadestText.isEmpty {
          Text(physicalPlaceNamesSpecificToBroadestText)
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }

        ForEach(
          Array(
            searchMatch.searchMatchPresentation.searchMatchTextFieldsInDisplayOrder.enumerated()),
          id: \.offset
        ) { _, searchMatchTextField in
          if let evidence = fieldLabelledSearchMatchTextEvidence(searchMatchTextField) {
            evidence
              .font(TrawlDesign.Typography.body)
              .foregroundStyle(.secondary)
              .lineLimit(2)
          }
        }
      }
    }
    .padding(.vertical, 8)
    .accessibilityElement(children: .combine)
    .accessibilityLabel(accessibilityLabel)
  }

  /// Combines timestamp, container, and people into one deduplicated metadata line
  /// so Mail never repeats sender addresses and Telegram never repeats the conversation title.
  private var metadataLine: String {
    var parts: [String] = []
    if let formattedTime {
      parts.append(formattedTime)
    }

    let container = digitalContainerNamesNearestToBroadestText
    let people = cleanedPeopleSummary(excludingContainer: container)

    if !people.isEmpty {
      parts.append(people)
    } else if !container.isEmpty,
      container.localizedCaseInsensitiveCompare(displayTitle) != .orderedSame,
      !Self.isRawChatIdentifier(container)
    {
      parts.append(container)
    }
    return parts.joined(separator: " · ")
  }

  private func cleanedPeopleSummary(excludingContainer container: String) -> String {
    var seen = Set<String>()
    var senders: [String] = []
    var others: [String] = []

    func normalizeKey(_ text: String) -> String {
      text.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    }

    let titleKey = normalizeKey(displayTitle)

    for person in searchMatch.searchMatchPresentation.peopleRelatedToMatchingRecord {
      let cleaned = Self.stripRedundantEmailEcho(
        person.personDisplayName.trimmingCharacters(in: .whitespacesAndNewlines)
      )
      let key = normalizeKey(cleaned)
      guard !key.isEmpty,
        key != titleKey,
        !Self.isRawChatIdentifier(cleaned),
        seen.insert(key).inserted
      else { continue }

      switch person.personRoleInArchiveRecord {
      case .sender:
        senders.append(cleaned)
      case .recipient:
        // Omit recipient when a sender is already present so inbox rows don't repeat the user's own address.
        if senders.isEmpty {
          others.append(cleaned)
        }
      case .organizer:
        others.append("Organizer: \(cleaned)")
      case .attendee:
        others.append("Attendee: \(cleaned)")
      case .author, .participant, nil:
        others.append(cleaned)
      }
    }

    if !senders.isEmpty {
      return senders.joined(separator: ", ")
    }
    if !others.isEmpty {
      return others.joined(separator: ", ")
    }
    if !container.isEmpty,
      normalizeKey(container) != titleKey,
      !Self.isRawChatIdentifier(container)
    {
      return container
    }
    return ""
  }

  private static func isRawChatIdentifier(_ text: String) -> Bool {
    guard text.hasPrefix("chat"), text.count >= 12 else { return false }
    let suffix = text.dropFirst(4)
    return !suffix.isEmpty && suffix.allSatisfy(\.isNumber)
  }

  private static func stripRedundantEmailEcho(_ text: String) -> String {
    // Collapses "Name <email@host>" or "Name email@host" down to just the clean display name
    // when a human name precedes the email address.
    let tokens = text.split(separator: " ")
    guard tokens.count >= 2, let last = tokens.last, last.contains("@") else {
      return text
    }
    let namePart = tokens.dropLast().joined(separator: " ")
      .trimmingCharacters(in: CharacterSet(charactersIn: " <>\""))
    return namePart.isEmpty ? text : namePart
  }

  private var accessibilityLabel: String {
    var facts = [
      showsTrawlerDisplayName ? registeredTrawlerDisplayName : nil,
      displayTitle,
      metadataLine,
      physicalPlaceNamesSpecificToBroadestText,
    ]
      .compactMap { $0 }
      .filter { !$0.isEmpty }
    facts.append(contentsOf: fieldLabelledSearchMatchTextEvidenceInDisplayOrder)
    return facts.joined(separator: ". ")
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
      let name = searchMatchTextField.searchMatchTextFieldName
        .trimmingCharacters(in: .whitespacesAndNewlines)
      let content = searchMatchTextField.searchMatchTextFragmentsInDisplayOrder
        .map(\.searchMatchTextFragmentContent)
        .joined()
        .trimmingCharacters(in: .whitespacesAndNewlines)
      switch (name.isEmpty, content.isEmpty) {
      case (false, false): return "\(name): \(content)"
      case (false, true): return name
      case (true, false): return content
      case (true, true): return nil
      }
    }
  }

  private func fieldLabelledSearchMatchTextEvidence(
    _ searchMatchTextField: SearchMatchTextField
  ) -> Text? {
    let fieldName = searchMatchTextField.searchMatchTextFieldName
      .trimmingCharacters(in: .whitespacesAndNewlines)
    let fragments = searchMatchTextField.searchMatchTextFragmentsInDisplayOrder
      .filter { !$0.searchMatchTextFragmentContent.isEmpty }
    guard !fieldName.isEmpty || !fragments.isEmpty else { return nil }

    var result = Text("")
    if !fieldName.isEmpty {
      result = Text(fragments.isEmpty ? fieldName : "\(fieldName): ")
        .foregroundStyle(.secondary)
    }
    for fragment in fragments {
      var piece = Text(fragment.searchMatchTextFragmentContent)
      if fragment.searchMatchTextFragmentMatchesSearchQuery {
        piece = piece
          .bold()
          .foregroundColor(TrawlDesign.brandRed)
      }
      result = Text("\(result)\(piece)")
    }
    return result
  }

  private var formattedTime: String? {
    guard let time = searchMatch.time else { return nil }
    if searchMatch.associatedTimeHasNoTimeOfDay {
      var format = Date.FormatStyle.dateTime.year().month(.abbreviated).day().locale(locale)
      format.timeZone = .gmt
      return time.formatted(format)
    }
    let isCurrentYear = Calendar.current.isDate(time, equalTo: Date(), toGranularity: .year)
    if isCurrentYear {
      return time.formatted(.dateTime.month(.abbreviated).day().hour().minute().locale(locale))
    }
    return time.formatted(.dateTime.year().month(.abbreviated).day().locale(locale))
  }
}
