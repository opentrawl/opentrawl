import SwiftUI
import TrawlClient
import TrawlCore

struct SearchOverlay: View {
  let onDismiss: () -> Void
  private let sourceDisplayNames: [String: String]

  @State private var scope: SourceStatus?
  @State private var model: SearchModel
  @State private var interaction: SearchInteraction
  @FocusState private var focus: SearchFocus?

  init(
    client: any TrawlClient,
    initialScope: SourceStatus?,
    sourceDisplayNames: [String: String] = [:],
    onDismiss: @escaping () -> Void
  ) {
    let model = SearchModel(client: client)
    var names = sourceDisplayNames
    if let initialScope {
      names[initialScope.id] = initialScope.name
    }
    self.onDismiss = onDismiss
    self.sourceDisplayNames = names
    _scope = State(initialValue: initialScope)
    _model = State(initialValue: model)
    _interaction = State(
      initialValue: SearchInteraction(model: model, sourceID: initialScope?.id)
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
        sourceDisplayNames: sourceDisplayNames,
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
    .onKeyPress(.escape) {
      onDismiss()
      return .handled
    }
    .task(id: SearchKey(query: interaction.query, sourceID: interaction.sourceID)) {
      await model.search(interaction.query, source: interaction.sourceID)
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
}
