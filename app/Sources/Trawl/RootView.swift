import AppKit
import Observation
import PermissionGuide
import SwiftUI
import TrawlClient
import TrawlCore

@MainActor
@Observable
final class AppCommandSignals {
  var focusSearchRequest = 0
  var updateArchiveRequest = 0
  var selectAllAppsRequest = 0
  var showConnectAIRequest = 0

  func focusSearch() { focusSearchRequest &+= 1 }
  func updateArchive() { updateArchiveRequest &+= 1 }
  func selectAllApps() { selectAllAppsRequest &+= 1 }
  func showConnectAI() { showConnectAIRequest &+= 1 }
}

struct RootView: View {
  @Environment(\.scenePhase) private var scenePhase
  @Bindable var model: AppModel

  let client: any TrawlClient
  let featureFlags: AppFeatureFlags
  let buildIdentity: BuildIdentity
  let aiInstruction: String
  let terminalCommand: String
  let openFullDiskAccess: @MainActor () -> Void
  let commandSignals: AppCommandSignals

  private enum SearchRecordPresentationState: Equatable {
    case hidden
    case showingRecord
    case returnedToResults
  }

  @State private var onboarding: OnboardingModel
  @State private var appInstallations: MacAppInstallations
  @State private var trawlerIconStore = TrawlerIconStore()
  @State private var helperController = TrawlHelperController()
  @State private var searchModel: SearchModel
  @State private var searchInteraction: SearchInteraction
  @State private var trawlerResolver = SearchTrawlerResolver(statuses: [])
  @State private var fieldState = SearchFieldState()
  @State private var selectedScope: RestingTrawler?
  @State private var activeCategory: TrawlerFilterCategory = .all
  @State private var recordPresentation: SearchRecordPresentationState = .hidden
  @State private var isShowingSemanticPopover = false
  @State private var isShowingConnectAISheet = false
  @State private var isShowingRecentUpdateCompletion = false
  @State private var updateCompletionLingerTask: Task<Void, Never>?
  @FocusState private var focus: SearchFocus?

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
      PermissionGuideController.openSystemSettings,
    commandSignals: AppCommandSignals = AppCommandSignals()
  ) {
    self.model = model
    self.client = client
    self.featureFlags = featureFlags
    self.buildIdentity = buildIdentity
    self.aiInstruction = aiInstruction
    self.terminalCommand = terminalCommand
    self.openFullDiskAccess = openFullDiskAccess
    self.commandSignals = commandSignals
    _onboarding = State(initialValue: onboarding)
    _appInstallations = State(initialValue: appInstallations)
    let searchModel = SearchModel(client: client)
    _searchModel = State(initialValue: searchModel)
    _searchInteraction = State(
      initialValue: SearchInteraction(model: searchModel, registeredTrawler: nil)
    )
  }

  private var homeTrawlers: [RestingTrawler] {
    model.homeTrawlers.filter { featureFlags.includes($0.id) }
  }

  private var availableCategories: [TrawlerFilterCategory] {
    TrawlerFilterCategory.availableCategories(for: homeTrawlers)
  }

  private var effectiveSearchTrawlerScope: RegisteredTrawlerIdentity? {
    if let selectedScope {
      return selectedScope.id
    }
    if activeCategory != .all {
      let matching = activeCategory.matchingTrawlers(in: homeTrawlers)
      if matching.count == 1 {
        return matching.first?.id
      }
    }
    return nil
  }

  private var filteredSearchMatches: [SearchMatch] {
    if selectedScope != nil || activeCategory == .all {
      return searchModel.searchMatches
    }
    return searchModel.searchMatches.filter { match in
      guard let registeredTrawler = match.registeredTrawler else { return true }
      return activeCategory.matches(registeredTrawler)
    }
  }

  private var hasActiveQuery: Bool {
    !searchInteraction.query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
  }

  private var trawlersToUpdate: [RegisteredTrawlerIdentity] {
    featureFlags.trawlersToUpdate(
      reportedTrawlers: model.updateCandidateTrawlers,
      unavailableTrawlers: appInstallations.unavailableTrawlers
    )
  }

  var body: some View {
    Group {
      if onboarding.isComplete {
        spotlightShell
      } else {
        ZStack {
          TrawlDesign.canvasBackground.ignoresSafeArea()
          OnboardingView(
            onboarding: onboarding,
            appModel: model,
            flags: featureFlags,
            appInstallations: appInstallations,
            buildIdentity: buildIdentity,
            aiInstruction: aiInstruction,
            onFinish: { onboarding.complete() }
          )
        }
      }
    }
    .background(WindowBehavior(isOnboarding: !onboarding.isComplete))
    .environment(trawlerIconStore)
    .toolbar {
      if onboarding.isComplete {
        ToolbarItem(placement: .automatic) {
          Button {
            isShowingSemanticPopover.toggle()
          } label: {
            Label(
              helperController.semanticStatus.isEnabled
                ? OperationalCopy.Home.semanticOnShort
                : OperationalCopy.Home.semanticOffShort,
              systemImage: "brain.head.profile"
            )
            .labelStyle(.titleAndIcon)
          }
          .popover(isPresented: $isShowingSemanticPopover, arrowEdge: .bottom) {
            SemanticSearchPopoverView(helperController: helperController)
          }
        }
        ToolbarItem(placement: .automatic) {
          Button {
            isShowingConnectAISheet = true
          } label: {
            Label(OperationalCopy.Home.connectAIButton, systemImage: "sparkles")
              .labelStyle(.titleAndIcon)
          }
        }
        ToolbarItem(placement: .automatic) {
          ArchiveUpdateToolbarButton(
            isUpdating: model.isUpdating,
            isShowingRecentCompletion: isShowingRecentUpdateCompletion,
            lastUpdateTime: model.latestArchiveUpdateTime,
            onUpdate: triggerArchiveUpdate
          )
        }
      }
    }
    .sheet(isPresented: $isShowingConnectAISheet) {
      AgentAccessSheetView(
        helperController: helperController,
        aiInstruction: aiInstruction,
        terminalCommand: terminalCommand,
        onDismiss: { isShowingConnectAISheet = false }
      )
    }
    .onChange(of: model.lastSuccessfullyCompletedArchiveUpdateTime) { _, updateTime in
      guard updateTime != nil else { return }
      isShowingRecentUpdateCompletion = true
      updateCompletionLingerTask?.cancel()
      updateCompletionLingerTask = Task { @MainActor in
        try? await Task.sleep(for: .seconds(20))
        guard !Task.isCancelled else { return }
        isShowingRecentUpdateCompletion = false
      }
    }
    .onChange(of: scenePhase) {
      guard scenePhase == .active else { return }
      refreshAppMetadata()
      if onboarding.isComplete {
        Task {
          await model.recoverFullDiskAccess(registeredTrawlers: trawlersToUpdate)
          await helperController.refreshSemanticStatus()
        }
      } else {
        onboarding.applicationDidBecomeActive(appModel: model) {
          trawlersToUpdate
        }
      }
    }
    .onChange(of: model.registeredTrawlerCatalog, initial: true) { _, _ in
      refreshAppMetadata()
    }
    .onChange(of: model.trawlerStatuses, initial: true) { _, statuses in
      trawlerResolver.replace(with: statuses.filter { featureFlags.includes($0.id) })
    }
    .onChange(of: commandSignals.focusSearchRequest) { _, _ in
      focusSearchField()
    }
    .onChange(of: commandSignals.updateArchiveRequest) { _, _ in
      triggerArchiveUpdate()
    }
    .onChange(of: commandSignals.selectAllAppsRequest) { _, _ in
      selectAllApps()
    }
    .onChange(of: commandSignals.showConnectAIRequest) { _, _ in
      if onboarding.isComplete {
        isShowingConnectAISheet = true
      }
    }
    .task {
      if onboarding.isComplete {
        await model.recoverFullDiskAccess(registeredTrawlers: trawlersToUpdate)
        await helperController.refreshSemanticStatus()
        focusSearchField()
      } else {
        onboarding.checkPermission(appModel: model) {
          trawlersToUpdate
        }
      }
    }
  }

  private var spotlightShell: some View {
    HStack(spacing: 0) {
      ArchiveSidebarView(
        trawlers: homeTrawlers,
        selectedTrawlerID: selectedScope?.id,
        totalRecordCount: model.totalArchivedRecordCount,
        updateProgress: model.updateProgress,
        unavailableTrawlers: appInstallations.unavailableTrawlers,
        needsFullDiskAccessRecovery: model.needsFullDiskAccessRecovery,
        buildIdentity: buildIdentity,
        isExperimental: featureFlags.isExperimental,
        onSelectAll: selectAllApps,
        onSelectTrawler: selectTrawlerScope,
        onUpdateTrawler: updateSingleTrawler,
        onUnlockFullDiskAccess: openFullDiskAccess
      )
      Divider()
      GeometryReader { proxy in
        let isCompact = TrawlDesign.usesCompactSearchLayout(width: proxy.size.width)
        VStack(spacing: 0) {
          SpotlightSearchHeader(
            query: $searchInteraction.query,
            scope: selectedScope,
            activeCategory: activeCategory,
            availableCategories: availableCategories,
            isSearching: searchModel.phase == .loading,
            fieldIdentity: fieldState.identity,
            focus: $focus,
            onClearScope: clearScope,
            onSelectCategory: selectCategory,
            onSubmit: openSelectedOrFirstResult,
            onMoveToResults: { focusResults(isCompact: isCompact) }
          )
          Divider()
          mainContentArea(isCompact: isCompact)
        }
        .background(TrawlDesign.canvasBackground)
        .onChange(of: searchModel.phase) { _, newPhase in
          if newPhase != .loading {
            searchInteraction.reconcileCommittedResults()
            if !isCompact,
              let first = filteredSearchMatches.first,
              searchInteraction.selectedSearchMatchIdentifier == nil
            {
              openSearchMatch(first)
            } else if searchInteraction.selectedSearchMatchIdentifier == nil {
              recordPresentation = .hidden
            }
          }
        }
      }
    }
    .onKeyPress(.escape) {
      handleEscape()
      return .handled
    }
    .onExitCommand(perform: handleEscape)
    .onChange(of: effectiveSearchTrawlerScope) { _, newScope in
      searchInteraction.changeScope(to: newScope)
    }
    .task(
      id: SearchKey(
        query: searchInteraction.query,
        registeredTrawler: effectiveSearchTrawlerScope
      )
    ) {
      await searchModel.search(
        searchInteraction.query,
        registeredTrawler: effectiveSearchTrawlerScope
      )
    }
  }

  @ViewBuilder
  private func mainContentArea(isCompact: Bool) -> some View {
    if hasActiveQuery {
      SearchResultsWorkspace(
        interaction: searchInteraction,
        scope: selectedScope,
        activeCategory: activeCategory,
        filteredMatches: filteredSearchMatches,
        trawlerResolver: trawlerResolver,
        isCompact: isCompact,
        model: searchModel,
        helperController: helperController,
        focus: $focus,
        onSubmit: openSelectedOrFirstResult,
        onEscape: handleEscape,
        onOpen: openSearchMatch,
        onOpenLink: openLinkedRecord,
        onReturnToResults: returnToResults,
        showsCompactRecord: recordPresentation == .showingRecord
      )
    } else if case .loading = model.phase, model.trawlerStatuses.isEmpty, homeTrawlers.isEmpty {
      ProgressView("Loading apps")
        .controlSize(.large)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    } else if model.blockingFailureMessage != nil, homeTrawlers.isEmpty {
      FailureView {
        Task { await model.refresh() }
      }
    } else {
      ArchiveHomeOverviewView(
        trawlers: homeTrawlers,
        selectedTrawler: selectedScope,
        activeCategory: activeCategory,
        totalRecordCount: model.totalArchivedRecordCount,
        searchableCount: model.searchableTrawlerCount,
        latestUpdateTime: model.latestArchiveUpdateTime,
        isUpdating: model.isUpdating,
        updateProgress: model.updateProgress,
        unavailableTrawlers: appInstallations.unavailableTrawlers,
        needsFullDiskAccessRecovery: model.needsFullDiskAccessRecovery,
        fdaAppNames: model.fullDiskAccessOptionalTrawlers.map(\.registeredTrawlerDisplayName),
        semanticStatus: helperController.semanticStatus,
        onSelectTrawler: selectTrawlerScope,
        onUpdateTrawler: updateSingleTrawler,
        onUpdateAll: triggerArchiveUpdate,
        onUnlockFullDiskAccess: openFullDiskAccess,
        onOpenSemanticPopover: { isShowingSemanticPopover = true },
        onOpenConnectAI: { isShowingConnectAISheet = true }
      )
    }
  }

  private func refreshAppMetadata() {
    appInstallations.refresh(registeredTrawlerCatalog: model.registeredTrawlerCatalog)
    trawlerIconStore.update(registeredTrawlerCatalog: model.registeredTrawlerCatalog)
  }

  private func focusSearchField() {
    fieldState.requestFocus()
    Task { @MainActor in
      focus = .field
    }
  }

  private func selectAllApps() {
    selectedScope = nil
    activeCategory = .all
    focusSearchField()
  }

  private func selectTrawlerScope(_ trawler: RestingTrawler) {
    selectedScope = trawler
    activeCategory = .all
    focusSearchField()
  }

  private func clearScope() {
    selectedScope = nil
    focusSearchField()
  }

  private func selectCategory(_ category: TrawlerFilterCategory) {
    selectedScope = nil
    activeCategory = category
    if hasActiveQuery {
      if let first = filteredSearchMatches.first {
        openSearchMatch(first)
      } else {
        searchInteraction.selectedSearchMatchIdentifier = nil
        searchModel.clearOpenResult()
      }
    }
    focusSearchField()
  }

  private func triggerArchiveUpdate() {
    refreshAppMetadata()
    let registeredTrawlers = trawlersToUpdate
    guard !registeredTrawlers.isEmpty else { return }
    Task {
      await model.updateNow(registeredTrawlers: registeredTrawlers)
      await helperController.refreshSemanticStatus()
    }
  }

  private func updateSingleTrawler(_ registeredTrawler: RegisteredTrawlerIdentity) {
    Task {
      await model.updateNow(registeredTrawlers: [registeredTrawler])
    }
  }

  private func focusResults(isCompact: Bool) {
    guard let first = filteredSearchMatches.first else { return }
    if searchInteraction.selectedSearchMatchIdentifier == nil {
      searchInteraction.selectedSearchMatchIdentifier = first.id
      if !isCompact {
        openSearchMatch(first)
      }
    }
    focus = .results
  }

  private func openSelectedOrFirstResult() {
    if let selected = searchInteraction.resultForReturn() {
      openSearchMatch(selected)
    } else if let first = filteredSearchMatches.first {
      openSearchMatch(first)
    }
  }

  private func openSearchMatch(_ searchMatch: SearchMatch) {
    searchInteraction.selectedSearchMatchIdentifier = searchMatch.id
    recordPresentation = .showingRecord
    Task { await searchModel.open(searchMatch) }
  }

  private func openLinkedRecord(
    _ link: GloballyRoutableTrawlLink,
    anchor: RecordAnchorIdentifier
  ) {
    recordPresentation = .showingRecord
    Task { await searchModel.open(link: link, anchor: anchor) }
  }

  private func returnToResults() {
    recordPresentation = .returnedToResults
    focus = .results
  }

  private func handleEscape() {
    switch SearchEscapeAction.resolve(
      showsRecord: recordPresentation == .showingRecord
        || (searchModel.openPhase != .idle && recordPresentation != .returnedToResults),
      focus: focus
    ) {
    case .closeRecord:
      searchModel.clearOpenResult()
      recordPresentation = .hidden
      focus = searchInteraction.selectedSearchMatchIdentifier == nil ? .field : .results
    case .focusField:
      focus = .field
    case .dismiss:
      if !searchInteraction.query.isEmpty {
        searchInteraction.query = ""
        searchModel.reset()
        focus = .field
      } else if selectedScope != nil {
        selectedScope = nil
        focus = .field
      }
    }
  }
}

