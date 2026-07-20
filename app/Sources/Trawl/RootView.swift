import PermissionGuide
import SwiftUI
import TrawlClient
import TrawlCore

struct RootView: View {
  @Environment(\.scenePhase) private var scenePhase
  @Bindable var model: AppModel

  let client: any TrawlClient
  let featureFlags: AppFeatureFlags
  let buildIdentity: BuildIdentity
  let aiInstruction: String
  let openFullDiskAccess: @MainActor () -> Void

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
    appInstallations: MacAppInstallations = MacAppInstallations(),
    buildIdentity: BuildIdentity = .current,
    aiInstruction: String = AgentPrompts.connectAI(
      helperCommand: TrawlRuntimeConfiguration().agentCommand
    ),
    openFullDiskAccess: @escaping @MainActor () -> Void =
      PermissionGuideController.openSystemSettings
  ) {
    self.model = model
    self.client = client
    self.featureFlags = featureFlags
    self.buildIdentity = buildIdentity
    self.aiInstruction = aiInstruction
    self.openFullDiskAccess = openFullDiskAccess
    _onboarding = State(initialValue: onboarding)
    _appInstallations = State(initialValue: appInstallations)
  }

  var body: some View {
    VStack(spacing: 0) {
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
            buildIdentity: buildIdentity,
            aiInstruction: aiInstruction,
            onFinish: finishOnboarding
          )
        }
      }
      if onboarding.isComplete {
        Divider()
        BuildIdentityFooter(
          identity: buildIdentity,
          isExperimental: featureFlags.isExperimental
        )
      }
    }
    .background(
      WindowBehavior(
        isOnboarding: !onboarding.isComplete,
        keepsPermissionGuideVisible: onboarding.stage == .permission
      )
    )
    .environment(iconStore)
    .toolbar {
      if onboarding.isComplete {
        ToolbarItem {
          Button(OperationalCopy.syncNow, systemImage: "arrow.clockwise") {
            appInstallations.refresh(manifests: model.sources.map(\.manifest))
            let appIDs = syncAppIDs
            guard !appIDs.isEmpty else { return }
            Task { await model.syncNow(appIDs: appIDs) }
          }
          .disabled(model.isSyncing)
        }
      }
    }
    .onChange(of: scenePhase) { _, phase in
      guard phase == .active else { return }
      appInstallations.refresh(manifests: model.sources.map(\.manifest))
      if onboarding.isComplete {
        Task { await model.recoverFullDiskAccess(appIDs: syncAppIDs) }
      } else {
        onboarding.applicationDidBecomeActive(appModel: model) { syncAppIDs }
      }
    }
    .task {
      if onboarding.isComplete {
        await model.recoverFullDiskAccess(appIDs: syncAppIDs)
      }
    }
    .onChange(of: model.sources, initial: true) { _, sources in
      let manifests = sources.map(\.manifest)
      appInstallations.refresh(manifests: manifests)
      iconStore.update(manifests: manifests)
    }
    .task(id: automaticSyncTaskID) {
      guard onboarding.isComplete else { return }
      await model.runAutomaticSyncLoop(appIDs: syncAppIDs)
    }
  }

  private var syncAppIDs: [String] {
    featureFlags.syncAppIDs(
      reportedAppIDs: model.sources.map(\.id)
        + model.statusFailures.map(\.sourceID),
      unavailableAppIDs: appInstallations.unavailableAppIDs
    )
  }

  private var automaticSyncTaskID: AutomaticSyncTaskID {
    AutomaticSyncTaskID(isOnboardingComplete: onboarding.isComplete, appIDs: syncAppIDs)
  }

  @ViewBuilder
  private var home: some View {
    VStack(spacing: 0) {
      if model.needsFullDiskAccessRecovery {
        PermissionRecoveryBanner {
          openFullDiskAccess()
        }
        .padding(.horizontal, TrawlDesign.contentInset)
        .padding(.top, TrawlDesign.contentInset)
      }
      if case .loading = model.phase, model.sources.isEmpty {
        ProgressView("Loading apps")
          .controlSize(.large)
          .frame(maxWidth: .infinity, maxHeight: .infinity)
      } else if model.needsFullDiskAccessRecovery, model.restingSources.isEmpty {
        Spacer()
      } else if model.blockingFailureMessage != nil {
        FailureView {
          Task { await model.refresh() }
        }
      } else {
        ConstellationView(
          sources: homeSources,
          sourceDetailOverrides: HomeSourcePresentation.detailOverrides(
            for: homeSources,
            appInstallations: appInstallations
          ),
          activity: constellationActivity,
          trafficEvent: constellationTrafficEvent,
          onSelectEverything: { showSearch(scope: nil) },
          onSelectSource: { showSearch(scope: $0) }
        )
        .padding(TrawlDesign.contentInset)
      }
    }
  }

  private var homeSources: [RestingSource] {
    model.restingSources.filter { featureFlags.includes($0.id) }
  }

  private func showSearch(scope: RestingSource?) {
    searchScope = scope
    hasSearchWorkspace = true
    isSearching = true
  }

  private func finishOnboarding() {
    onboarding.complete()
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

enum HomeSourcePresentation {
  @MainActor
  static func detailOverrides(
    for sources: [RestingSource],
    appInstallations: MacAppInstallations
  ) -> [String: String] {
    Dictionary(
      uniqueKeysWithValues: sources.compactMap { source in
        guard !appInstallations.isAvailable(source.id) else { return nil }
        return (source.id, OperationalCopy.notInstalled)
      })
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
  let retry: () -> Void

  var body: some View {
    ContentUnavailableView {
      Label(OperationalCopy.appsUnavailable, systemImage: "exclamationmark.triangle")
    } description: {
      Text(OperationalCopy.statusCheckFailed)
      Text(OperationalCopy.statusCheckRecovery)
    } actions: {
      Button(OperationalCopy.retry, action: retry)
    }
  }
}
