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
  @State private var trawlerIconStore = TrawlerIconStore()
  @State private var searchScope: RestingTrawler?
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
    aiInstruction: String = AgentPrompts.connectAI,
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
              trawlerStatuses: model.trawlerStatuses.filter {
                featureFlags.includes($0.id)
              },
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
        isOnboarding: !onboarding.isComplete
      )
    )
    .environment(trawlerIconStore)
    .toolbar {
      if onboarding.isComplete {
        ToolbarItem {
          Button(OperationalCopy.Home.syncNow, systemImage: "arrow.clockwise") {
            refreshAppMetadata()
            let registeredTrawlers = trawlersToSync
            guard !registeredTrawlers.isEmpty else { return }
            Task {
              await model.syncNow(registeredTrawlers: registeredTrawlers)
            }
          }
          .disabled(model.isSyncing)
        }
      }
    }
    .onChange(of: scenePhase) {
      guard scenePhase == .active else { return }
      refreshAppMetadata()
      if onboarding.isComplete {
        Task {
          await model.recoverFullDiskAccess(
            registeredTrawlers: trawlersToSync)
        }
      } else {
        onboarding.applicationDidBecomeActive(appModel: model) {
          trawlersToSync
        }
      }
    }
    .task {
      if onboarding.isComplete {
        await model.recoverFullDiskAccess(
          registeredTrawlers: trawlersToSync)
      } else {
        onboarding.checkPermission(appModel: model) {
          trawlersToSync
        }
      }
    }
    .onChange(of: model.registeredTrawlerCatalog, initial: true) { _, _ in
      refreshAppMetadata()
    }
    .task(id: automaticSyncTaskID) {
      guard automaticSyncTaskID.shouldRun else { return }
      await model.runAutomaticSyncLoop(registeredTrawlers: trawlersToSync)
    }
  }

  private var trawlersToSync: [RegisteredTrawlerIdentity] {
    featureFlags.trawlersToSync(
      reportedTrawlers: model.syncCandidateTrawlers,
      unavailableTrawlers: appInstallations.unavailableTrawlers
    )
  }

  private func refreshAppMetadata() {
    appInstallations.refresh(
      registeredTrawlerCatalog: model.registeredTrawlerCatalog)
    trawlerIconStore.update(
      registeredTrawlerCatalog: model.registeredTrawlerCatalog)
  }

  private var automaticSyncTaskID: AutomaticSyncTaskID {
    AutomaticSyncTaskID(
      onboardingStage: onboarding.stage,
      registeredTrawlers: trawlersToSync)
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
      if case .loading = model.phase, model.trawlerStatuses.isEmpty {
        ProgressView("Loading apps")
          .controlSize(.large)
          .frame(maxWidth: .infinity, maxHeight: .infinity)
      } else if model.needsFullDiskAccessRecovery, model.restingTrawlers.isEmpty {
        Spacer()
      } else if model.blockingFailureMessage != nil {
        FailureView {
          Task { await model.refresh() }
        }
      } else {
        ConstellationView(
          trawlers: homeTrawlers,
          trawlerDetailOverrides: HomeTrawlerPresentation.detailOverrides(
            for: homeTrawlers,
            appInstallations: appInstallations
          ),
          disabledTrawlers: comingSoonTrawlers,
          activity: constellationActivity,
          trafficEvent: constellationTrafficEvent,
          onSelectEverything: { showSearch(scope: nil) },
          onSelectTrawler: { showSearch(scope: $0) }
        )
        .padding(TrawlDesign.constellationInset)
      }
    }
  }

  private var homeTrawlers: [RestingTrawler] {
    model.homeTrawlers.filter { featureFlags.includes($0.id) }
  }

  private var comingSoonTrawlers: Set<RegisteredTrawlerIdentity> {
    Set(
      model.registeredTrawlerCatalog.compactMap {
        $0.registeredTrawlerReleaseState == .comingSoon ? $0.id : nil
      })
  }

  private func showSearch(scope: RestingTrawler?) {
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

enum HomeTrawlerPresentation {
  @MainActor
  static func detailOverrides(
    for trawlers: [RestingTrawler],
    appInstallations: MacAppInstallations
  ) -> [RegisteredTrawlerIdentity: String] {
    Dictionary(
      uniqueKeysWithValues: trawlers.compactMap { trawler in
        if trawler.state == "comingSoon" {
          return (trawler.id, OperationalCopy.AppStatus.comingSoon)
        }
        guard !appInstallations.isAvailable(trawler.id) else { return nil }
        return (trawler.id, OperationalCopy.AppStatus.notInstalled)
      })
  }
}

struct AutomaticSyncTaskID: Hashable {
  let onboardingStage: OnboardingStage
  let registeredTrawlers: [RegisteredTrawlerIdentity]

  var shouldRun: Bool {
    onboardingStage == .building
      || onboardingStage == .complete
  }
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
      Label(OperationalCopy.AppStatus.appsUnavailable, systemImage: "exclamationmark.triangle")
    } description: {
      Text(OperationalCopy.AppStatus.statusCheckFailed)
      Text(OperationalCopy.AppStatus.statusCheckRecovery)
    } actions: {
      Button(OperationalCopy.SharedAction.retry, action: retry)
    }
  }
}