private struct ArchiveUpdateToolbarButton: View {
  let isUpdating: Bool
  let isShowingRecentCompletion: Bool
  let lastUpdateTime: Date?
  let onUpdate: () -> Void

  private var title: String {
    if isUpdating {
      return OperationalCopy.Home.updatingArchive
    }
    if isShowingRecentCompletion, let lastUpdateTime {
      let clockTime = lastUpdateTime.formatted(date: .omitted, time: .shortened)
      return String(format: OperationalCopy.Home.updatedAtFormat, clockTime)
    }
    return OperationalCopy.Home.updateArchiveShort
  }

  private var tooltip: String {
    if let lastUpdateTime {
      return "Update your archive (last updated \(lastUpdateTime.formatted(date: .abbreviated, time: .shortened)))"
    }
    return HumanCopy.Home.updateArchiveAction
  }

  var body: some View {
    Button(action: onUpdate) {
      Label {
        Text(title)
      } icon: {
        if isUpdating {
          ProgressView()
            .controlSize(.small)
        } else {
          Image(systemName: isShowingRecentCompletion ? "checkmark" : "arrow.clockwise")
        }
      }
      .labelStyle(.titleAndIcon)
    }
    .disabled(isUpdating)
    .help(tooltip)
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
        switch trawler.state {
        case .comingSoon:
          return (trawler.id, HumanCopy.AppStatus.comingSoon)
        case .failed, .skipped:
          return (trawler.id, OperationalCopy.Home.unavailableApp)
        case .ok:
          guard !appInstallations.isAvailable(trawler.id) else { return nil }
          return (trawler.id, HumanCopy.AppStatus.notInstalled)
        }
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
