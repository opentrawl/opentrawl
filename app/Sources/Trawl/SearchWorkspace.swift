import SwiftUI
import TrawlClient
import TrawlCore

enum SearchFocus: Hashable {
  case field
  case results
}

struct SearchWorkspace: View {
  @Bindable var interaction: SearchInteraction
  let scope: RestingSource?
  let sourceResolver: SearchSourceResolver
  let isCompact: Bool
  let model: SearchModel
  let fieldIdentity: UUID
  @FocusState.Binding var focus: SearchFocus?
  let onClearScope: () -> Void
  let onSubmit: () -> Void
  let onMoveToResults: () -> Void
  let onOpen: (SearchHit) -> Void

  var body: some View {
    VStack(spacing: 0) {
      searchField
      switch SearchWorkspaceMode.resolve(phase: model.phase, resultCount: model.results.count) {
      case .field:
        EmptyView()
      case .outcome:
        Divider()
        SearchOutcome(
          phase: model.phase,
          failureGuidance: model.failureGuidance,
          skippedSources: model.skippedSources,
          isScoped: scope != nil
        )
      case .results:
        Divider()
        workspaceLayout
      }
    }
  }

  private var searchField: some View {
    SearchField(
      query: $interaction.query,
      scope: scope,
      focus: $focus,
      onClearScope: onClearScope,
      onSubmit: onSubmit,
      onMoveToResults: onMoveToResults
    )
    .padding(14)
    .id(fieldIdentity)
  }

  @ViewBuilder
  private var workspaceLayout: some View {
    if isCompact {
      VStack(spacing: 0) {
        results
          .frame(height: 188)
        Divider()
        ResultPreview(phase: model.openPhase, response: model.openResult)
      }
    } else {
      HStack(spacing: 0) {
        results
          .frame(minWidth: 360)
        Divider()
        ResultPreview(phase: model.openPhase, response: model.openResult)
      }
    }
  }

  private var results: some View {
    SearchResultsList(
      phase: model.phase,
      results: model.results,
      sourceDisplayName: sourceDisplayName(for:),
      failureGuidance: model.failureGuidance,
      hasTimeoutFailure: model.hasTimeoutFailure,
      resultLimit: model.resultLimit,
      title: model.displayTitle(for:),
      selectedResultID: $interaction.selectedResultID,
      focus: $focus,
      onReturn: onSubmit,
      onOpen: onOpen
    )
  }

  private func sourceDisplayName(for sourceID: String) -> String {
    if sourceID == scope?.id { return scope?.surface ?? SearchSourceResolver.unavailableDisplayName }
    return model.sourceDisplayName(
      for: sourceID,
      resolvedName: sourceResolver.displayName(for: sourceID)
    )
  }
}

private struct SearchField: View {
  @Binding var query: String
  let scope: RestingSource?
  @FocusState.Binding var focus: SearchFocus?
  let onClearScope: () -> Void
  let onSubmit: () -> Void
  let onMoveToResults: () -> Void

  var body: some View {
    HStack(spacing: 9) {
      Image(systemName: "magnifyingglass")
        .foregroundStyle(.secondary)
      TextField(scope.map { "Search \($0.surface)" } ?? "Search everything", text: $query)
        .textFieldStyle(.plain)
        .focused($focus, equals: .field)
        .defaultFocus($focus, .field, priority: .userInitiated)
        .layoutPriority(1)
        .onSubmit(onSubmit)
        .onKeyPress(.downArrow) {
          onMoveToResults()
          return .handled
        }
      if let scope {
        HStack(spacing: 8) {
          SourceIconView(sourceID: scope.id, size: 36)
          Text(scope.surface)
            .font(.callout.weight(.semibold))
            .lineLimit(1)
            .fixedSize()
          Button(action: onClearScope) {
            Image(systemName: "xmark")
              .font(.caption.weight(.bold))
              .foregroundStyle(.secondary)
              .frame(width: 18, height: 18)
              .contentShape(.circle)
          }
          .buttonStyle(.plain)
          .accessibilityLabel("Search all sources")
        }
        .padding(.leading, 8)
        .padding(.trailing, 7)
        .padding(.vertical, 2)
        .background(.secondary.opacity(0.14), in: Capsule())
        .fixedSize(horizontal: true, vertical: false)
      }
      if !query.isEmpty {
        Button(action: clearQuery) {
          Image(systemName: "xmark.circle.fill")
            .foregroundStyle(.secondary)
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Clear search query")
      }
    }
    .padding(.horizontal, 13)
    .frame(height: 44)
    .background(.secondary.opacity(0.08), in: Capsule())
  }

  private func clearQuery() {
    query = ""
    Task { @MainActor in
      focus = .field
    }
  }
}

private struct SearchOutcome: View {
  let phase: SearchPhase
  let failureGuidance: String?
  let skippedSources: [SkippedSource]
  let isScoped: Bool

  var body: some View {
    VStack(spacing: 9) {
      switch phase {
      case .loading:
        ProgressView()
          .controlSize(.small)
        Text("Searching. Stops after \(SearchModel.defaultWaitSeconds) seconds.")
      case .complete:
        Label("No matches.", systemImage: "magnifyingglass")
      case .partial:
        Label(
          SearchWorkspaceCopy.partialNoMatches(
            failureGuidance: failureGuidance,
            isScoped: isScoped
          ),
          systemImage: "exclamationmark.triangle"
        )
      case .skipped:
        Label(SearchWorkspaceCopy.skippedOutcome(for: skippedSources), systemImage: "exclamationmark.triangle")
      case .failed(let message):
        Label(message, systemImage: "exclamationmark.circle")
      case .timedOut:
        Label(
          "Search stopped after \(SearchModel.defaultWaitSeconds) seconds.",
          systemImage: "clock.badge.exclamationmark"
        )
      case .idle:
        EmptyView()
      }
    }
    .font(.callout)
    .foregroundStyle(.secondary)
    .multilineTextAlignment(.center)
    .frame(maxWidth: .infinity, maxHeight: .infinity)
    .padding()
  }

}

struct SearchKey: Hashable {
  let query: String
  let sourceID: String?
}
