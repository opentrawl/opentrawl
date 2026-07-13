import SwiftUI
import TrawlClient
import TrawlCore

struct SearchOverlay: View {
  let onDismiss: () -> Void
  let onTrafficChange: (ConstellationActivity, ConstellationTrafficEvent?) -> Void
  private let sourceStatuses: [SourceStatus]

  @State private var scope: RestingSource?
  @State private var model: SearchModel
  @State private var interaction: SearchInteraction
  @State private var sourceResolver: SearchSourceResolver
  @State private var fieldState = SearchFieldState()
  @FocusState private var focus: SearchFocus?

  init(
    client: any TrawlClient,
    initialScope: RestingSource?,
    sourceStatuses: [SourceStatus] = [],
    onTrafficChange: @escaping (ConstellationActivity, ConstellationTrafficEvent?) -> Void = { _, _ in },
    onDismiss: @escaping () -> Void
  ) {
    self.init(
      model: SearchModel(client: client),
      initialScope: initialScope,
      sourceStatuses: sourceStatuses,
      onTrafficChange: onTrafficChange,
      onDismiss: onDismiss
    )
  }

  init(
    model: SearchModel,
    initialScope: RestingSource?,
    sourceStatuses: [SourceStatus] = [],
    onTrafficChange: @escaping (ConstellationActivity, ConstellationTrafficEvent?) -> Void = { _, _ in },
    onDismiss: @escaping () -> Void
  ) {
    self.onDismiss = onDismiss
    self.onTrafficChange = onTrafficChange
    self.sourceStatuses = sourceStatuses
    _scope = State(initialValue: initialScope)
    _model = State(initialValue: model)
    _interaction = State(
      initialValue: SearchInteraction(model: model, sourceID: initialScope?.id)
    )
    _sourceResolver = State(
      initialValue: SearchSourceResolver(statuses: sourceStatuses)
    )
  }

  var body: some View {
    GeometryReader { proxy in
      let size = CGSize(
        width: min(proxy.size.width, 860),
        height: panelHeight(in: proxy.size)
      )
      SearchWorkspace(
        interaction: interaction,
        scope: scope,
        sourceResolver: sourceResolver,
        isCompact: size.width < 720,
        model: model,
        fieldIdentity: fieldState.identity,
        focus: $focus,
        onClearScope: clearScope,
        onSubmit: openSelectedResult,
        onMoveToResults: focusResults,
        onOpen: open
      )
      .frame(width: size.width, height: size.height)
      .glassEffect(.regular, in: .rect(cornerRadius: TrawlDesign.panelCornerRadius))
      .position(x: proxy.size.width / 2, y: proxy.size.height / 2)
    }
    .onChange(of: model.phase) { oldPhase, newPhase in
      if oldPhase == .idle, newPhase == .loading {
        fieldState.requestFocus()
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
    .onKeyPress(.escape) {
      onDismiss()
      return .handled
    }
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

  private func focusResults() {
    guard let first = model.results.first else { return }
    if interaction.selectedResultID == nil {
      interaction.selectedResultID = first.id
    }
    focus = .results
  }

  private func openSelectedResult() {
    Task { await interaction.handleReturn() }
  }

  private func open(_ hit: SearchHit) {
    interaction.selectedResultID = hit.id
    Task { await interaction.handleReturn() }
  }

  private func panelHeight(in available: CGSize) -> CGFloat {
    switch SearchWorkspaceMode.resolve(phase: model.phase, resultCount: model.results.count) {
    case .field:
      72
    case .outcome, .results:
      min(available.height, 560)
    }
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
