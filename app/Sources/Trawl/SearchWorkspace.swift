import SwiftUI
import TrawlClient
import TrawlCore

enum SearchFocus: Hashable {
  case field
  case results
}

enum SearchEscapeAction: Equatable {
  case closeRecord
  case focusField
  case dismiss

  static func resolve(showsRecord: Bool, focus: SearchFocus?) -> Self {
    if showsRecord { return .closeRecord }
    return focus == .results ? .focusField : .dismiss
  }
}

enum SearchWorkspacePaneVisibility {
  static func showsRecord(for phase: SearchOpenPhase) -> Bool { phase != .idle }
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
  let onReturnToSources: () -> Void
  let onSubmit: () -> Void
  let onMoveToResults: () -> Void
  let onEscape: () -> Void
  let onOpen: (SearchHit) -> Void
  @Binding var showsRecord: Bool

  var body: some View {
    VStack(spacing: 0) {
      searchField
        .padding(14)
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
      onReturnToSources: onReturnToSources,
      onSubmit: onSubmit,
      onMoveToResults: onMoveToResults
    )
    .id(fieldIdentity)
  }

  @ViewBuilder
  private var workspaceLayout: some View {
    if isCompact {
      if showsRecord {
        VStack(spacing: 0) {
          HStack { Button("Back") { showsRecord = false }; Spacer() }.padding()
          Divider()
          ResultPreview(phase: model.openPhase, response: model.openResult)
        }
      } else { results }
    } else {
      if SearchWorkspacePaneVisibility.showsRecord(for: model.openPhase) {
        HStack(spacing: 0) {
          results.frame(minWidth: 360)
          Divider()
          ResultPreview(phase: model.openPhase, response: model.openResult)
        }
      } else {
        results
      }
    }
  }

  private var results: some View {
    SearchResultsList(
      phase: model.phase,
      results: model.results,
      sourceDisplayName: sourceDisplayName(for:),
      failureGuidance: model.failureGuidance,
      committedQuery: model.committedInput?.query,
      resultLimit: model.resultLimit,
      title: model.displayTitle(for:),
      selectedResultID: $interaction.selectedResultID,
      focus: $focus,
      onReturn: onSubmit,
      onEscape: onEscape,
      onOpen: onOpen,
      onSelectionChanged: { hit in if !isCompact { onOpen(hit) } }
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
  let onReturnToSources: () -> Void
  let onSubmit: () -> Void
  let onMoveToResults: () -> Void

  var body: some View {
    HStack(spacing: 9) {
      Button(action: onReturnToSources) {
        Image(systemName: "chevron.left")
          .font(.body.weight(.semibold))
          .foregroundStyle(.secondary)
          .frame(width: 32, height: 32)
          .contentShape(.rect)
      }
      .buttonStyle(.plain)
      .help("Return to sources")
      .accessibilityLabel("Return to sources")
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
            .scaleEffect(1.22)
            .frame(width: 36, height: 36)
            .clipShape(.rect(cornerRadius: 8))
          Text(scope.surface)
            .font(.callout.weight(.semibold))
            .lineLimit(1)
            .fixedSize()
          Button(action: onClearScope) {
            Image(systemName: "square.grid.2x2.fill")
              .font(.caption.weight(.semibold))
              .foregroundStyle(.secondary)
              .frame(width: 20, height: 20)
              .contentShape(.circle)
          }
          .buttonStyle(.plain)
          .help("Search all sources")
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
            .font(.body)
            .foregroundStyle(.secondary)
            .frame(width: 20, height: 20)
            .contentShape(.circle)
        }
        .buttonStyle(.plain)
        .help("Clear search query")
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
    Group {
      switch phase {
      case .loading:
        VStack(spacing: 9) {
          ProgressView()
            .controlSize(.small)
          Text("Searching. Stops after \(SearchModel.defaultWaitSeconds) seconds.")
        }
        .font(.callout)
        .foregroundStyle(.secondary)
      default:
        ContentUnavailableView(
          SearchWorkspaceCopy.outcomeTitle(for: phase),
          systemImage: SearchWorkspaceCopy.outcomeSymbol(for: phase),
          description: Text(detail)
        )
      }
    }
    .multilineTextAlignment(.center)
    .frame(maxWidth: .infinity, maxHeight: .infinity)
    .padding()
  }

  private var detail: String {
    SearchWorkspaceCopy.outcomeDetail(
      for: phase,
      failureGuidance: failureGuidance,
      skippedSources: skippedSources,
      isScoped: isScoped,
      timeoutSeconds: SearchModel.defaultWaitSeconds
    )
  }
}

struct SearchKey: Hashable {
  let query: String
  let sourceID: String?
}
