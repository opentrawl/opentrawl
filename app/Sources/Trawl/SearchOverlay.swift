import SwiftUI
import TrawlClient
import TrawlCore

struct SearchOverlay: View {
  let onDismiss: () -> Void
  let onTrafficChange: (ConstellationActivity, ConstellationTrafficEvent?) -> Void
  let onQueryChange: (String) -> Void
  private let sourceStatuses: [SourceStatus]

  @State private var scope: RestingSource?
  @State private var model: SearchModel
  @State private var interaction: SearchInteraction
  @State private var sourceResolver: SearchSourceResolver
  @State private var fieldState = SearchFieldState()
  @State private var showsRecord = false
  @FocusState private var focus: SearchFocus?

  init(
    client: any TrawlClient,
    initialScope: RestingSource?,
    initialQuery: String = "",
    sourceStatuses: [SourceStatus] = [],
    onTrafficChange: @escaping (ConstellationActivity, ConstellationTrafficEvent?) -> Void = { _, _ in },
    onQueryChange: @escaping (String) -> Void = { _ in },
    onDismiss: @escaping () -> Void
  ) {
    self.init(
      model: SearchModel(client: client),
      initialScope: initialScope,
      initialQuery: initialQuery,
      sourceStatuses: sourceStatuses,
      onTrafficChange: onTrafficChange,
      onQueryChange: onQueryChange,
      onDismiss: onDismiss
    )
  }

  init(
    model: SearchModel,
    initialScope: RestingSource?,
    initialQuery: String = "",
    sourceStatuses: [SourceStatus] = [],
    onTrafficChange: @escaping (ConstellationActivity, ConstellationTrafficEvent?) -> Void = { _, _ in },
    onQueryChange: @escaping (String) -> Void = { _ in },
    onDismiss: @escaping () -> Void
  ) {
    self.onDismiss = onDismiss
    self.onTrafficChange = onTrafficChange
    self.onQueryChange = onQueryChange
    self.sourceStatuses = sourceStatuses
    _scope = State(initialValue: initialScope)
    _model = State(initialValue: model)
    let interaction = SearchInteraction(model: model, sourceID: initialScope?.id)
    interaction.query = initialQuery
    _interaction = State(initialValue: interaction)
    _sourceResolver = State(
      initialValue: SearchSourceResolver(statuses: sourceStatuses)
    )
  }

  var body: some View {
    ZStack {
      Button(action: onDismiss) {
        Color(nsColor: .windowBackgroundColor).opacity(0.97)
      }
      .buttonStyle(.plain)
      .accessibilityLabel("Return to sources")
      GeometryReader { proxy in
        SearchWorkspace(
          interaction: interaction,
          scope: scope,
          sourceResolver: sourceResolver,
          isCompact: proxy.size.width < 760,
          model: model,
          fieldIdentity: fieldState.identity,
          focus: $focus,
          onClearScope: clearScope,
          onSubmit: openSelectedResult,
          onMoveToResults: focusResults,
          onEscape: handleEscape,
          onOpen: open,
          showsRecord: $showsRecord
        )
        .frame(maxWidth: 1_180, maxHeight: .infinity)
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
        if interaction.selectedResultID == nil { showsRecord = false }
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
    .onChange(of: interaction.query) { _, query in
      onQueryChange(query)
    }
    .onKeyPress(.escape) {
      handleEscape()
      return .handled
    }
    .onExitCommand(perform: handleEscape)
    .onAppear {
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
    interaction.changeScope(to: nil)
    scope = nil
  }

  private func handleEscape() {
    switch SearchEscapeAction.resolve(
      showsRecord: showsRecord || model.openPhase != .idle,
      focus: focus
    ) {
    case .closeRecord:
      model.clearOpenResult()
      showsRecord = false
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
    showsRecord = true
    Task { await interaction.handleReturn() }
  }

  private func open(_ hit: SearchHit) {
    interaction.selectedResultID = hit.id
    showsRecord = true
    Task { await interaction.handleReturn() }
  }

  private func reportActivity() {
    switch model.phase {
    case .loading:
      onTrafficChange(.searching(sourceID: interaction.sourceID), nil)
    case .complete, .partial, .skipped, .failed:
      let failedSourceIDs = Set(model.failures.map(\.sourceID))
      let requestedSourceIDs = interaction.sourceID.map { Set([$0]) }
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
