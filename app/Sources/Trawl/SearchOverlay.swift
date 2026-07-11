import SwiftUI
import TrawlClient
import TrawlCore

struct SearchOverlay: View {
  let onDismiss: () -> Void
  let onActivityChange: (ConstellationActivity) -> Void
  private let sourceStatuses: [SourceStatus]

  @State private var scope: SourceStatus?
  @State private var model: SearchModel
  @State private var interaction: SearchInteraction
  @State private var sourceResolver: SearchSourceResolver
  @FocusState private var focus: SearchFocus?

  init(
    client: any TrawlClient,
    initialScope: SourceStatus?,
    sourceStatuses: [SourceStatus] = [],
    onActivityChange: @escaping (ConstellationActivity) -> Void = { _ in },
    onDismiss: @escaping () -> Void
  ) {
    let model = SearchModel(client: client)
    self.onDismiss = onDismiss
    self.onActivityChange = onActivityChange
    self.sourceStatuses = sourceStatuses
    _scope = State(initialValue: initialScope)
    _model = State(initialValue: model)
    _interaction = State(
      initialValue: SearchInteraction(model: model, sourceID: initialScope?.id)
    )
    _sourceResolver = State(
      initialValue: SearchSourceResolver(
        statuses: sourceStatuses,
        scopedStatus: initialScope
      )
    )
  }

  var body: some View {
    GeometryReader { proxy in
      let size = CGSize(
        width: min(proxy.size.width, 760),
        height: min(proxy.size.height, 560)
      )
      SearchWorkspace(
        interaction: interaction,
        scope: scope,
        sourceResolver: sourceResolver,
        isCompact: size.width < 680,
        model: model,
        focus: $focus,
        onClearScope: clearScope,
        onSubmit: openSelectedResult,
        onMoveToResults: focusResults,
        onDismiss: onDismiss
      )
      .frame(width: size.width, height: size.height)
      .glassEffect(.regular, in: .rect(cornerRadius: TrawlDesign.panelCornerRadius))
      .position(x: proxy.size.width / 2, y: proxy.size.height / 2)
    }
    .onChange(of: interaction.selectedResultID) { _, resultID in
      guard let hit = model.results.first(where: { $0.id == resultID }) else { return }
      Task { await model.open(hit) }
    }
    .onChange(of: model.phase) { _, _ in
      reportActivity()
    }
    .onChange(of: sourceStatuses) { _, statuses in
      sourceResolver.replace(with: statuses, scopedStatus: scope)
    }
    .onKeyPress(.escape) {
      onDismiss()
      return .handled
    }
    .task(id: SearchKey(query: interaction.query, sourceID: interaction.sourceID)) {
      await model.search(interaction.query, source: interaction.sourceID)
    }
    .onDisappear {
      onActivityChange(.idle)
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
    guard let hit = interaction.resultForReturn() else { return }
    Task { await model.open(hit) }
  }

  private func reportActivity() {
    switch model.phase {
    case .loading:
      onActivityChange(.searching(sourceID: interaction.sourceID, response: nil))
    case .complete, .partial, .failed:
      onActivityChange(
        .searching(
          sourceID: interaction.sourceID,
          response: ConstellationResponseEvent(
            usefulSourceIDs: Set(model.results.map(\.sourceID)),
            failedSourceIDs: Set(model.failures.map(\.sourceID))
          )
        )
      )
    case .idle, .timedOut:
      onActivityChange(.idle)
    }
  }
}
