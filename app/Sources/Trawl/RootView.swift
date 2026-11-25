import AppKit
import PermissionGuide
import SwiftUI
import TrawlClient
import TrawlCore

struct RootView: View {
  @Environment(\.scenePhase) private var scenePhase
  @Environment(\.openSettings) private var openSettings
  @Bindable var model: AppModel

  let client: any TrawlClient
  let connectionStore: ArchiveConnectionStore
  let runtimeConfiguration: TrawlRuntimeConfiguration
  let featureFlags: AppFeatureFlags
  let buildIdentity: BuildIdentity
  let aiInstruction: String
  let personalWikiPrompt: String
  let openFullDiskAccess: @MainActor () -> Void
  let onOpenSettingsPane: @MainActor (SettingsPane) -> Void

  @State private var onboarding: OnboardingModel
  @State private var appInstallations: MacAppInstallations
  @State private var trawlerIconStore: TrawlerIconStore
  @State private var destination: WorkspaceDestination = .overview
  @State private var searchFocusRequestToken = 0
  @State private var isShowingConnectionPopover = false
  @State private var isShowingXSignInSheet = false
  @State private var accountConnectionError: String?
  @State private var hasCopiedAIInstructions = false
  @State private var aiPromptCopyResetTask: Task<Void, Never>?
  @State private var hasCopiedPersonalWikiPrompt = false
  @State private var personalWikiPromptCopyResetTask: Task<Void, Never>?
  @State private var isShowingRecentUpdateCompletion = false
  @State private var updateCompletionLingerTask: Task<Void, Never>?

  init(
    model: AppModel,
    client: any TrawlClient,
    connectionStore: ArchiveConnectionStore = ArchiveConnectionStore(
      runtimeConfiguration: TrawlRuntimeConfiguration()),
    runtimeConfiguration: TrawlRuntimeConfiguration = TrawlRuntimeConfiguration(),
    onboarding: OnboardingModel = OnboardingModel(),
    featureFlags: AppFeatureFlags = .current(),
    appInstallations: MacAppInstallations = MacAppInstallations(),
    trawlerIconStore: TrawlerIconStore = TrawlerIconStore(),
    buildIdentity: BuildIdentity = .current,
    aiInstruction: String = AgentPrompts.connectAI,
    personalWikiPrompt: String = AgentPrompts.personalWiki,
    openFullDiskAccess: @escaping @MainActor () -> Void =
      PermissionGuideController.openSystemSettings,
    onOpenSettingsPane: @escaping @MainActor (SettingsPane) -> Void = { _ in }
  ) {
    self.model = model
    self.client = client
    self.connectionStore = connectionStore
    self.runtimeConfiguration = runtimeConfiguration
    self.featureFlags = featureFlags
    self.buildIdentity = buildIdentity
    self.aiInstruction = aiInstruction
    self.personalWikiPrompt = personalWikiPrompt
    self.openFullDiskAccess = openFullDiskAccess
    self.onOpenSettingsPane = onOpenSettingsPane
    _onboarding = State(initialValue: onboarding)
    _appInstallations = State(initialValue: appInstallations)
    _trawlerIconStore = State(initialValue: trawlerIconStore)
  }

  /// Remote archive connections show the workspace directly without falsely
  /// marking local Mac setup complete, so switching back to "This Mac" resumes
  /// local setup cleanly if it never ran.
  private var showsWorkspace: Bool {
    onboarding.isComplete || connectionStore.isRemote
  }

