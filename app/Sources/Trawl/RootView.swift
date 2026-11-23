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
  @State private var searchFocusRequestToken = 0
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
          Button("") {
            searchFocusRequestToken &+= 1
          }
          .keyboardShortcut("k", modifiers: .command)
          .opacity(0)
          .frame(width: 0, height: 0)
          .accessibilityHidden(true)
          Button("") {
            searchFocusRequestToken &+= 1
          }
          .keyboardShortcut("f", modifiers: .command)
          .opacity(0)
          .frame(width: 0, height: 0)
          .accessibilityHidden(true)
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

  @ViewBuilder
  private var home: some View {
    VStack(spacing: 0) {
      if model.needsFullDiskAccessRecovery {
        PermissionRecoveryBanner {
          openFullDiskAccess()
        }
        .padding(.horizontal, TrawlDesign.contentInset)
        .padding(.top, TrawlDesign.Space.md)
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
        StrataWorkspaceView(
          client: client,
          trawlers: homeTrawlers,
          searchableTrawlers: searchableHomeTrawlers,
          trawlerStatuses: model.trawlerStatuses.filter {
            featureFlags.includes($0.id)
          },
          detailOverrides: HomeTrawlerPresentation.detailOverrides(
            for: homeTrawlers,
            appInstallations: appInstallations
          ),
          updatingTrawlers: activelyUpdatingTrawlers,
          focusRequestToken: searchFocusRequestToken
        )
      }
    }
  }

  private var homeTrawlers: [RestingTrawler] {
    model.homeTrawlers.filter { featureFlags.includes($0.id) }
  }

  private var searchableHomeTrawlers: [RestingTrawler] {
    homeTrawlers.filter { $0.state != .comingSoon }
  }

  private var activelyUpdatingTrawlers: Set<RegisteredTrawlerIdentity> {
    guard model.isUpdating else { return [] }
    return Set(
      model.updateProgress.compactMap { registeredTrawler, progressState in
        switch progressState {
        case .waiting, .building, .finalising:
          registeredTrawler
        case .finished, .failed:
          nil
        }
      })
  }

  private func finishOnboarding() {
    onboarding.complete()
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

private struct CanvasBackground: View {
  var body: some View {
    TrawlDesign.Colors.paper
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
