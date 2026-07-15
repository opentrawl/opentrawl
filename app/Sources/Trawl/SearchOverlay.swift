import SwiftUI
import TrawlClient
import TrawlCore

struct SearchOverlay: View {
  private let client: any TrawlClient
  let onDismiss: () -> Void
  let onTrafficChange: (ConstellationActivity, ConstellationTrafficEvent?) -> Void
  let onQueryChange: (String) -> Void
  private let sourceStatuses: [SourceStatus]

  @Binding private var scope: RestingSource?
  @State private var model: SearchModel
  @State private var interaction: SearchInteraction
  @State private var sourceResolver: SearchSourceResolver
  @State private var fieldState = SearchFieldState()
  @State private var showsRecord = false
  @State private var returnedToResults = false
  @FocusState private var focus: SearchFocus?

  init(
    client: any TrawlClient,
    scope: Binding<RestingSource?>,
    initialQuery: String = "",
    sourceStatuses: [SourceStatus] = [],
    onTrafficChange: @escaping (ConstellationActivity, ConstellationTrafficEvent?) -> Void = {
      _, _ in
    },
    onQueryChange: @escaping (String) -> Void = { _ in },
    onDismiss: @escaping () -> Void
  ) {
    self.init(
      model: SearchModel(client: client),
      client: client,
      scope: scope,
      initialQuery: initialQuery,
      sourceStatuses: sourceStatuses,
      onTrafficChange: onTrafficChange,
      onQueryChange: onQueryChange,
      onDismiss: onDismiss
    )
  }

  init(
    model: SearchModel,
    client: any TrawlClient,
    scope: Binding<RestingSource?>,
    initialQuery: String = "",
    sourceStatuses: [SourceStatus] = [],
    onTrafficChange: @escaping (ConstellationActivity, ConstellationTrafficEvent?) -> Void = {
      _, _ in
    },
    onQueryChange: @escaping (String) -> Void = { _ in },
    onDismiss: @escaping () -> Void
  ) {
    self.client = client
    self.onDismiss = onDismiss
    self.onTrafficChange = onTrafficChange
    self.onQueryChange = onQueryChange
    self.sourceStatuses = sourceStatuses
    _scope = scope
    _model = State(initialValue: model)
    let interaction = SearchInteraction(model: model, sourceID: scope.wrappedValue?.id)
    interaction.query = initialQuery
    _interaction = State(initialValue: interaction)
    _sourceResolver = State(
      initialValue: SearchSourceResolver(statuses: sourceStatuses)
    )
  }

  var body: some View {
    ZStack {
      Color(nsColor: .windowBackgroundColor)
        .accessibilityHidden(true)
      GeometryReader { proxy in
        SearchWorkspace(
          client: client,
          interaction: interaction,
          scope: scope,
          sourceResolver: sourceResolver,
          isCompact: TrawlDesign.usesCompactSearchLayout(width: proxy.size.width),
          model: model,
          fieldIdentity: fieldState.identity,
          focus: $focus,
          onClearScope: clearScope,
          onReturnToSources: onDismiss,
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
      if oldPhase == .idle, newPhase == .loading {
        fieldState.requestFocus()
      }
      if newPhase != .loading {
        interaction.reconcileCommittedResults()
        if interaction.selectedResultID == nil {
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
    .onChange(of: sourceStatuses) { _, statuses in
      sourceResolver.replace(with: statuses)
    }
    .onChange(of: scope?.id) { _, sourceID in
      interaction.changeScope(to: sourceID)
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
    .task(id: SearchKey(query: interaction.query, sourceID: interaction.sourceID)) {
      await model.search(interaction.query, source: interaction.sourceID)
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
      focus = interaction.selectedResultID == nil ? .field : .results
    case .focusField:
      focus = .field
    case .dismiss:
      onDismiss()
    }
  }

  private func focusResults() {
    guard let first = model.results.first else { return }
    if interaction.selectedResultID == nil {
      interaction.selectedResultID = first.id
    }
    focus = .results
  }

  private func openSelectedResult() {
    returnedToResults = false
    showsRecord = true
    Task { await interaction.handleReturn() }
  }

  private func open(_ hit: SearchHit) {
    interaction.selectedResultID = hit.id
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
      onTrafficChange(.searching(sourceID: interaction.sourceID), nil)
    case .complete, .partial, .skipped, .failed:
      let failedSourceIDs = Set(model.failures.map(\.sourceID))
      let requestedSourceIDs =
        interaction.sourceID.map { Set([$0]) }
        ?? Set(sourceStatuses.map(\.id))
      onTrafficChange(
        failedSourceIDs.isEmpty ? .idle : .failed(sourceIDs: failedSourceIDs),
        ConstellationTrafficEvent(
          requestedSourceIDs: requestedSourceIDs,
          usefulSourceIDs: Set(model.results.map(\.sourceID)),
          failedSourceIDs: failedSourceIDs
        )
      )
    case .idle, .timedOut:
      onTrafficChange(.idle, nil)
    }
  }
}
