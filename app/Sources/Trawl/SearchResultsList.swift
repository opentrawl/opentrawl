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
  let committedQuery: String?
  let resultLimit: UInt32
  let title: (SearchHit) -> String
  @Binding var selectedResultID: SearchHit.ID?
  @FocusState.Binding var focus: SearchFocus?
  let onReturn: () -> Void
  let onEscape: () -> Void
  let onOpen: (SearchHit) -> Void
  let onSelectionChanged: (SearchHit) -> Void

  var body: some View {
    GeometryReader { proxy in
      ScrollView {
        LazyVStack(spacing: 0) {
          SearchResultsContext(
            phase: phase,
            resultCount: results.count,
            resultLimit: resultLimit,
            failureGuidance: failureGuidance,
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
      guard selectedResultID != nil else { return .ignored }
      onReturn()
      return .handled
    }
    .onExitCommand(perform: onEscape)
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
  let committedQuery: String?

  @ViewBuilder
  var body: some View {
    VStack(alignment: .leading, spacing: 4) {
      if let retained = SearchResultsContextCopy.retained(
        phase,
        query: committedQuery,
        failure: failureGuidance
      ) {
        Label(retained, systemImage: "magnifyingglass")
      } else if resultLimit > 0 {
        Text(SearchResultBounds.copy(resultCount: resultCount, resultLimit: resultLimit))
      }
      if case .partial = phase {
        Label(failureGuidance ?? "Some sources failed.", systemImage: "exclamationmark.triangle")
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
        if !evidenceText.isEmpty {
          Text(evidenceText)
            .font(.callout)
            .foregroundStyle(.secondary)
            .lineLimit(2)
        }
        if !hit.summary.subtitle.isEmpty {
          Text(hit.summary.subtitle)
            .font(.callout)
            .foregroundStyle(.tertiary)
            .lineLimit(2)
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
