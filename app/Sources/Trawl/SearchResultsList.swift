import SwiftUI
import TrawlClient
import TrawlCore

struct SearchResultsList: View {
  let phase: SearchPhase
  let results: [SearchHit]
  let sourceResolver: SearchSourceResolver
  let failureGuidance: String?
  @Binding var selectedResultID: SearchHit.ID?
  @FocusState.Binding var focus: SearchFocus?

  var body: some View {
    Group {
      if results.isEmpty {
        SearchStatePlaceholder(phase: phase, failureGuidance: failureGuidance)
      } else {
        ScrollView {
          LazyVStack(spacing: 0) {
            ForEach(results) { hit in
              Button {
                selectedResultID = hit.id
              } label: {
                SearchResultRow(
                  hit: hit,
                  sourceDisplayName: sourceResolver.displayNameOrUnavailable(for: hit.sourceID),
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
        .overlay {
          RoundedRectangle(cornerRadius: 9)
            .stroke(
              focus == .results ? TrawlDesign.brandRed.opacity(0.45) : .clear,
              lineWidth: 1
            )
            .padding(5)
            .allowsHitTesting(false)
        }
      }
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
  }

  private func moveSelection(by offset: Int) {
    guard !results.isEmpty else { return }
    let currentIndex = selectedResultID.flatMap { selectedID in
      results.firstIndex(where: { $0.id == selectedID })
    } ?? (offset > 0 ? -1 : results.count)
    let nextIndex = min(max(currentIndex + offset, 0), results.count - 1)
    selectedResultID = results[nextIndex].id
  }
}

private struct SearchStatePlaceholder: View {
  let phase: SearchPhase
  let failureGuidance: String?

  var body: some View {
    VStack(spacing: 9) {
      if case .loading = phase {
        ProgressView()
          .controlSize(.small)
      } else {
        Image(systemName: symbol)
      }
      Text(title)
      if shouldShowFailure, let failureGuidance {
        Text(failureGuidance)
          .font(.caption)
      }
    }
    .font(.callout)
    .foregroundStyle(.secondary)
    .multilineTextAlignment(.center)
    .padding()
  }

  private var shouldShowFailure: Bool {
    switch phase {
    case .partial, .failed: true
    case .idle, .loading, .complete, .timedOut: false
    }
  }

  private var title: LocalizedStringResource {
    switch phase {
    case .idle: "Search your sources"
    case .loading: "Searching"
    case .complete: "No matches"
    case .partial: "No matches from available sources"
    case .failed: "Search failed"
    case .timedOut: "Search timed out"
    }
  }

  private var symbol: String {
    switch phase {
    case .idle, .complete, .loading: "magnifyingglass"
    case .partial: "exclamationmark.triangle"
    case .failed: "exclamationmark.circle"
    case .timedOut: "clock.badge.exclamationmark"
    }
  }
}

private struct SearchResultRow: View {
  let hit: SearchHit
  let sourceDisplayName: String
  let isSelected: Bool

  var body: some View {
    HStack(alignment: .top, spacing: 10) {
      SourceIconView(sourceID: hit.sourceID, size: 24)
      VStack(alignment: .leading, spacing: 3) {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
          Text(hit.title.isEmpty ? "Untitled result" : hit.title)
            .font(.body.weight(.semibold))
            .lineLimit(1)
          Spacer(minLength: 4)
          Text(hit.whenDisplay)
            .font(.caption)
            .foregroundStyle(.tertiary)
        }
        Text(hit.snippet)
          .font(.callout)
          .foregroundStyle(.secondary)
          .lineLimit(1)
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
    .accessibilityLabel("\(sourceDisplayName), \(hit.title.isEmpty ? "Untitled result" : hit.title)")
  }
}
