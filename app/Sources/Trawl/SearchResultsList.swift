import SwiftUI
import TrawlClient
import TrawlCore

struct SearchResultsList: View {
  let phase: SearchPhase
  let results: [SearchHit]
  let sourceDisplayNames: [String: String]
  let failureGuidance: String?
  @Binding var selectedResultID: SearchHit.ID?
  @FocusState.Binding var focus: SearchFocus?

  var body: some View {
    Group {
      if results.isEmpty {
        SearchStatePlaceholder(phase: phase, failureGuidance: failureGuidance)
      } else {
        List(results, selection: $selectedResultID) { hit in
          SearchResultRow(
            hit: hit,
            sourceDisplayName: sourceDisplayNames[hit.sourceID] ?? "Unknown source"
          )
            .tag(hit.id)
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .focused($focus, equals: .results)
        .tint(TrawlDesign.brandRed)
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
    .contentShape(.rect)
    .accessibilityElement(children: .combine)
    .accessibilityLabel("\(sourceDisplayName), \(hit.title.isEmpty ? "Untitled result" : hit.title)")
  }
}
