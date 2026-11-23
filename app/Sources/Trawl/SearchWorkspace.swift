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
  @Bindable var interaction: SearchInteraction
  let scope: RestingTrawler?
  let availableTrawlers: [RestingTrawler]
  let trawlerResolver: SearchTrawlerResolver
  let isCompact: Bool
  let model: SearchModel
  let fieldIdentity: UUID
  @FocusState.Binding var focus: SearchFocus?
  let onSelectScope: (RestingTrawler?) -> Void
  let onClearScope: () -> Void
  let onReturnToTrawlers: () -> Void
  let onSubmit: () -> Void
  let onMoveToResults: () -> Void
  let onEscape: () -> Void
  let onOpen: (SearchMatch) -> Void
  let onReturnToResults: () -> Void
  let showsCompactRecord: Bool

  var body: some View {
    VStack(spacing: 0) {
      searchField
        .padding(14)
      switch SearchWorkspaceMode.resolve(
        phase: model.phase,
        resultCount: model.searchMatches.count)
      {
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
      availableTrawlers: availableTrawlers,
      focus: $focus,
      onSelectScope: onSelectScope,
      onClearScope: onClearScope,
      onReturnToTrawlers: onReturnToTrawlers,
      onSubmit: onSubmit,
      onMoveToResults: onMoveToResults
    )
    .id(fieldIdentity)
  }

  @ViewBuilder
  private var workspaceLayout: some View {
    switch SearchWorkspaceLayout.resolve(
      isCompact: isCompact,
      showsCompactRecord: showsCompactRecord,
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
          phase: model.openPhase,
          focus: $focus,
          onReturnToResults: onReturnToResults,
          onOpenLink: openLinkedRecord
        )
      }
    case .split:
      wideWorkspace(layout: .split) {
        Divider()
        ResultPreview(phase: model.openPhase, onOpenLink: openLinkedRecord)
      }
    }
  }

  private func openLinkedRecord(
    _ link: GloballyRoutableTrawlLink,
    anchor: RecordAnchorIdentifier
  ) {
    Task {
      await model.open(link: link, anchor: anchor)
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
      searchMatches: model.searchMatches,
      trawlerDisplayName: {
        $0.map(trawlerDisplayName(for:))
          ?? SearchTrawlerResolver.unavailableDisplayName
      },
      showsTrawlerDisplayName: scope == nil,
      committedQuery: model.committedInput?.query,
      resultLimit: model.resultLimit,
      title: model.displayTitle(for:),
      selectedSearchMatchIdentifier: $interaction.selectedSearchMatchIdentifier,
      focus: $focus,
      onReturn: onSubmit,
      onEscape: onEscape,
      onOpen: onOpen,
      onSelectionChanged: { hit in
        if !isCompact { onOpen(hit) }
      }
    )
  }

  private func trawlerDisplayName(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> String {
    if registeredTrawler == scope?.id {
      return scope?.registeredTrawlerDisplayName
        ?? SearchTrawlerResolver.unavailableDisplayName
    }
    return model.trawlerDisplayName(
      for: registeredTrawler,
      resolvedName: trawlerResolver.displayName(
        for: registeredTrawler)
    )
  }
}

private struct CompactRecordWorkspace: View {
  let phase: SearchOpenPhase
  @FocusState.Binding var focus: SearchFocus?
  let onReturnToResults: () -> Void
  let onOpenLink: (GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void

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
      ResultPreview(phase: phase, onOpenLink: onOpenLink)
    }
    .onAppear { focus = .record }
  }
}

private struct SearchField: View {
  @Binding var query: String
  let scope: RestingTrawler?
  let availableTrawlers: [RestingTrawler]
  @FocusState.Binding var focus: SearchFocus?
  let onSelectScope: (RestingTrawler?) -> Void
  let onClearScope: () -> Void
  let onReturnToTrawlers: () -> Void
  let onSubmit: () -> Void
  let onMoveToResults: () -> Void

  var body: some View {
    HStack(spacing: 9) {
      Button(action: onReturnToTrawlers) {
        Image(systemName: "chevron.left")
          .font(.body.weight(.semibold))
          .foregroundStyle(.secondary)
          .frame(width: 32, height: 32)
          .contentShape(.rect)
      }
      .buttonStyle(.plain)
      .help("Return to apps")
      .accessibilityLabel("Return to apps")
      Image(systemName: "magnifyingglass")
        .foregroundStyle(.secondary)
      TextField(
        scope.map { "Search \($0.registeredTrawlerDisplayName)" }
          ?? "Find anything in your archive",
        text: $query)
        .textFieldStyle(.plain)
        .focused($focus, equals: .field)
        .defaultFocus($focus, .field, priority: .userInitiated)
        .layoutPriority(1)
        .onSubmit(onSubmit)
        .onKeyPress(.downArrow) {
          onMoveToResults()
          return .handled
        }
      scopeMenu
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

  @ViewBuilder
  private var scopeMenu: some View {
    if !availableTrawlers.isEmpty || scope != nil {
      Menu {
        Button {
          onClearScope()
        } label: {
          if scope == nil {
            Label("All apps", systemImage: "checkmark")
          } else {
            Text("All apps")
          }
        }
        if !availableTrawlers.isEmpty {
          Divider()
          ForEach(availableTrawlers) { trawler in
            Button {
              onSelectScope(trawler)
            } label: {
              if scope?.id == trawler.id {
                Label(trawler.registeredTrawlerDisplayName, systemImage: "checkmark")
              } else {
                Text(trawler.registeredTrawlerDisplayName)
              }
            }
          }
        }
      } label: {
        HStack(spacing: 6) {
          if let scope {
            TrawlerIconView(registeredTrawler: scope.id, size: 20)
            Text(scope.registeredTrawlerDisplayName)
              .font(.callout.weight(.semibold))
              .lineLimit(1)
              .fixedSize()
          } else {
            Text("All apps")
              .font(.callout.weight(.medium))
              .foregroundStyle(.secondary)
              .lineLimit(1)
              .fixedSize()
          }
          Image(systemName: "chevron.up.chevron.down")
            .font(.caption2.weight(.semibold))
            .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .background(.secondary.opacity(0.12), in: Capsule())
      }
      .menuStyle(.borderlessButton)
      .fixedSize(horizontal: true, vertical: false)
      .help("Choose which app to search")
      .accessibilityLabel(
        scope.map { "Searching \($0.registeredTrawlerDisplayName). Change app scope." }
          ?? "Searching all apps. Change app scope.")
    }
  }

  private func clearQuery() {
    query = ""
    Task { @MainActor in
      focus = .field
    }
  }
}

private struct ScopedSearchPrompt: View {
  let scope: RestingTrawler

  var body: some View {
    ContentUnavailableView {
      Label("Search \(scope.registeredTrawlerDisplayName)", systemImage: "magnifyingglass")
    } description: {
      Text("Enter a word or phrase to search this app.")
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
    .padding()
  }
}

private struct SearchOutcome: View {
  let phase: SearchPhase
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
          OperationalCopy.Search.outcomeTitle(for: phase),
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
    OperationalCopy.Search.outcomeDetail(
      for: phase,
      isScoped: isScoped,
      timedOutLocally: timedOutLocally,
      timeoutSeconds: SearchModel.defaultWaitSeconds
    )
  }
}

struct SearchKey: Hashable {
  let query: String
  let registeredTrawler: RegisteredTrawlerIdentity?
}
