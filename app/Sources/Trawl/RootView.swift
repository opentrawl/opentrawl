import SwiftUI
import TrawlClient
import TrawlCore

struct RootView: View {
  @Environment(\.scenePhase) private var scenePhase
  @Bindable var model: AppModel

  let client: any TrawlClient
  let featureFlags: AppFeatureFlags

  @State private var onboarding: OnboardingModel
  @State private var appInstallations: MacAppInstallations
  @State private var iconStore = SourceIconStore()
  @State private var searchScope: RestingSource?
  @State private var searchQuery = ""
  @State private var isSearching = false
  @State private var hasSearchWorkspace = false
  @State private var constellationActivity: ConstellationActivity = .idle
  @State private var constellationTrafficEvent: ConstellationTrafficEvent?
  @State private var trafficClearTask: Task<Void, Never>?

  init(
    model: AppModel,
    client: any TrawlClient,
    onboarding: OnboardingModel = OnboardingModel(),
    featureFlags: AppFeatureFlags = .current(),
    appInstallations: MacAppInstallations = MacAppInstallations()
  ) {
    self.model = model
    self.client = client
    self.featureFlags = featureFlags
    _onboarding = State(initialValue: onboarding)
    _appInstallations = State(initialValue: appInstallations)
  }

  var body: some View {
    ZStack {
      CanvasBackground()
      if onboarding.isComplete {
        home
          .opacity(isSearching ? 0.18 : 1)
          .allowsHitTesting(!isSearching)
          .accessibilityHidden(isSearching)
        if hasSearchWorkspace {
          SearchOverlay(
            client: client,
            scope: $searchScope,
            initialQuery: searchQuery,
            sourceStatuses: model.sources.filter { featureFlags.includes($0.id) },
            onTrafficChange: presentTraffic,
            onQueryChange: { searchQuery = $0 },
            onDismiss: dismissSearch
          )
          .opacity(isSearching ? 1 : 0)
          .allowsHitTesting(isSearching)
          .accessibilityHidden(!isSearching)
        }
      } else {
        OnboardingView(
          onboarding: onboarding,
          appModel: model,
          flags: featureFlags,
          appInstallations: appInstallations,
          onSearch: finishOnboardingAndSearch
        )
      }
    }
    .environment(iconStore)
    .toolbar {
      if onboarding.isComplete {
        ToolbarItem {
          Button(OnboardingStrings.syncNow, systemImage: "arrow.clockwise") {
            appInstallations.refresh()
            let appIDs = syncAppIDs
            guard !appIDs.isEmpty else { return }
            Task { await model.syncNow(appIDs: appIDs) }
          }
          .disabled(model.isSyncing)
        }
      }
    }
    .onChange(of: scenePhase) { _, phase in
      if phase == .active { appInstallations.refresh() }
    }
    .task(id: automaticSyncTaskID) {
      guard onboarding.isComplete else { return }
      await model.runAutomaticSyncLoop(appIDs: syncAppIDs)
    }
  }

  private var syncAppIDs: [String] {
    featureFlags.syncAppIDs(
      reportedAppIDs: model.sources.map(\.id)
        + model.statusFailures.map(\.sourceID)
        + model.skippedSources.map(\.sourceID),
      installedAppIDs: appInstallations.installedAppIDs
    )
  }

  private var automaticSyncTaskID: AutomaticSyncTaskID {
    AutomaticSyncTaskID(isOnboardingComplete: onboarding.isComplete, appIDs: syncAppIDs)
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
        sources: model.restingSources.filter { featureFlags.includes($0.id) },
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

  private func finishOnboardingAndSearch() {
    onboarding.complete()
    showSearch(scope: nil)
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

struct AutomaticSyncTaskID: Hashable {
  let isOnboardingComplete: Bool
  let appIDs: [String]
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
      Label("Apps unavailable", systemImage: "exclamationmark.triangle")
    } description: {
      Text(message)
    } actions: {
      Button("Try again", action: retry)
    }
  }
}
