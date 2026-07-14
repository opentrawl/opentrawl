import SwiftUI
import TrawlClient
import TrawlCore

enum SearchResultsContextCopy {
  static func retained(_ phase: SearchPhase, query: String?, failure: String?) -> String? {
    let prior = query ?? "the previous search"
    switch phase {
    case .loading: return "Showing results for \(prior) while searching"
    case .timedOut: return "Showing results for \(prior). The replacement search timed out."
    case .failed(let message): return "Showing results for \(prior). \(message)"
    default: return nil
    }
  }
}

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
  let results: [SearchHit]
  let sourceDisplayName: (String) -> String
  let failureGuidance: String?
  let hasTimeoutFailure: Bool
  let committedQuery: String?
  let resultLimit: UInt32
  let title: (SearchHit) -> String
  @Binding var selectedResultID: SearchHit.ID?
  @FocusState.Binding var focus: SearchFocus?
  let onReturn: () -> Void
  let onOpen: (SearchHit) -> Void
  let onSelectionChanged: (SearchHit) -> Void

  var body: some View {
    ScrollView {
      LazyVStack(spacing: 0) {
        SearchResultsContext(
          phase: phase,
          resultCount: results.count,
          resultLimit: resultLimit,
          failureGuidance: failureGuidance,
          hasTimeoutFailure: hasTimeoutFailure,
          committedQuery: committedQuery
        )
        ForEach(results) { hit in
          Button {
            selectedResultID = hit.id
            onOpen(hit)
          } label: {
            SearchResultRow(
              hit: hit,
              title: title(hit),
              sourceDisplayName: sourceDisplayName(hit.sourceID),
              isSelected: selectedResultID == hit.id
            )
          }
          .buttonStyle(.plain)
          Divider()
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
      guard selectedResultID != nil else { return .ignored }
      onReturn()
      return .handled
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
  }

  private func moveSelection(by offset: Int) {
    guard !results.isEmpty else { return }
    let currentIndex =
      selectedResultID.flatMap { selectedID in
        results.firstIndex(where: { $0.id == selectedID })
      } ?? (offset > 0 ? -1 : results.count)
    let nextIndex = min(max(currentIndex + offset, 0), results.count - 1)
    selectedResultID = results[nextIndex].id
    onSelectionChanged(results[nextIndex])
  }
}

private struct SearchResultsContext: View {
  let phase: SearchPhase
  let resultCount: Int
  let resultLimit: UInt32
  let failureGuidance: String?
  let hasTimeoutFailure: Bool
  let committedQuery: String?

  var body: some View {
    HStack(alignment: .firstTextBaseline, spacing: 10) {
      if case .partial = phase {
        Label(partialMessage, systemImage: "exclamationmark.triangle")
          .lineLimit(2)
      }
      Spacer(minLength: 8)
      if let retained = SearchResultsContextCopy.retained(phase, query: committedQuery, failure: failureGuidance) {
        Label(retained, systemImage: "magnifyingglass")
          .fixedSize()
      } else if resultLimit > 0 {
        Text(resultBounds)
          .fixedSize()
      }
    }
    .font(.callout)
    .foregroundStyle(.secondary)
    .padding(.horizontal, 14)
    .padding(.vertical, 10)
  }

  private var partialMessage: String {
    if hasTimeoutFailure { return "Some sources timed out." }
    return failureGuidance ?? "Some sources failed."
  }

  private var resultBounds: String {
    SearchResultBounds.copy(resultCount: resultCount, resultLimit: resultLimit)
  }
}

private struct SearchResultRow: View {
  @Environment(\.locale) private var locale

  let hit: SearchHit
  let title: String
  let sourceDisplayName: String
  let isSelected: Bool

  var body: some View {
    HStack(alignment: .top, spacing: 10) {
      SourceIconView(sourceID: hit.sourceID, size: 24)
      VStack(alignment: .leading, spacing: 3) {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
          Text(title)
            .font(.body.weight(.semibold))
            .lineLimit(2)
          Spacer(minLength: 4)
          if let time = hit.time {
            Text(
              time,
              format: hit.allDay
                ? .dateTime.year().month().day()
                : .dateTime.month().day().hour().minute()
            )
            .font(.caption)
            .foregroundStyle(.tertiary)
          }
        }
        if !hit.summary.subtitle.isEmpty {
          Text(hit.summary.subtitle)
            .font(.callout)
            .foregroundStyle(.secondary)
            .lineLimit(2)
        }
        Text(evidenceText)
          .font(.callout)
          .foregroundStyle(.secondary)
          .lineLimit(2)
      }
    }
    .padding(.vertical, 7)
    .padding(.horizontal, 10)
    .background(
      isSelected ? TrawlDesign.brandRed.opacity(0.08) : .clear,
      in: RoundedRectangle(cornerRadius: 8)
    )
    .padding(.horizontal, 5)
    .contentShape(.rect)
    .accessibilityElement(children: .combine)
    .accessibilityLabel(accessibilityLabel)
    .accessibilityValue(isSelected ? "Selected" : "Not selected")
    .accessibilityAddTraits(isSelected ? .isSelected : [])
  }

  private var accessibilityLabel: String {
    [sourceDisplayName, title, hit.summary.subtitle, formattedTime, evidenceText]
      .compactMap { $0 }
      .filter { !$0.isEmpty }
      .joined(separator: ". ")
  }

  private var evidenceText: String {
    hit.evidence.map(\.labelledDisplayText).joined(separator: " · ")
  }

  private var formattedTime: String? {
    guard let time = hit.time else { return nil }
    if hit.allDay {
      return time.formatted(.dateTime.year().month().day().locale(locale))
    }
    return time.formatted(.dateTime.month().day().hour().minute().locale(locale))
  }
}
