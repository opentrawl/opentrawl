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

/// Formats a `SearchMatchPresentation` into crisp native macOS row components,
/// stripping redundant key-value prefixes (`Sender:`, `In:`, `message:`, `body:`, `title:`).
enum SearchMatchRowFormatter {
  private static let redundantPrimaryFieldNames: Set<String> = [
    "message",
    "body",
    "text",
    "content",
    "description",
    "title",
    "note",
    "tweet",
    "summary",
    "snippet",
  ]

  static func contextSubtitle(for searchMatch: SearchMatch, title: String) -> String? {
    let presentation = searchMatch.searchMatchPresentation
    let normalizedTitle = title.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()

    var segments: [String] = []

    if let peopleSegment = formattedPeopleSegment(
      presentation.peopleRelatedToMatchingRecord,
      normalizedTitle: normalizedTitle
    ) {
      segments.append(peopleSegment)
    }

    let containers = presentation.digitalContainerNamesNearestToBroadest
      .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
      .filter { !$0.isEmpty && $0.lowercased() != normalizedTitle }
    if !containers.isEmpty {
      segments.append("in \(containers.joined(separator: " › "))")
    }

    let places = presentation.physicalPlaceNamesSpecificToBroadest
      .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
      .filter { !$0.isEmpty && $0.lowercased() != normalizedTitle }
    if !places.isEmpty {
      segments.append(places.joined(separator: " › "))
    }

    guard !segments.isEmpty else { return nil }
    return segments.joined(separator: " · ")
  }

  static func shouldShowFieldBadge(for fieldName: String) -> Bool {
    let trimmed = fieldName.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    guard !trimmed.isEmpty else { return false }
    return !redundantPrimaryFieldNames.contains(trimmed)
  }

  static func formattedFieldBadge(for fieldName: String) -> String {
    let trimmed = fieldName.trimmingCharacters(in: .whitespacesAndNewlines)
    if trimmed.lowercased() == "ocr" { return "OCR" }
    guard let first = trimmed.first else { return trimmed }
    return first.uppercased() + trimmed.dropFirst()
  }

  private static func formattedPeopleSegment(
    _ people: [PersonRelatedToArchiveRecord],
    normalizedTitle: String
  ) -> String? {
    let cleaned = people.compactMap { person -> (role: PersonRoleInArchiveRecord?, name: String)? in
      let name = person.personDisplayName.trimmingCharacters(in: .whitespacesAndNewlines)
      guard !name.isEmpty else { return nil }
      return (person.personRoleInArchiveRecord, name)
    }
    guard !cleaned.isEmpty else { return nil }

    let sender = cleaned.first { $0.role == .sender || $0.role == .author }?.name
    let recipient = cleaned.first { $0.role == .recipient }?.name

    if let sender {
      let senderIsMe = sender.localizedCaseInsensitiveCompare("me") == .orderedSame
      if senderIsMe {
        if let recipient, recipient.lowercased() != normalizedTitle {
          return "You → \(recipient)"
        }
        return "You"
      }
      if sender.lowercased() != normalizedTitle {
        return sender
      }
      if let recipient,
        recipient.localizedCaseInsensitiveCompare("me") != .orderedSame,
        recipient.lowercased() != normalizedTitle
      {
        return "→ \(recipient)"
      }
      return nil
    }

    let nonSelfNames = cleaned
      .map(\.name)
      .filter {
        $0.localizedCaseInsensitiveCompare("me") != .orderedSame
          && $0.lowercased() != normalizedTitle
      }
    guard !nonSelfNames.isEmpty else { return nil }
    return nonSelfNames.prefix(3).joined(separator: ", ")
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
    ScrollViewReader { proxy in
      ScrollView {
        LazyVStack(spacing: 4) {
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
            .id(searchMatch.id)
          }
        }
        .padding(.horizontal, 10)
        .padding(.bottom, 12)
      }
      .onChange(of: selectedSearchMatchIdentifier) { _, newSelection in
        guard let newSelection else { return }
        withAnimation(.easeOut(duration: 0.14)) {
          proxy.scrollTo(newSelection, anchor: .center)
        }
      }
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

  var body: some View {
    VStack(alignment: .leading, spacing: 4) {
      HStack(alignment: .center, spacing: 8) {
        if let retained = OperationalCopy.Search.retainedResults(
          for: phase,
          query: committedQuery
        ) {
          Label(retained, systemImage: "magnifyingglass")
        } else if resultLimit > 0 {
          Text(SearchResultBounds.copy(resultCount: resultCount, resultLimit: resultLimit))
        }
        Spacer(minLength: 4)
        Text("↑↓")
          .font(.caption2.monospaced())
          .foregroundStyle(.quaternary)
      }
      if case .partial = phase {
        Label(OperationalCopy.Search.partialResults, systemImage: "exclamationmark.triangle")
          .font(.caption)
          .foregroundStyle(.orange)
      }
    }
    .font(.caption.weight(.medium))
    .foregroundStyle(.secondary)
    .fixedSize(horizontal: false, vertical: true)
    .padding(.horizontal, 8)
    .padding(.top, 10)
    .padding(.bottom, 6)
  }
}

private struct SearchResultRow: View {
  @Environment(\.locale) private var locale

  let searchMatch: SearchMatch
  let title: String
  let registeredTrawlerDisplayName: String
  let showsTrawlerDisplayName: Bool
  let isSelected: Bool

