import SwiftUI
import TrawlClient
import TrawlCore

enum SearchFocus: Hashable {
  case field
  case results
}

struct SearchWorkspace: View {
  @Bindable var interaction: SearchInteraction
  let scope: SourceStatus?
  let sourceDisplayNames: [String: String]
  let isCompact: Bool
  let model: SearchModel
  @FocusState.Binding var focus: SearchFocus?
  let onClearScope: () -> Void
  let onSubmit: () -> Void
  let onMoveToResults: () -> Void
  let onDismiss: () -> Void

  var body: some View {
    VStack(spacing: 0) {
      SearchField(
        query: $interaction.query,
        scope: scope,
        focus: $focus,
        onClearScope: onClearScope,
        onSubmit: onSubmit,
        onMoveToResults: onMoveToResults,
        onDismiss: onDismiss
      )
      .padding(14)

      Divider()
      workspaceLayout
      Divider()
      SearchStatus(
        phase: model.phase,
        count: model.results.count,
        scopeName: scope?.name,
        resultLimit: model.resultLimit,
        failureGuidance: model.failureGuidance
      )
      .padding(.horizontal, 16)
      .frame(minHeight: 48)
    }
  }

  @ViewBuilder
  private var workspaceLayout: some View {
    if isCompact {
      VStack(spacing: 0) {
        SearchResultsList(
          phase: model.phase,
          results: model.results,
          sourceDisplayNames: sourceDisplayNames,
          failureGuidance: model.failureGuidance,
          selectedResultID: $interaction.selectedResultID,
          focus: $focus
        )
        .frame(height: 188)
        Divider()
        ResultPreview(
          hit: selectedHit,
          sourceDisplayName: selectedSourceDisplayName,
          phase: model.openPhase,
          response: model.openResult
        )
      }
    } else {
      HStack(spacing: 0) {
        SearchResultsList(
          phase: model.phase,
          results: model.results,
          sourceDisplayNames: sourceDisplayNames,
          failureGuidance: model.failureGuidance,
          selectedResultID: $interaction.selectedResultID,
          focus: $focus
        )
        .frame(width: 306)
        Divider()
        ResultPreview(
          hit: selectedHit,
          sourceDisplayName: selectedSourceDisplayName,
          phase: model.openPhase,
          response: model.openResult
        )
      }
    }
  }

  private var selectedHit: SearchHit? {
    model.results.first(where: { $0.id == interaction.selectedResultID })
  }

  private var selectedSourceDisplayName: String {
    guard let selectedHit else { return "Unknown source" }
    return sourceDisplayNames[selectedHit.sourceID] ?? "Unknown source"
  }
}

private struct SearchField: View {
  @Binding var query: String
  let scope: SourceStatus?
  @FocusState.Binding var focus: SearchFocus?
  let onClearScope: () -> Void
  let onSubmit: () -> Void
  let onMoveToResults: () -> Void
  let onDismiss: () -> Void

  var body: some View {
    HStack(spacing: 9) {
      Image(systemName: "magnifyingglass")
        .foregroundStyle(.secondary)
      if let scope {
        HStack(spacing: 5) {
          SourceIconView(sourceID: scope.id, size: 18)
          Text(scope.name)
            .font(.caption.weight(.medium))
            .lineLimit(1)
          Button {
            onClearScope()
          } label: {
            Image(systemName: "xmark")
          }
          .buttonStyle(.plain)
          .accessibilityLabel("Search every source")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 5)
        .background(.secondary.opacity(0.1), in: Capsule())
      }
      TextField(scope == nil ? "Search everything" : "Search this source", text: $query)
        .textFieldStyle(.plain)
        .focused($focus, equals: .field)
        .defaultFocus($focus, .field, priority: .userInitiated)
        .onSubmit(onSubmit)
        .onKeyPress(.downArrow) {
          onMoveToResults()
          return .handled
        }
      Button(action: onDismiss) {
        Image(systemName: "xmark.circle.fill")
          .foregroundStyle(.secondary)
      }
      .buttonStyle(.plain)
      .accessibilityLabel("Close search")
    }
    .padding(.horizontal, 13)
    .frame(height: 44)
    .background(.secondary.opacity(0.08), in: Capsule())
  }
}

private struct SearchStatus: View {
  let phase: SearchPhase
  let count: Int
  let scopeName: String?
  let resultLimit: UInt32
  let failureGuidance: String?

  var body: some View {
    ViewThatFits(in: .horizontal) {
      HStack(alignment: .firstTextBaseline, spacing: 12) {
        status
        Spacer(minLength: 8)
        context
      }
      VStack(alignment: .leading, spacing: 3) {
        status
        context
      }
    }
    .font(.callout)
    .foregroundStyle(.secondary)
    .frame(maxWidth: .infinity, alignment: .leading)
  }

  @ViewBuilder
  private var status: some View {
    switch phase {
    case .idle:
      Text("Ready to search.")
    case .loading:
      HStack(spacing: 7) {
        ProgressView()
          .controlSize(.small)
        Text("Searching. Stops after \(SearchModel.defaultWaitSeconds) seconds.")
      }
    case .complete where count == 0:
      Text("No matches.")
    case .complete:
      Text("\(count) results.")
    case .partial where count == 0:
      Label(
        failureGuidance ?? "Some sources failed; the others returned no matches.",
        systemImage: "exclamationmark.triangle"
      )
    case .partial:
      Label(
        partialMessage,
        systemImage: "exclamationmark.triangle"
      )
    case .failed(let message):
      Label(message, systemImage: "exclamationmark.circle")
    case .timedOut:
      Label(
        "Search stopped after \(SearchModel.defaultWaitSeconds) seconds.",
        systemImage: "clock.badge.exclamationmark"
      )
    }
  }

  private var partialMessage: String {
    let result = "Showing \(count) useful results."
    guard let failureGuidance else { return "Some sources failed. \(result)" }
    return "\(result) \(failureGuidance)"
  }

  private var context: some View {
    HStack(spacing: 10) {
      Label(scopeName ?? "All sources", systemImage: scopeName == nil ? "square.grid.2x2" : "scope")
        .lineLimit(1)
      Text("Up to \(resultLimit == 0 ? SearchResponse.maximumResults : resultLimit)")
        .fixedSize()
    }
  }
}

struct SearchKey: Hashable {
  let query: String
  let sourceID: String?
}
