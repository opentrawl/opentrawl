import AppKit
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
  let terminalCommand: String
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
  @State private var hasCopiedAIInstructions = false
  @State private var aiPromptCopyResetTask: Task<Void, Never>?
  @State private var hasCopiedTerminalCommand = false
  @State private var terminalCommandCopyResetTask: Task<Void, Never>?
  @State private var isShowingRecentUpdateCompletion = false
  @State private var updateCompletionLingerTask: Task<Void, Never>?

  init(
    model: AppModel,
    client: any TrawlClient,
    onboarding: OnboardingModel = OnboardingModel(),
    featureFlags: AppFeatureFlags = .current(),
    appInstallations: MacAppInstallations = MacAppInstallations(),
    buildIdentity: BuildIdentity = .current,
    aiInstruction: String = AgentPrompts.connectAI,
    terminalCommand: String = TrawlTerminalHandoff.executableHelpCommand(
      helperURL: TrawlRuntimeConfiguration().helperURL),
    openFullDiskAccess: @escaping @MainActor () -> Void =
      PermissionGuideController.openSystemSettings
  ) {
    self.model = model
    self.client = client
    self.featureFlags = featureFlags
    self.buildIdentity = buildIdentity
    self.aiInstruction = aiInstruction
    self.terminalCommand = terminalCommand
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
          Button {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(aiInstruction, forType: .string)
            confirmAIPromptCopied()
          } label: {
            Label(
              hasCopiedAIInstructions
                ? OperationalCopy.Home.copiedAIPromptConfirmation
                : OperationalCopy.Home.copyAIPromptAction,
              systemImage: hasCopiedAIInstructions ? "checkmark" : "sparkles"
            )
            .labelStyle(.titleAndIcon)
          }
        }
        ToolbarItem {
          Button {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(terminalCommand, forType: .string)
            confirmTerminalCommandCopied()
          } label: {
            Label {
              Text(
                markdownStyled(
                  hasCopiedTerminalCommand
                    ? OperationalCopy.Home.copiedTerminalCommandConfirmation
                    : OperationalCopy.Home.copyTerminalCommandAction)
              )
            } icon: {
              Image(
                systemName: hasCopiedTerminalCommand ? "checkmark" : "apple.terminal")
            }
            .labelStyle(.titleAndIcon)
          }
        }
        ToolbarItem {
          archiveUpdateButton
        }
      }
    }
    .onChange(of: model.lastSuccessfullyCompletedArchiveUpdateTime) { _, updateTime in
      guard updateTime != nil else { return }
      isShowingRecentUpdateCompletion = true
      updateCompletionLingerTask?.cancel()
      updateCompletionLingerTask = Task { @MainActor in
        try? await Task.sleep(for: .seconds(30))
        guard !Task.isCancelled else { return }
        isShowingRecentUpdateCompletion = false
      }
    }
    .onChange(of: scenePhase) {
      guard scenePhase == .active else { return }
      refreshAppMetadata()
      if onboarding.isComplete {
        Task {
          await model.recoverFullDiskAccess(
            registeredTrawlers: trawlersToUpdate)
        }
      } else {
        onboarding.applicationDidBecomeActive(appModel: model) {
          trawlersToUpdate
        }
      }
    }
    .task {
      if onboarding.isComplete {
        await model.recoverFullDiskAccess(
          registeredTrawlers: trawlersToUpdate)
      } else {
        onboarding.checkPermission(appModel: model) {
          trawlersToUpdate
        }
      }
    }
    .onChange(of: model.registeredTrawlerCatalog, initial: true) { _, _ in
      refreshAppMetadata()
    }
    .task(id: automaticUpdateTaskID) {
      guard automaticUpdateTaskID.shouldRun else { return }
      await model.runAutomaticUpdateLoop(registeredTrawlers: trawlersToUpdate)
    }
  }

  private var trawlersToUpdate: [RegisteredTrawlerIdentity] {
    featureFlags.trawlersToUpdate(
      reportedTrawlers: model.updateCandidateTrawlers,
      unavailableTrawlers: appInstallations.unavailableTrawlers
    )
  }

  private func refreshAppMetadata() {
    appInstallations.refresh(
      registeredTrawlerCatalog: model.registeredTrawlerCatalog)
    trawlerIconStore.update(
      registeredTrawlerCatalog: model.registeredTrawlerCatalog)
  }

  private var automaticUpdateTaskID: AutomaticUpdateTaskID {
    AutomaticUpdateTaskID(
      onboardingStage: onboarding.stage,
      registeredTrawlers: trawlersToUpdate)
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
          activity: homeConstellationActivity,
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

  /// While an update runs, the constellation animates traffic to the
  /// trawlers that are still updating.
  private var homeConstellationActivity: ConstellationActivity {
    guard model.isUpdating, case .idle = constellationActivity else {
      return constellationActivity
    }
    let updatingSourceIDs = Set(
      model.updateProgress.compactMap { registeredTrawler, progressState in
        switch progressState {
        case .waiting, .building, .finalising:
          registeredTrawler.registeredTrawlerIdentity
        case .finished, .failed:
          nil
        }
      })
    guard !updatingSourceIDs.isEmpty else { return constellationActivity }
    return .updating(sourceIDs: updatingSourceIDs)
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

  /// The archive keeps itself up to date. This button shows that state and
  /// lets people start an update themselves.
  @ViewBuilder
  private var archiveUpdateButton: some View {
    if model.isUpdating {
      Button {
      } label: {
        Label {
          Text(OperationalCopy.Home.updatingArchive)
        } icon: {
          ProgressView()
            .controlSize(.small)
        }
        .labelStyle(.titleAndIcon)
      }
      .disabled(true)
    } else {
      Button {
        refreshAppMetadata()
        let registeredTrawlers = trawlersToUpdate
        guard !registeredTrawlers.isEmpty else { return }
        Task {
          await model.updateNow(registeredTrawlers: registeredTrawlers)
        }
      } label: {
        Label(
          archiveUpdateButtonTitle,
          systemImage: isShowingRecentUpdateCompletion ? "checkmark" : "arrow.clockwise"
        )
        .labelStyle(.titleAndIcon)
      }
    }
  }

  private var archiveUpdateButtonTitle: String {
    guard let updateTime = model.lastSuccessfullyCompletedArchiveUpdateTime else {
      return HumanCopy.Home.updateArchiveAction
    }
    let clockTime = updateTime.formatted(date: .omitted, time: .shortened)
    return isShowingRecentUpdateCompletion
      ? String(format: OperationalCopy.Home.updatedAtFormat, clockTime)
      : String(format: OperationalCopy.Home.lastUpdatedAtFormat, clockTime)
  }

  private func confirmAIPromptCopied() {
    hasCopiedAIInstructions = true
    aiPromptCopyResetTask?.cancel()
    aiPromptCopyResetTask = Task { @MainActor in
      try? await Task.sleep(for: .seconds(5))
      guard !Task.isCancelled else { return }
      hasCopiedAIInstructions = false
    }
  }

  /// Renders locked copy that uses markdown code spans, such as `trawl`,
  /// with monospaced styling. The strings are compile-time constants, so a
  /// parse failure is a programming error.
  private func markdownStyled(_ lockedCopy: String) -> AttributedString {
    do {
      return try AttributedString(markdown: lockedCopy)
    } catch {
      preconditionFailure("Locked copy is not valid markdown: \(lockedCopy)")
    }
  }

  private func confirmTerminalCommandCopied() {
    hasCopiedTerminalCommand = true
    terminalCommandCopyResetTask?.cancel()
    terminalCommandCopyResetTask = Task { @MainActor in
      try? await Task.sleep(for: .seconds(5))
      guard !Task.isCancelled else { return }
      hasCopiedTerminalCommand = false
    }
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
          return (trawler.id, HumanCopy.AppStatus.comingSoon)
        }
        if trawler.state == "failed" || trawler.state == "skipped" {
          return (trawler.id, OperationalCopy.Home.unavailableApp)
        }
        guard !appInstallations.isAvailable(trawler.id) else { return nil }
        return (trawler.id, HumanCopy.AppStatus.notInstalled)
      })
  }
}

struct AutomaticUpdateTaskID: Hashable {
  let onboardingStage: OnboardingStage
  let registeredTrawlers: [RegisteredTrawlerIdentity]

  var shouldRun: Bool {
    onboardingStage == .complete
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
