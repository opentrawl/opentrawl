import SwiftUI
import TrawlClient
import TrawlCore

struct RootView: View {
  @Bindable var model: AppModel

  let client: any TrawlClient

  @State private var iconStore = SourceIconStore()
  @State private var searchScope: RestingSource?
  @State private var isSearching = false
  @State private var constellationActivity: ConstellationActivity = .idle
  @State private var constellationTrafficEvent: ConstellationTrafficEvent?
  @State private var trafficClearTask: Task<Void, Never>?

  var body: some View {
    ZStack {
      CanvasBackground()
      home
        .opacity(isSearching ? TrawlDesign.backgroundContentOpacity : 1)
        .blur(radius: isSearching ? TrawlDesign.backgroundContentBlur : 0)
        .disabled(isSearching)
        .allowsHitTesting(!isSearching)
        .accessibilityHidden(isSearching)

      if isSearching {
        Color.white.opacity(TrawlDesign.modalVeilOpacity)
          .ignoresSafeArea()
          .contentShape(.rect)
          .onTapGesture(perform: dismissSearch)
          .accessibilityHidden(true)
        SearchOverlay(
          client: client,
          initialScope: searchScope,
          sourceStatuses: model.sources,
          onTrafficChange: presentTraffic,
          onDismiss: dismissSearch
        )
        .padding(TrawlDesign.contentInset)
        .accessibilityElement(children: .contain)
        .accessibilityAddTraits(.isModal)
        .transition(.opacity.combined(with: .scale(scale: 0.98)))
      }
    }
    .environment(iconStore)
    .animation(.easeOut(duration: 0.16), value: isSearching)
  }

  @ViewBuilder
  private var home: some View {
    if case .loading = model.phase, model.sources.isEmpty {
      ProgressView("Loading sources")
        .controlSize(.large)
    } else if let message = model.blockingFailureMessage {
      FailureView(message: message) {
        Task { await model.refresh() }
      }
    } else {
      ConstellationView(
        sources: model.restingSources,
        activity: constellationActivity,
        trafficEvent: constellationTrafficEvent,
        onSelectEverything: { showSearch(scope: nil) },
        onSelectSource: { showSearch(scope: $0) }
      )
      .padding(TrawlDesign.contentInset)
    }
  }

  private func showSearch(scope: RestingSource?) {
    searchScope = scope
    isSearching = true
  }

  private func dismissSearch() {
    presentTraffic(activity: .idle, event: nil)
    isSearching = false
  }

  private func presentTraffic(
    activity: ConstellationActivity,
    event: ConstellationTrafficEvent?
  ) {
    trafficClearTask?.cancel()
    constellationActivity = activity
    constellationTrafficEvent = event
    guard event != nil else { return }
    trafficClearTask = Task { @MainActor in
      try? await Task.sleep(for: .seconds(4))
      guard !Task.isCancelled else { return }
      constellationActivity = .idle
      constellationTrafficEvent = nil
    }
  }
}

private struct CanvasBackground: View {
  var body: some View {
    Color.white
      .ignoresSafeArea()
  }
}

private struct FailureView: View {
  let message: String
  let retry: () -> Void

  var body: some View {
    ContentUnavailableView {
      Label("Sources unavailable", systemImage: "exclamationmark.triangle")
    } description: {
      Text(message)
    } actions: {
      Button("Try again", action: retry)
    }
  }
}
