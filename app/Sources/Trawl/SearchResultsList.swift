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
  let appFilterChips: [SearchAppFilterChip]
  let allAppsSearchMatchCountText: String?
  let appFilterSelection: RegisteredTrawlerIdentity?
  let onSelectAppFilter: (RegisteredTrawlerIdentity?) -> Void
  let activeDateRangeLabel: String?
  let onClearDateRange: () -> Void
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

      SearchAppFilterChipStrip(
        chips: appFilterChips,
        allAppsSearchMatchCountText: allAppsSearchMatchCountText,
        selection: appFilterSelection,
        onSelect: onSelectAppFilter,
        activeDateRangeLabel: activeDateRangeLabel,
        onClearDateRange: onClearDateRange
      )

      Divider()

      List(selection: $selectedSearchMatchIdentifier) {
        if searchMatchesAreNewestFirst {
          ForEach(groupedSections) { section in
            Section(section.title) {
              searchResultRows(for: section.matches)
            }
          }
        } else {
          searchResultRows(for: searchMatches)
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

  /// Month headings describe a list in date order. A search for words comes
  /// back in order of relevance, where a heading for every row is noise.
  private var searchMatchesAreNewestFirst: Bool {
    let times = searchMatches.compactMap(\.time)
    return zip(times, times.dropFirst()).allSatisfy { $0 >= $1 }
  }

  private func searchResultRows(for matches: [SearchMatch]) -> some View {
    ForEach(matches) { searchMatch in
      SearchResultRow(
        searchMatch: searchMatch,
        rawTitle: title(searchMatch),
        registeredTrawlerDisplayName: trawlerDisplayName(
          searchMatch.registeredTrawler),
        showsTrawlerDisplayName: showsTrawlerDisplayName,
        isSelected: searchMatch.id == selectedSearchMatchIdentifier
      )
      .tag(searchMatch.id)
    }
  }

  @ViewBuilder
  private var paginationFooter: some View {
    if let loadMoreFailureMessage {
      HStack(spacing: 8) {
        Image(systemName: "exclamationmark.triangle.fill")
          .foregroundStyle(TrawlDesign.Colors.warningAmber)
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
        HStack(alignment: .firstTextBaseline, spacing: TrawlDesign.Space.xs) {
          Text(base)
            .font(TrawlDesign.Typography.bodyEmphasis)
            .foregroundStyle(.primary)
          if showsUpToTodayContext {
            Text("· Up to today")
          }
        }
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
    .padding(.top, 10)
    .padding(.bottom, 8)
  }
}

struct SearchAppFilterChip: Identifiable, Equatable {
  let registeredTrawler: RegisteredTrawlerIdentity
  let registeredTrawlerDisplayName: String
  let searchMatchCountText: String

  var id: RegisteredTrawlerIdentity { registeredTrawler }
}

/// One chip for all apps and one for each app with matches, in the same order
/// as the sidebar, then the active date range as a red chip that clears it.
private struct SearchAppFilterChipStrip: View {
  let chips: [SearchAppFilterChip]
  let allAppsSearchMatchCountText: String?
  let selection: RegisteredTrawlerIdentity?
  let onSelect: (RegisteredTrawlerIdentity?) -> Void
  let activeDateRangeLabel: String?
  let onClearDateRange: () -> Void

  private static let chipHeight: CGFloat = 26

  var body: some View {
    ScrollView(.horizontal, showsIndicators: false) {
      HStack(spacing: 6) {
        appChip(
          registeredTrawler: nil,
          name: "All",
          countText: allAppsSearchMatchCountText,
          isSelected: selection == nil)
        ForEach(chips) { chip in
          appChip(
            registeredTrawler: chip.registeredTrawler,
            name: chip.registeredTrawlerDisplayName,
            countText: chip.searchMatchCountText,
            isSelected: selection == chip.registeredTrawler)
        }
        if let activeDateRangeLabel {
          Button(action: onClearDateRange) {
            HStack(spacing: 5) {
              Text(activeDateRangeLabel)
              Image(systemName: "xmark")
                .imageScale(.small)
            }
            .font(TrawlDesign.Typography.meta.weight(.medium))
            .foregroundStyle(TrawlDesign.Colors.matchRed)
            .padding(.horizontal, 8)
            .frame(height: Self.chipHeight)
            .background(TrawlDesign.Colors.matchRed.opacity(0.10), in: TrawlDesign.Shape.control)
            .contentShape(TrawlDesign.Shape.control)
          }
          .buttonStyle(.plain)
          .help("Clear dates")
          .accessibilityLabel("Clear dates: \(activeDateRangeLabel)")
        }
      }
      .padding(.horizontal, 12)
      .padding(.bottom, 8)
    }
  }

  private func appChip(
    registeredTrawler: RegisteredTrawlerIdentity?,
    name: String,
    countText: String?,
    isSelected: Bool
  ) -> some View {
    Button {
      onSelect(registeredTrawler)
    } label: {
      HStack(spacing: 5) {
        if let registeredTrawler {
          TrawlerIconView(registeredTrawler: registeredTrawler, size: 14)
        }
        Text(name)
          .font(TrawlDesign.Typography.meta.weight(.medium))
          .foregroundStyle(isSelected ? Color.white : Color.primary)
        if let countText {
          Text(countText)
            .font(TrawlDesign.Typography.smallNumber)
            .foregroundStyle(isSelected ? Color.white.opacity(0.85) : Color.secondary)
        }
      }
      .padding(.horizontal, 8)
      .frame(height: Self.chipHeight)
      .background(
        isSelected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.messageBubble,
        in: TrawlDesign.Shape.control
      )
      .contentShape(TrawlDesign.Shape.control)
    }
    .buttonStyle(.plain)
    .accessibilityLabel(countText.map { "\(name), \($0)" } ?? name)
    .accessibilityAddTraits(isSelected ? .isSelected : [])
  }
}

private struct SearchResultRow: View {
  @Environment(\.locale) private var locale

  let searchMatch: SearchMatch
  let rawTitle: String
  let registeredTrawlerDisplayName: String
  let showsTrawlerDisplayName: Bool
  let isSelected: Bool

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
        HStack(alignment: .firstTextBaseline, spacing: TrawlDesign.Space.sm) {
          Text(displayTitle)
            .font(TrawlDesign.Typography.itemTitle)
            .foregroundStyle(.primary)
            .lineLimit(1)
          Spacer(minLength: 0)
          if let formattedTime {
            Text(formattedTime)
              .font(TrawlDesign.Typography.smallNumber)
              .foregroundStyle(.secondary)
              .fixedSize()
          }
        }

        if let matchingTextLine {
          matchingTextLine
            .font(TrawlDesign.Typography.body)
            .foregroundStyle(.secondary)
            .lineLimit(1)
        } else if !peopleAndPlacesLine.isEmpty {
          Text(peopleAndPlacesLine)
            .font(TrawlDesign.Typography.body)
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }
      }
    }
    .padding(.vertical, 8)
    .accessibilityElement(children: .combine)
    .accessibilityLabel(accessibilityLabel)
  }

  /// Shown when the record has no text to quote: who it involves, or where
  /// it is filed, and its place.
  private var peopleAndPlacesLine: String {
    let container = digitalContainerNamesNearestToBroadestText
    return [
      cleanedPeopleSummary(excludingContainer: container),
      physicalPlaceNamesSpecificToBroadestText,
    ]
    .filter { !$0.isEmpty }
    .joined(separator: " · ")
  }

  private var senderDisplayName: String? {
    searchMatch.searchMatchPresentation.peopleRelatedToMatchingRecord
      .filter { $0.personRoleInArchiveRecord == .sender || $0.personRoleInArchiveRecord == .author }
      .map {
        Self.stripRedundantEmailEcho(
          $0.personDisplayName.trimmingCharacters(in: .whitespacesAndNewlines))
      }
      .first {
        !$0.isEmpty
          && !Self.isRawChatIdentifier($0)
          && $0.localizedCaseInsensitiveCompare(displayTitle) != .orderedSame
      }
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
      formattedTime,
      peopleAndPlacesLine,
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

  /// One line of the record's text, with the sender in front and the words
  /// that matched the search in red.
  private var matchingTextLine: Text? {
    let fields = searchMatch.searchMatchPresentation.searchMatchTextFieldsInDisplayOrder
    let fieldToQuote =
      fields.first {
        $0.searchMatchTextFragmentsInDisplayOrder.contains(
          where: \.searchMatchTextFragmentMatchesSearchQuery)
      }
      ?? fields.first {
        $0.searchMatchTextFragmentsInDisplayOrder.contains {
          !$0.searchMatchTextFragmentContent
            .trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
      }
    guard let fieldToQuote else { return nil }

    var pieces = fieldToQuote.searchMatchTextFragmentsInDisplayOrder
      .map {
        QuotedTextPiece(
          text: $0.searchMatchTextFragmentContent.replacing(#/\s+/#, with: " "),
          matchesSearchQuery: $0.searchMatchTextFragmentMatchesSearchQuery)
      }
      .filter { !$0.text.isEmpty }
    if let firstMatchingPiece = pieces.firstIndex(where: \.matchesSearchQuery),
      firstMatchingPiece > 0
    {
      let textBeforeFirstMatch = pieces[..<firstMatchingPiece].map(\.text).joined()
      pieces.replaceSubrange(
        ..<firstMatchingPiece,
        with: [
          QuotedTextPiece(
            text: Self.endOfTextBeforeFirstMatch(textBeforeFirstMatch),
            matchesSearchQuery: false)
        ])
    }
    if let firstPiece = pieces.first {
      pieces[0].text = String(firstPiece.text.drop(while: \.isWhitespace))
    }
    guard pieces.contains(where: { !$0.text.isEmpty }) else { return nil }

    let fieldName = fieldToQuote.searchMatchTextFieldName
      .trimmingCharacters(in: .whitespacesAndNewlines)
    var line = Text("")
    if let lineLabel = senderDisplayName ?? (fieldName.isEmpty ? nil : fieldName) {
      line = Text("\(lineLabel): ")
    }
    for piece in pieces {
      var pieceText = Text(piece.text)
      if piece.matchesSearchQuery {
        // Red is unreadable on the selected row's accent fill.
        pieceText =
          isSelected
          ? pieceText.bold().underline()
          : pieceText.bold().foregroundColor(TrawlDesign.Colors.matchRed)
      }
      line = Text("\(line)\(pieceText)")
    }
    return line
  }

  private struct QuotedTextPiece {
    var text: String
    let matchesSearchQuery: Bool
  }

  /// Keeps the first matched word on the visible part of a one-line row.
  private static func endOfTextBeforeFirstMatch(_ text: String) -> String {
    let longestTextBeforeFirstMatch = 28
    guard text.count > longestTextBeforeFirstMatch else { return text }
    let ending = text.suffix(longestTextBeforeFirstMatch)
    guard let firstSpace = ending.firstIndex(of: " ") else { return "…\(ending)" }
    return "…\(ending[ending.index(after: firstSpace)...])"
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
