import SwiftUI
import TrawlClient
import TrawlCore

struct SearchOverlay: View {
  let onDismiss: () -> Void
  let onTrafficChange: (ConstellationActivity, ConstellationTrafficEvent?) -> Void
  let onQueryChange: (String) -> Void
  private let trawlerStatuses: [TrawlerStatus]

  @Binding private var scope: RestingTrawler?
  @State private var model: SearchModel
  @State private var interaction: SearchInteraction
  @State private var trawlerResolver: SearchTrawlerResolver
  @State private var fieldState = SearchFieldState()
  @State private var showsRecord = false
  @State private var returnedToResults = false
  @FocusState private var focus: SearchFocus?

  init(
    client: any TrawlClient,
    scope: Binding<RestingTrawler?>,
    initialQuery: String = "",
    trawlerStatuses: [TrawlerStatus] = [],
    onTrafficChange: @escaping (ConstellationActivity, ConstellationTrafficEvent?) -> Void = {
      _, _ in
    },
    onQueryChange: @escaping (String) -> Void = { _ in },
    onDismiss: @escaping () -> Void
  ) {
    self.init(
      model: SearchModel(client: client),
      scope: scope,
      initialQuery: initialQuery,
      trawlerStatuses: trawlerStatuses,
      onTrafficChange: onTrafficChange,
      onQueryChange: onQueryChange,
      onDismiss: onDismiss
    )
  }

  init(
    model: SearchModel,
    scope: Binding<RestingTrawler?>,
    initialQuery: String = "",
    trawlerStatuses: [TrawlerStatus] = [],
    onTrafficChange: @escaping (ConstellationActivity, ConstellationTrafficEvent?) -> Void = {
      _, _ in
    },
    onQueryChange: @escaping (String) -> Void = { _ in },
    onDismiss: @escaping () -> Void
  ) {
    self.onDismiss = onDismiss
    self.onTrafficChange = onTrafficChange
    self.onQueryChange = onQueryChange
    self.trawlerStatuses = trawlerStatuses
    _scope = scope
    _model = State(initialValue: model)
    let interaction = SearchInteraction(
      model: model,
      registeredTrawler: scope.wrappedValue?.id)
    interaction.query = initialQuery
    _interaction = State(initialValue: interaction)
    _trawlerResolver = State(
      initialValue: SearchTrawlerResolver(statuses: trawlerStatuses)
    )
  }

  var body: some View {
    ZStack {
      Color(nsColor: .windowBackgroundColor)
        .accessibilityHidden(true)
      GeometryReader { proxy in
        SearchWorkspace(
          interaction: interaction,
          scope: scope,
          trawlerResolver: trawlerResolver,
          isCompact: TrawlDesign.usesCompactSearchLayout(width: proxy.size.width),
          model: model,
          fieldIdentity: fieldState.identity,
          focus: $focus,
          onClearScope: clearScope,
          onReturnToTrawlers: onDismiss,
          onSubmit: openSelectedResult,
          onMoveToResults: focusResults,
          onEscape: handleEscape,
          onOpen: open,
          onReturnToResults: returnToResults,
          showsRecord: $showsRecord
        )
        .frame(maxWidth: TrawlDesign.searchWorkspaceMaximumWidth, maxHeight: .infinity)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(TrawlDesign.contentInset)
      }
    }
    .onChange(of: model.phase) { oldPhase, newPhase in
      if case .idle = oldPhase, case .loading = newPhase {
        fieldState.requestFocus()
      }
      if newPhase != .loading {
        interaction.reconcileCommittedResults()
        if interaction.selectedSearchMatchIdentifier == nil {
          showsRecord = false
          returnedToResults = false
        }
      }
      reportActivity()
    }
    .onChange(of: fieldState.focusRequest) { _, _ in
      Task { @MainActor in
        focus = .field
      }
    }
    .onChange(of: trawlerStatuses) { _, statuses in
      trawlerResolver.replace(with: statuses)
    }
    .onChange(of: scope?.id) { _, registeredTrawler in
      interaction.changeScope(to: registeredTrawler)
    }
    .onChange(of: interaction.query) { _, query in
      onQueryChange(query)
    }
    .onKeyPress(.escape) {
      handleEscape()
      return .handled
    }
    .onExitCommand(perform: handleEscape)
    .onAppear {
      if model.openPhase != .idle {
        showsRecord = true
      }
      Task { @MainActor in
        focus = .field
      }
    }
    .task(
      id: SearchKey(
        query: interaction.query,
        registeredTrawler: interaction.registeredTrawler)
    ) {
      await model.search(
        interaction.query,
        registeredTrawler: interaction.registeredTrawler)
    }
    .onDisappear {
      onTrafficChange(.idle, nil)
    }
  }

  private func clearScope() {
    scope = nil
  }

  private func handleEscape() {
    switch SearchEscapeAction.resolve(
      showsRecord: showsRecord || (model.openPhase != .idle && !returnedToResults),
      focus: focus
    ) {
    case .closeRecord:
      model.clearOpenResult()
      showsRecord = false
      returnedToResults = false
      focus = interaction.selectedSearchMatchIdentifier == nil ? .field : .results
    case .focusField:
      focus = .field
    case .dismiss:
      onDismiss()
    }
  }

  private func focusResults() {
    guard let first = model.searchMatches.first else { return }
    if interaction.selectedSearchMatchIdentifier == nil {
      interaction.selectedSearchMatchIdentifier = first.id
    }
    focus = .results
  }

  private func openSelectedResult() {
    returnedToResults = false
    showsRecord = true
    Task { await interaction.handleReturn() }
  }

  private func open(_ searchMatch: SearchMatch) {
    interaction.selectedSearchMatchIdentifier = searchMatch.id
    returnedToResults = false
    showsRecord = true
    Task { await interaction.handleReturn() }
  }

  private func returnToResults() {
    showsRecord = false
    returnedToResults = true
    focus = .results
  }

  private func reportActivity() {
    switch model.phase {
    case .loading:
      onTrafficChange(
        .searching(registeredTrawler: interaction.registeredTrawler),
        nil)
    case .complete, .partial, .skipped, .failed:
      let failedRegisteredTrawlers = Set(model.operationFailures.map(\.failedTrawler))
      let requestedRegisteredTrawlers =
        interaction.registeredTrawler.map {
          Set([$0])
        }
        ?? Set(trawlerStatuses.map(\.id))
      onTrafficChange(
        failedRegisteredTrawlers.isEmpty
          ? .idle
          : .failed(registeredTrawlers: failedRegisteredTrawlers),
        ConstellationTrafficEvent(
          requestedRegisteredTrawlers: requestedRegisteredTrawlers,
          usefulRegisteredTrawlers: Set(
            model.searchMatches.compactMap {
              $0.registeredTrawler
            }),
          failedRegisteredTrawlers: failedRegisteredTrawlers
        )
      )
    case .idle, .timedOut:
      onTrafficChange(.idle, nil)
    }
  }
}