  var body: some View {
    VStack(spacing: 0) {
      ZStack {
        CanvasBackground()
        if showsWorkspace {
          home
        } else {
          OnboardingView(
            onboarding: onboarding,
            appModel: model,
            flags: featureFlags,
            appInstallations: appInstallations,
            buildIdentity: buildIdentity,
            aiInstruction: aiInstruction,
            connectionStore: connectionStore,
            onConnectedToRemoteArchive: {
              handleArchiveConnectionChanged()
            },
            onSignInGmail: signInGmail,
            onSignInX: { isShowingXSignInSheet = true },
            onOpenSourceApp: openSourceApp(bundleIdentifier:),
            onFinish: finishOnboarding
          )
        }
      }
      if showsWorkspace {
        Divider()
        BuildIdentityFooter(
          identity: buildIdentity,
          isExperimental: featureFlags.isExperimental
        )
      }
    }
    .background(
      WindowBehavior(
        isOnboarding: !showsWorkspace
      )
    )
    .environment(trawlerIconStore)
    .sheet(isPresented: $isShowingXSignInSheet) {
      XWebSignInSheet(
        runtimeConfiguration: runtimeConfiguration,
        onConnected: {
          Task {
            await model.refresh()
            await model.updateNow(
              registeredTrawlers: [RegisteredTrawlerIdentity(registeredTrawlerIdentity: "twitter")]
            )
          }
        },
        onDismiss: {
          isShowingXSignInSheet = false
        }
      )
      .environment(trawlerIconStore)
    }
    .alert(
      "Could not connect account",
      isPresented: Binding(
        get: { accountConnectionError != nil },
        set: { if !$0 { accountConnectionError = nil } }
      )
    ) {
      Button("OK", role: .cancel) { accountConnectionError = nil }
    } message: {
      Text(accountConnectionError ?? "")
    }
    .toolbar {
      if showsWorkspace {
        ToolbarItemGroup(placement: .primaryAction) {
          archiveConnectionButton
          archiveUpdateButton
          Button {
            openSettingsPane(.general)
          } label: {
            Label("Settings", systemImage: "gearshape")
          }
          .help("Open Settings (⌘,)")
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
      if showsWorkspace {
        Task {
          if connectionStore.isRemote {
            await model.refresh()
          } else {
            await model.recoverFullDiskAccess(
              registeredTrawlers: trawlersToUpdate)
          }
        }
      } else if !connectionStore.isRemote {
        onboarding.applicationDidBecomeActive(appModel: model) {
          trawlersToUpdate
        }
      }
    }
    .task {
      if showsWorkspace {
        if connectionStore.isRemote {
          await model.refresh()
        } else {
          await model.recoverFullDiskAccess(
            registeredTrawlers: trawlersToUpdate)
        }
      } else if !connectionStore.isRemote {
        onboarding.checkPermission(appModel: model) {
          trawlersToUpdate
        }
      }
    }
    .onChange(of: model.registeredTrawlerCatalog, initial: true) { _, _ in
      refreshAppMetadata()
    }
  }

  private func openSettingsPane(_ pane: SettingsPane) {
    onOpenSettingsPane(pane)
    openSettings()
  }

  private func handleArchiveConnectionChanged() {
    model.clearForConnectionSwitch()
    destination = .overview
    Task {
      await model.refresh()
    }
  }

  private var archiveConnectionButton: some View {
    Button {
      isShowingConnectionPopover.toggle()
    } label: {
      Label(
        connectionStore.activeHostShortDisplayName,
        systemImage: connectionStore.isRemote ? "network" : "desktopcomputer"
      )
      .labelStyle(.titleAndIcon)
    }
    .help(
      connectionStore.isRemote
        ? "Connected to archive on \(connectionStore.activeHostDisplayName)"
        : "Reading archive on this Mac. Click to switch archive host."
    )
    .popover(isPresented: $isShowingConnectionPopover, arrowEdge: .bottom) {
      RemoteArchiveConnectionView(
        connectionStore: connectionStore,
        showsLocalOption: true,
        onConnectionChanged: handleArchiveConnectionChanged,
        onDismiss: {
          isShowingConnectionPopover = false
        }
      )
    }
  }

  private var trawlersToUpdate: [RegisteredTrawlerIdentity] {
    let candidates = featureFlags.trawlersToUpdate(
      reportedTrawlers: model.updateCandidateTrawlers,
      unavailableTrawlers: appInstallations.unavailableTrawlers
    )
    if model.fullDiskAccessChoice == .leftOut {
      return candidates.filter {
        !AppOperabilityState.fullDiskAccessTrawlerIDs.contains($0.registeredTrawlerIdentity)
      }
    }
    return candidates
  }

  private func refreshAppMetadata() {
    appInstallations.refresh(
      registeredTrawlerCatalog: model.registeredTrawlerCatalog)
    trawlerIconStore.update(
      registeredTrawlerCatalog: model.registeredTrawlerCatalog)
  }

  private var operabilityByID: [RegisteredTrawlerIdentity: AppOperabilityState] {
    let statusesByID = Dictionary(
      model.trawlerStatuses.map { ($0.id, $0) },
      uniquingKeysWith: { _, latest in latest }
    )
    let failuresByID = Dictionary(
      (model.statusOperationFailures + model.updateOperationFailures).map {
        ($0.failedTrawler, $0)
      },
      uniquingKeysWith: { _, latest in latest }
    )
    return Dictionary(
      homeTrawlers.map { trawler in
        (
          trawler.id,
          AppOperabilityState.resolve(
            trawler: trawler,
            status: statusesByID[trawler.id],
            updateProgress: model.updateProgress[trawler.id],
            operationFailure: failuresByID[trawler.id],
            isRemoteArchive: connectionStore.isRemote,
            isInstalledOnThisMac: appInstallations.isAvailable(trawler.id),
            fullDiskAccessChoice: model.fullDiskAccessChoice
          )
        )
      },
      uniquingKeysWith: { _, latest in latest }
    )
  }

  private var freshnessLine: String {
    if connectionStore.isRemote {
      return "Read-only archive"
    }
    if model.isUpdating {
      return "Updating…"
    }
    if let updateTime = model.lastSuccessfullyCompletedArchiveUpdateTime {
      return "Updated at \(updateTime.formatted(date: .omitted, time: .shortened))"
    }
    if let latest = model.trawlerStatuses.compactMap(\.lastSuccessfullyCompletedArchiveUpdateTime).max() {
      let relative = RelativeDateTimeFormatter().localizedString(for: latest, relativeTo: Date())
      return "Updated \(relative)"
    }
    return "On this Mac"
  }

  @ViewBuilder
  private var statusBanners: some View {
    if !connectionStore.isRemote && model.needsFullDiskAccessRecovery {
      PermissionRecoveryBanner {
        openFullDiskAccess()
      }
      .padding(.horizontal, TrawlDesign.contentInset)
      .padding(.top, TrawlDesign.Space.md)
      .padding(.bottom, TrawlDesign.Space.sm)
    }
    if let refreshFailure = model.statusRefreshFailure, !model.trawlerStatuses.isEmpty {
      StaleStatusBanner(
        message: refreshFailure,
        showsConnectionAction: connectionStore.isRemote,
        onRetry: { Task { await model.refresh() } },
        onOpenConnection: { isShowingConnectionPopover = true }
      )
      .padding(.horizontal, TrawlDesign.contentInset)
      .padding(.top, TrawlDesign.Space.sm)
      .padding(.bottom, TrawlDesign.Space.sm)
    }
  }

  @ViewBuilder
  private var home: some View {
    if case .loading = model.phase, model.trawlerStatuses.isEmpty {
      VStack(spacing: 0) {
        statusBanners
        ProgressView("Loading apps")
          .controlSize(.large)
          .frame(maxWidth: .infinity, maxHeight: .infinity)
      }
    } else if !connectionStore.isRemote, model.needsFullDiskAccessRecovery,
      model.restingTrawlers.isEmpty
    {
      VStack(spacing: 0) {
        statusBanners
        Spacer()
      }
    } else if let failureMessage = model.blockingFailureMessage {
      VStack(spacing: 0) {
        statusBanners
        FailureView(
          detail: connectionStore.isRemote ? failureMessage : nil,
          onOpenConnection: connectionStore.isRemote
            ? { isShowingConnectionPopover = true }
            : nil,
          retry: {
            Task { await model.refresh() }
          }
        )
      }
    } else {
      ArchiveWorkspaceView(
        client: client,
        trawlers: homeTrawlers,
        searchableTrawlers: searchableHomeTrawlers,
        trawlerStatuses: model.trawlerStatuses.filter {
          featureFlags.includes($0.id)
        },
        operabilityByID: operabilityByID,
        hostDisplayName: connectionStore.activeHostShortDisplayName,
        freshnessLine: freshnessLine,
        isRemoteArchive: connectionStore.isRemote,
        hasCopiedAIPrompt: hasCopiedAIInstructions,
        hasCopiedPersonalWikiPrompt: hasCopiedPersonalWikiPrompt,
        focusRequestToken: searchFocusRequestToken,
        destination: $destination,
        onCopyAIPrompt: {
          NSPasteboard.general.clearContents()
          NSPasteboard.general.setString(aiInstruction, forType: .string)
          confirmAIPromptCopied()
        },
        onCopyPersonalWikiPrompt: {
          NSPasteboard.general.clearContents()
          NSPasteboard.general.setString(personalWikiPrompt, forType: .string)
          confirmPersonalWikiPromptCopied()
        },
        onOpenAIAppsSettings: { openSettingsPane(.aiApps) },
        onOpenOtherMacsSettings: { openSettingsPane(.otherMacs) },
        onOpenPluginsSettings: { openSettingsPane(.apps) },
        onOpenHostSwitcher: { isShowingConnectionPopover = true },
        onRunAttentionAction: { trawlerID, action in
          handleAttentionAction(action, for: trawlerID)
        },
        detailBanners: { statusBanners }
      )
      .id(connectionStore.connectionRevision)
    }
  }

  private func handleAttentionAction(
    _ action: AppAttentionAction,
    for trawlerID: RegisteredTrawlerIdentity
  ) {
    switch action {
    case .openFullDiskAccessSettings:
      model.setFullDiskAccessChoice(.included)
      openFullDiskAccess()
    case .openPrivacySettings(let paneAnchor):
      if let url = URL(
        string: "x-apple.systempreferences:com.apple.preference.security?\(paneAnchor)"
      ) {
        NSWorkspace.shared.open(url)
      }
    case .openSourceApp(let bundleIdentifier, _):
      openSourceApp(bundleIdentifier: bundleIdentifier)
    case .signInGmail:
      signInGmail()
    case .signInX:
      isShowingXSignInSheet = true
    case .retryUpdate:
      Task {
        await model.updateNow(registeredTrawlers: [trawlerID])
      }
    }
  }

  private func signInGmail() {
    Task {
      do {
        try await TrawlerAccountConnector.connectGmail(configuration: runtimeConfiguration)
        await model.refresh()
        await model.updateNow(
          registeredTrawlers: [RegisteredTrawlerIdentity(registeredTrawlerIdentity: "gmail")]
        )
      } catch {
        accountConnectionError = error.localizedDescription
      }
    }
  }

  private func openSourceApp(bundleIdentifier: String) {
    guard let appURL = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleIdentifier) else {
      return
    }
    let config = NSWorkspace.OpenConfiguration()
    config.activates = true
    NSWorkspace.shared.openApplication(at: appURL, configuration: config)
  }

  private var homeTrawlers: [RestingTrawler] {
    model.homeTrawlers.filter { featureFlags.includes($0.id) }
  }

  private var searchableHomeTrawlers: [RestingTrawler] {
    homeTrawlers.filter { $0.state != .comingSoon }
  }

  private func finishOnboarding() {
    onboarding.complete()
  }

  @ViewBuilder
  private var archiveUpdateButton: some View {
    if connectionStore.isRemote {
      Button {
        Task { await model.refresh() }
      } label: {
        Label(
          OperationalCopy.Home.refreshRemoteArchive,
          systemImage: "arrow.clockwise"
        )
        .labelStyle(.titleAndIcon)
      }
    } else if model.isUpdating {
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

  private func confirmPersonalWikiPromptCopied() {
    hasCopiedPersonalWikiPrompt = true
    personalWikiPromptCopyResetTask?.cancel()
    personalWikiPromptCopyResetTask = Task { @MainActor in
      try? await Task.sleep(for: .seconds(5))
      guard !Task.isCancelled else { return }
      hasCopiedPersonalWikiPrompt = false
    }
  }
}

enum HomeTrawlerPresentation {
  @MainActor
  static func detailOverrides(
    for trawlers: [RestingTrawler],
    appInstallations: MacAppInstallations,
    isRemoteArchive: Bool = false
  ) -> [RegisteredTrawlerIdentity: String] {
    Dictionary(
      uniqueKeysWithValues: trawlers.compactMap { trawler in
        switch trawler.state {
        case .comingSoon:
          return (trawler.id, HumanCopy.AppStatus.comingSoon)
        case .failed, .skipped:
          return (trawler.id, OperationalCopy.Home.unavailableApp)
        case .ok:
          guard !isRemoteArchive, !appInstallations.isAvailable(trawler.id) else { return nil }
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

private struct CanvasBackground: View {
  var body: some View {
    TrawlDesign.Colors.paper
      .ignoresSafeArea()
  }
}

private struct StaleStatusBanner: View {
  let message: String
  let showsConnectionAction: Bool
  let onRetry: () -> Void
  let onOpenConnection: () -> Void

  var body: some View {
    HStack(spacing: TrawlDesign.Space.sm) {
      Image(systemName: "exclamationmark.triangle.fill")
        .foregroundStyle(TrawlDesign.Colors.warningAmber)
      Text("\(OperationalCopy.Connection.staleStatusPrefix) \(message)")
        .font(TrawlDesign.Typography.meta)
        .foregroundStyle(TrawlDesign.Colors.ink)
        .lineLimit(2)
      Spacer()
      if showsConnectionAction {
        Button(OperationalCopy.Connection.changeArchiveHost, action: onOpenConnection)
          .buttonStyle(.bordered)
          .controlSize(.small)
          .fixedSize()
      }
      Button(OperationalCopy.SharedAction.retry, action: onRetry)
        .buttonStyle(.bordered)
        .controlSize(.small)
        .fixedSize()
    }
    .padding(.horizontal, 12)
    .padding(.vertical, 8)
    .background(
      TrawlDesign.Colors.surface,
      in: TrawlDesign.Shape.surface
    )
    .overlay {
      TrawlDesign.Shape.surface
        .stroke(TrawlDesign.Colors.warningAmber.opacity(0.45), lineWidth: 1)
    }
  }
}

struct FailureView: View {
  var detail: String? = nil
  var onOpenConnection: (() -> Void)? = nil
  let retry: () -> Void

  var body: some View {
    VStack(spacing: 14) {
      Image(systemName: "exclamationmark.triangle")
        .font(TrawlDesign.Typography.pageTitle)
        .imageScale(.large)
        .foregroundStyle(.secondary)
      VStack(spacing: 6) {
        Text(OperationalCopy.AppStatus.appsUnavailable)
          .font(TrawlDesign.Typography.itemTitle)
          .foregroundStyle(.primary)
        if let detail, !detail.isEmpty {
          Text(detail)
            .font(TrawlDesign.Typography.body)
            .foregroundStyle(.secondary)
            .multilineTextAlignment(.center)
            .fixedSize(horizontal: false, vertical: true)
        } else {
          Text(OperationalCopy.AppStatus.statusCheckFailed)
            .font(TrawlDesign.Typography.body)
            .foregroundStyle(.secondary)
            .multilineTextAlignment(.center)
          Text(OperationalCopy.AppStatus.statusCheckRecovery)
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(.secondary)
            .multilineTextAlignment(.center)
        }
      }
      .frame(maxWidth: 440)

      HStack(spacing: 10) {
        if let onOpenConnection {
          Button(OperationalCopy.Connection.changeArchiveHost, action: onOpenConnection)
            .buttonStyle(.bordered)
            .fixedSize()
        }
        Button(OperationalCopy.SharedAction.retry, action: retry)
          .buttonStyle(.borderedProminent)
          .fixedSize()
      }
      .padding(.top, 4)
    }
    .padding(24)
    .frame(maxWidth: .infinity, maxHeight: .infinity)
  }
}
