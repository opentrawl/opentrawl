import SwiftUI
import TrawlClient
import TrawlCore

struct RootView: View {
  @Bindable var model: AppModel

  let client: any TrawlClient

  @State private var iconStore = SourceIconStore()
  @State private var searchScope: RestingSource?
  @State private var searchQuery = ""
  @State private var isSearching = false
  @State private var hasSearchWorkspace = false
  @State private var constellationActivity: ConstellationActivity = .idle
  @State private var constellationTrafficEvent: ConstellationTrafficEvent?
  @State private var trafficClearTask: Task<Void, Never>?

  var body: some View {
    ZStack {
      CanvasBackground()
      home
        .opacity(isSearching ? 0.18 : 1)
        .allowsHitTesting(!isSearching)
        .accessibilityHidden(isSearching)
      if hasSearchWorkspace {
        SearchOverlay(
          client: client,
          scope: $searchScope,
          initialQuery: searchQuery,
          sourceStatuses: model.sources,
          onTrafficChange: presentTraffic,
          onQueryChange: { searchQuery = $0 },
          onDismiss: dismissSearch
        )
        .opacity(isSearching ? 1 : 0)
        .allowsHitTesting(isSearching)
        .accessibilityHidden(!isSearching)
      }
    }
    .environment(iconStore)
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
    hasSearchWorkspace = true
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
    Color(nsColor: .windowBackgroundColor)
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
