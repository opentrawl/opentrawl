import SwiftUI
import TrawlClient
import TrawlCore

enum SearchFocus: Hashable {
  case field
  case results
  case record
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

enum SearchWorkspaceLayout: Equatable {
  case results
  case compactRecord
  case split

  static func resolve(
    isCompact: Bool,
    showsCompactRecord: Bool,
    openPhase: SearchOpenPhase
  ) -> Self {
    if isCompact {
      return showsCompactRecord ? .compactRecord : .results
    }
    return SearchWorkspacePaneVisibility.showsRecord(for: openPhase) ? .split : .results
  }
}

enum SearchWorkspaceGeometry {
  static let wideResultsWidth = TrawlDesign.searchResultsMaximumWidth

  struct WideLayout: Equatable {
    let resultsOriginX: CGFloat
    let reservesRecordRegion: Bool
  }

  static func layout(for workspace: SearchWorkspaceLayout) -> WideLayout {
    switch workspace {
    case .results, .split:
      WideLayout(resultsOriginX: 0, reservesRecordRegion: true)
    case .compactRecord:
      WideLayout(resultsOriginX: 0, reservesRecordRegion: false)
    }
  }
}

enum SearchWorkspaceFieldContent: Equatable {
  case none
  case scopedPrompt

  static func resolve(isScoped: Bool) -> Self {
    isScoped ? .scopedPrompt : .none
  }
}

struct SearchWorkspace: View {
  let client: any TrawlClient
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
  let onReturnToResults: () -> Void
  @Binding var showsRecord: Bool

  var body: some View {
    VStack(spacing: 0) {
      searchField
        .padding(14)
      switch SearchWorkspaceMode.resolve(phase: model.phase, resultCount: model.results.count) {
      case .field:
        if SearchWorkspaceFieldContent.resolve(isScoped: scope != nil) == .scopedPrompt,
          let scope
        {
          Divider()
          ScopedSearchPrompt(scope: scope)
        }
      case .outcome:
        Divider()
        SearchOutcome(
          phase: model.phase,
          failureGuidance: model.failureGuidance,
          skippedSources: model.skippedSources,
          isScoped: scope != nil,
          timedOutLocally: model.timedOutLocally
        )
      case .results:
        Divider()
        workspaceLayout
      }
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
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
    switch SearchWorkspaceLayout.resolve(
      isCompact: isCompact,
      showsCompactRecord: showsRecord,
      openPhase: model.openPhase
    ) {
    case .results:
      if isCompact {
        results.frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
      } else {
        wideWorkspace(layout: .results) {
          Spacer(minLength: TrawlDesign.searchRecordMinimumWidth)
        }
      }
    case .compactRecord:
      ZStack {
        results
          .opacity(0)
          .allowsHitTesting(false)
          .accessibilityHidden(true)
        CompactRecordWorkspace(
          client: client,
          phase: model.openPhase,
          response: model.openResult,
          focus: $focus,
          onReturnToResults: onReturnToResults
        )
      }
    case .split:
      wideWorkspace(layout: .split) {
        Divider()
        ResultPreview(client: client, phase: model.openPhase, response: model.openResult)
      }
    }
  }

  private func wideWorkspace<Preview: View>(
    layout: SearchWorkspaceLayout,
    @ViewBuilder preview: () -> Preview
  ) -> some View {
    let geometry = SearchWorkspaceGeometry.layout(for: layout)
    return HStack(spacing: 0) {
      results.frame(
        minWidth: SearchWorkspaceGeometry.wideResultsWidth,
        idealWidth: SearchWorkspaceGeometry.wideResultsWidth,
        maxWidth: SearchWorkspaceGeometry.wideResultsWidth,
        maxHeight: .infinity
      )
      preview()
    }
    .padding(.leading, geometry.resultsOriginX)
    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
  }

  private var results: some View {
    SearchResultsList(
      phase: model.phase,
      results: model.results,
      sourceDisplayName: sourceDisplayName(for:),
      showsSourceDisplayName: scope == nil,
      failureGuidance: model.failureGuidance,
      committedQuery: model.committedInput?.query,
      resultLimit: model.resultLimit,
      title: model.displayTitle(for:),
      selectedResultID: $interaction.selectedResultID,
      focus: $focus,
      onReturn: onSubmit,
      onEscape: onEscape,
      onOpen: onOpen,
      onSelectionChanged: { hit in
        if !isCompact { onOpen(hit) }
      }
    )
  }

  private func sourceDisplayName(for sourceID: String) -> String {
    if sourceID == scope?.id {
      return scope?.surface ?? SearchSourceResolver.unavailableDisplayName
    }
    return model.sourceDisplayName(
      for: sourceID,
      resolvedName: sourceResolver.displayName(for: sourceID)
    )
  }
}

private struct CompactRecordWorkspace: View {
  let client: any TrawlClient
  let phase: SearchOpenPhase
  let response: OpenResponse?
  @FocusState.Binding var focus: SearchFocus?
  let onReturnToResults: () -> Void

  var body: some View {
    VStack(spacing: 0) {
      HStack {
        Button(action: onReturnToResults) {
          Label("Results", systemImage: "chevron.left")
        }
        .buttonStyle(.borderless)
        .accessibilityLabel("Back to results")
        .focused($focus, equals: .record)
        Spacer()
      }
      .padding(.horizontal, 14)
      .padding(.vertical, 9)
      Divider()
      ResultPreview(client: client, phase: phase, response: response)
    }
    .onAppear { focus = .record }
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
            Text("All sources")
              .font(.caption.weight(.semibold))
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
      Group {
        if query.isEmpty {
          Color.clear
            .accessibilityHidden(true)
        } else {
          Button(action: clearQuery) {
            Image(systemName: "xmark.circle.fill")
              .font(.body)
              .foregroundStyle(.secondary)
              .contentShape(.circle)
          }
          .buttonStyle(.plain)
          .help("Clear search query")
          .accessibilityLabel("Clear search query")
        }
      }
      .frame(width: 20, height: 20)
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

private struct ScopedSearchPrompt: View {
  let scope: RestingSource

  var body: some View {
    ContentUnavailableView {
      Label("Search \(scope.surface)", systemImage: "magnifyingglass")
    } description: {
      Text("Enter a word or phrase to search this source.")
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
    .padding()
  }
}

private struct SearchOutcome: View {
  let phase: SearchPhase
  let failureGuidance: String?
  let skippedSources: [SkippedSource]
  let isScoped: Bool
  let timedOutLocally: Bool

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
      timedOutLocally: timedOutLocally,
      timeoutSeconds: SearchModel.defaultWaitSeconds
    )
  }
}

struct SearchKey: Hashable {
  let query: String
  let sourceID: String?
}