  private var contextSubtitle: String? {
    SearchMatchRowFormatter.contextSubtitle(for: searchMatch, title: title)
  }

  private var primaryEvidenceField: SearchMatchTextField? {
    searchMatch.searchMatchPresentation.searchMatchTextFieldsInDisplayOrder.first { field in
      !field.searchMatchTextFragmentsInDisplayOrder.allSatisfy {
        $0.searchMatchTextFragmentContent.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
      }
    }
  }

  var body: some View {
    HStack(alignment: .top, spacing: 10) {
      Group {
        if let registeredTrawler = searchMatch.registeredTrawler {
          TrawlerIconView(registeredTrawler: registeredTrawler, size: 24)
        } else {
          Image(systemName: "doc.text.fill")
            .font(.system(size: 13))
            .foregroundStyle(.secondary)
            .frame(width: 24, height: 24)
            .background(TrawlDesign.surfaceBackground, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
        }
      }
      .padding(.top, 2)
      VStack(alignment: .leading, spacing: 3) {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
          if showsTrawlerDisplayName {
            Text(registeredTrawlerDisplayName)
              .font(.caption2.weight(.semibold))
              .foregroundStyle(.secondary)
              .lineLimit(1)
          }
          Spacer(minLength: 4)
          if let time = searchMatch.time {
            SmartResultTimestamp(
              date: time,
              dateOnly: searchMatch.associatedTimeHasNoTimeOfDay
            )
          }
        }
        Text(title)
          .font(.system(size: 13.5, weight: .semibold))
          .foregroundStyle(.primary)
          .lineLimit(1)

        if let contextSubtitle {
          Text(contextSubtitle)
            .font(.caption)
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }

        if let field = primaryEvidenceField, let snippet = renderedSnippet(for: field) {
          snippet
            .font(.callout)
            .foregroundStyle(.secondary)
            .lineLimit(2)
            .lineSpacing(1.5)
        }
      }
    }
    .padding(.vertical, 9)
    .padding(.horizontal, 10)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(
      isSelected ? TrawlDesign.brandRed.opacity(0.11) : Color.clear,
      in: RoundedRectangle(cornerRadius: 9, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: 9, style: .continuous)
        .strokeBorder(
          isSelected ? TrawlDesign.brandRed.opacity(0.32) : Color.clear,
          lineWidth: 1
        )
    )
    .contentShape(.rect)
    .accessibilityElement(children: .combine)
    .accessibilityLabel(accessibilityLabel)
    .accessibilityValue(isSelected ? "Selected" : "Not selected")
    .accessibilityAddTraits(isSelected ? .isSelected : [])
  }

  private func renderedSnippet(for searchMatchTextField: SearchMatchTextField) -> Text? {
    let nonEmptyFragments = searchMatchTextField.searchMatchTextFragmentsInDisplayOrder
      .filter { !$0.searchMatchTextFragmentContent.isEmpty }
    guard !nonEmptyFragments.isEmpty else { return nil }

    var combined = Text("")
    if SearchMatchRowFormatter.shouldShowFieldBadge(
      for: searchMatchTextField.searchMatchTextFieldName
    ) {
      let badge = SearchMatchRowFormatter.formattedFieldBadge(
        for: searchMatchTextField.searchMatchTextFieldName
      )
      combined = Text("\(badge) · ")
        .font(.caption.weight(.semibold))
        .foregroundColor(.secondary)
    }

    for fragment in nonEmptyFragments {
      var piece = Text(fragment.searchMatchTextFragmentContent)
      if fragment.searchMatchTextFragmentMatchesSearchQuery {
        piece = piece
          .bold()
          .foregroundColor(TrawlDesign.brandRed)
      }
      combined = Text("\(combined)\(piece)")
    }
    return combined
  }

  private var accessibilityLabel: String {
    var parts: [String] = []
    if showsTrawlerDisplayName { parts.append(registeredTrawlerDisplayName) }
    parts.append(title)
    if let formattedTime { parts.append(formattedTime) }
    if let contextSubtitle { parts.append(contextSubtitle) }
    if let field = primaryEvidenceField {
      let snippet = field.searchMatchTextFragmentsInDisplayOrder
        .map(\.searchMatchTextFragmentContent)
        .joined()
        .trimmingCharacters(in: .whitespacesAndNewlines)
      if !snippet.isEmpty { parts.append(snippet) }
    }
    return parts.joined(separator: ". ")
  }

  private var formattedTime: String? {
    guard let time = searchMatch.time else { return nil }
    if searchMatch.associatedTimeHasNoTimeOfDay {
      return time.formatted(.dateTime.year().month().day().locale(locale))
    }
    return time.formatted(.dateTime.month().day().hour().minute().locale(locale))
  }
}

private struct SmartResultTimestamp: View {
  let date: Date
  let dateOnly: Bool

  var body: some View {
    Group {
      if dateOnly {
        Text(date, format: .dateTime.day().month(.abbreviated).year())
      } else if Calendar.current.isDateInToday(date) {
        Text(date, format: .dateTime.hour().minute())
      } else if Calendar.current.isDate(date, equalTo: Date(), toGranularity: .year) {
        Text(date, format: .dateTime.day().month(.abbreviated).hour().minute())
      } else {
        Text(date, format: .dateTime.day().month(.abbreviated).year())
      }
    }
    .font(.caption2.monospacedDigit())
    .foregroundStyle(.tertiary)
    .lineLimit(1)
  }
}
