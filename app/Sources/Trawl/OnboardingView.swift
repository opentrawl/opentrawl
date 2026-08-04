import AppKit
import SwiftUI
import TrawlClient
import TrawlCore

struct OnboardingView: View {
  let onboarding: OnboardingModel
  let appModel: AppModel
  let flags: AppFeatureFlags
  let appInstallations: MacAppInstallations
  let buildIdentity: BuildIdentity
  let aiInstruction: String
  let onFinish: () -> Void

  var body: some View {
    switch onboarding.stage {
    case .welcome:
      WelcomeStep(
        registeredTrawlerCatalog: appModel.registeredTrawlerCatalog,
        onContinue: {
          onboarding.showPermission(appModel: appModel)
        }
      )
    case .permission:
      PermissionStep(
        permissionCheck: onboarding.permissionCheck,
        buildIdentity: buildIdentity,
        onBack: onboarding.showWelcome,
        onOpenSettings: {
          onboarding.requestPermission(appModel: appModel) {
            refreshedTrawlersToUpdate()
          }
        },
        onCheckAgain: {
          onboarding.checkPermission(
            appModel: appModel, registeredTrawlers: refreshedTrawlersToUpdate)
        },
        onContinue: {
          onboarding.continueWithVerifiedAccess(
            appModel: appModel,
            registeredTrawlers: refreshedTrawlersToUpdate
          )
        }
      )
    case .building:
      BuildStep(
        appModel: appModel,
        appInstallations: appInstallations,
        aiInstruction: aiInstruction,
        hasCopiedAIInstructions: onboarding.hasCopiedAIInstructions,
        onCopyAIInstructions: onboarding.didCopyAIInstructions,
        onBack: { onboarding.showPermission(appModel: appModel) },
        onRetryApp: {
          onboarding.retry(appModel: appModel, registeredTrawler: $0)
        },
        onRetryInitialLoad: {
          onboarding.retryInitialLoad(appModel: appModel) {
            refreshedTrawlersToUpdate()
          }
        },
        onPermissionRecovery: {
          onboarding.reopenPermissionRecovery(appModel: appModel) {
            refreshedTrawlersToUpdate()
          }
        },
        onStop: onboarding.stopUpdate,
        onFinish: onboarding.showCommandDemo
      )
      .task(id: reportedTrawlers) {
        onboarding.resumeInitialUpdateIfNeeded(
          appModel: appModel,
          registeredTrawlers: refreshedTrawlersToUpdate()
        )
      }
    case .commandDemo:
      OpenTrawlCommandDemoView(
        helperURL: TrawlRuntimeConfiguration().helperURL,
        onBack: onboarding.returnToBuilding,
        onFinish: onFinish
      )
    case .complete:
      EmptyView()
    }
  }

  private var reportedTrawlers: [RegisteredTrawlerIdentity] {
    appModel.displayedTrawlers
  }

  private func refreshedTrawlersToUpdate() -> [RegisteredTrawlerIdentity] {
    appInstallations.refresh(
      registeredTrawlerCatalog: appModel.registeredTrawlerCatalog
    )
    return flags.trawlersToUpdate(
      reportedTrawlers: appModel.updateCandidateTrawlers,
      unavailableTrawlers: appInstallations.unavailableTrawlers
    )
  }

}

struct WelcomeStep: View {
  var icon = NSApplication.shared.applicationIconImage
  let registeredTrawlerCatalog: [RegisteredTrawlerCatalogEntry]
  let onContinue: () -> Void

  var body: some View {
    TrawlFlowScaffold(
      page: .welcome,
      composition: .centred,
      contentWidth: TrawlDesign.onboardingPageWidth
    ) {
      OnboardingHeroLayout {
        VStack(spacing: 0) {
          WelcomeMark(icon: icon)
          OnboardingProse(
            title: HumanCopy.Welcome.title,
            lede: HumanCopy.Welcome.body,
            statement: HumanCopy.Welcome.privacy,
            centred: true
          )
          .padding(.top, 24)
          WelcomeAppIconRow(registeredTrawlerCatalog: registeredTrawlerCatalog)
            .padding(.top, TrawlDesign.onboardingBlockSpacing)
        }
      }
    } actions: {
      OnboardingActionRow(
        backAction: nil,
        secondaryTitle: nil,
        secondaryAction: nil,
        primaryTitle: HumanCopy.Welcome.primaryAction,
        primaryAction: onContinue
      )
    }
  }
}

private struct WelcomeAppIconRow: View {
  let registeredTrawlerCatalog: [RegisteredTrawlerCatalogEntry]

  private var searchableEntries: [RegisteredTrawlerCatalogEntry] {
    registeredTrawlerCatalog.filter {
      $0.registeredTrawlerReleaseState != .comingSoon
    }
  }

  private var comingSoonEntries: [RegisteredTrawlerCatalogEntry] {
    registeredTrawlerCatalog.filter {
      $0.registeredTrawlerReleaseState == .comingSoon
    }
  }

  var body: some View {
    VStack(spacing: TrawlDesign.onboardingElementSpacing) {
      Text(HumanCopy.Welcome.appsTitle)
        .trawlText(.sectionHeader)
      HStack(spacing: 32) {
        HStack(spacing: 20) {
          ForEach(searchableEntries, id: \.id) { entry in
            WelcomeAppIcon(entry: entry)
          }
        }
        if !comingSoonEntries.isEmpty {
          HStack(spacing: 20) {
            ForEach(comingSoonEntries, id: \.id) { entry in
              WelcomeAppIcon(entry: entry)
            }
          }
        }
      }
    }
  }
}

private struct WelcomeAppIcon: View {
  let entry: RegisteredTrawlerCatalogEntry

  private var isComingSoon: Bool {
    entry.registeredTrawlerReleaseState == .comingSoon
  }

  private var appName: String {
    entry.registeredTrawlerManifest.registeredTrawlerDisplayName
  }

  var body: some View {
    TrawlerIconView(registeredTrawler: entry.id, size: 48)
      .overlay(alignment: .bottomTrailing) {
        if isComingSoon {
          Image(systemName: "clock.fill")
            .font(.system(size: 9, weight: .semibold))
            .foregroundStyle(.white)
            .frame(width: 17, height: 17)
            .background(.secondary, in: Circle())
            .overlay(Circle().stroke(Color(nsColor: .windowBackgroundColor), lineWidth: 2))
            .offset(x: 3, y: 3)
            .accessibilityHidden(true)
        }
      }
      .opacity(isComingSoon ? 0.72 : 1)
      .help(isComingSoon ? "\(appName) · \(HumanCopy.AppStatus.comingSoon)" : appName)
      .accessibilityLabel(appName)
      .accessibilityValue(isComingSoon ? HumanCopy.AppStatus.comingSoon : "")
  }
}

private struct WelcomeMark: View {
  let icon: NSImage?

  var body: some View {
    Image(
      nsImage: icon ?? NSImage(systemSymbolName: "shippingbox.fill", accessibilityDescription: nil)!
    )
    .resizable()
    .scaledToFit()
    .frame(width: TrawlDesign.onboardingHeroIcon, height: TrawlDesign.onboardingHeroIcon)
    .accessibilityHidden(true)
  }
}

struct PermissionStep: View {
  @State private var copiedAuditPrompt = false

  var icon = NSApplication.shared.applicationIconImage
  let permissionCheck: PermissionCheckState
  let buildIdentity: BuildIdentity
  let onBack: () -> Void
  let onOpenSettings: () -> Void
  let onCheckAgain: () -> Void
  let onContinue: () -> Void

  var body: some View {
    TrawlFlowScaffold(
      page: .access,
      contentWidth: TrawlDesign.onboardingPageWidth
    ) {
      OnboardingTaskLayout {
        OnboardingProse(
          title: HumanCopy.FullDiskAccess.title,
          lede: HumanCopy.FullDiskAccess.body,
          statement: HumanCopy.FullDiskAccess.purpose
        )
      } task: {
        VStack(alignment: .leading, spacing: 20) {
          PermissionDragDemonstration(icon: icon)
          PermissionStatus(state: permissionCheck)
            .frame(
              height: TrawlDesign.permissionStateSlotHeight,
              alignment: .topLeading
            )
        }
      } support: {
        OnboardingInformationGroup(title: HumanCopy.FullDiskAccess.trustGroupTitle) {
          TrustReview(
            buildIdentity: buildIdentity,
            copiedAuditPrompt: $copiedAuditPrompt
          )
        }
      }
    } actions: {
      OnboardingActionRow(
        backAction: onBack,
        secondaryTitle: nil,
        secondaryAction: nil,
        primaryTitle: permissionCheck == .confirmed
          ? OperationalCopy.SharedAction.continueAction
          : HumanCopy.FullDiskAccess.openAction,
        primaryAction: permissionCheck == .confirmed ? onContinue : onOpenSettings
      )
    }
  }
}

private struct PermissionDragDemonstration: View {
  @State private var isAtDestination = false

  let icon: NSImage?

  var body: some View {
    ZStack {
      RoundedRectangle(cornerRadius: 14, style: .continuous)
        .fill(Color.secondary.opacity(0.055))

      HStack(spacing: 26) {
        animationLane
        permissionList
      }
      .padding(.horizontal, 24)
    }
    .frame(maxWidth: .infinity)
    .frame(height: 126)
    .clipped()
    .onAppear {
      withAnimation(.easeInOut(duration: 1.45).repeatForever(autoreverses: false)) {
        isAtDestination = true
      }
    }
    .accessibilityElement(children: .ignore)
    .accessibilityLabel(HumanCopy.FullDiskAccess.dragAccessibilityLabel)
  }

  private var animationLane: some View {
    ZStack {
      appIcon
        .offset(x: -57)

      Image(systemName: "arrow.right")
        .foregroundStyle(.secondary)
      destinationList
        .offset(x: 57)

      appIcon
        .opacity(0.55)
      .offset(x: isAtDestination ? 57 : -57)
      .zIndex(1)
    }
    .frame(width: 180, height: 76)
  }

  private var appIcon: some View {
    Image(
      nsImage: icon ?? NSImage(
        systemSymbolName: "shippingbox.fill",
        accessibilityDescription: HumanCopy.FullDiskAccess.openTrawlLabel
      )!
    )
    .resizable()
    .scaledToFit()
    .frame(width: 46, height: 46)
  }

  private var destinationList: some View {
    VStack(spacing: 5) {
      ForEach(0..<3) { index in
        Capsule()
          .fill(Color.secondary.opacity(index == 1 ? 0.22 : 0.11))
          .frame(width: index == 1 ? 42 : 34, height: 3)
      }
    }
    .frame(width: 58, height: 58)
    .background(Color(nsColor: .windowBackgroundColor), in: RoundedRectangle(cornerRadius: 9))
    .overlay {
      RoundedRectangle(cornerRadius: 9)
        .strokeBorder(
          Color.secondary.opacity(0.3),
          style: StrokeStyle(lineWidth: 1, dash: [4])
        )
    }
  }

  private var permissionList: some View {
    VStack(alignment: .leading, spacing: 8) {
      Text(HumanCopy.FullDiskAccess.systemSettingsLabel)
        .trawlText(.sectionHeader)
      Text(HumanCopy.FullDiskAccess.addAppStep)
        .trawlText(.body)
        .foregroundStyle(.secondary)
    }
    .frame(width: 230, alignment: .leading)
  }
}

private struct PermissionStatus: View {
  let state: PermissionCheckState

  var body: some View {
    HStack(alignment: .top, spacing: 10) {
      Image(systemName: symbol)
        .foregroundStyle(colour)
        .frame(width: 18)
        .accessibilityHidden(true)
      VStack(alignment: .leading, spacing: 5) {
        Text(label)
          .trawlText(.body)
        if state == .notConfirmed {
          Text(OperationalCopy.FullDiskAccess.instruction)
            .trawlText(.meta)
            .foregroundStyle(.secondary)
        }
      }
    }
  }

  private var label: String {
    switch state {
    case .idle: OperationalCopy.FullDiskAccess.idle
    case .checking: OperationalCopy.FullDiskAccess.checking
    case .confirmed: OperationalCopy.FullDiskAccess.confirmed
    case .notConfirmed: OperationalCopy.FullDiskAccess.notConfirmed
    }
  }

  private var symbol: String {
    switch state {
    case .idle: "lock.open"
    case .checking: "arrow.trianglehead.2.clockwise.rotate.90"
    case .confirmed: "checkmark.circle.fill"
    case .notConfirmed: "exclamationmark.triangle"
    }
  }

  private var colour: Color {
    switch state {
    case .confirmed: .green
    case .notConfirmed: .orange
    case .idle, .checking: .secondary
    }
  }
}

private struct TrustReview: View {
  let buildIdentity: BuildIdentity
  @Binding var copiedAuditPrompt: Bool

  var body: some View {
    VStack(alignment: .leading, spacing: 12) {
      Text(HumanCopy.FullDiskAccess.trustGroupBody)
        .trawlText(.body)
        .lineSpacing(2)
        .foregroundStyle(.secondary)
        .fixedSize(horizontal: false, vertical: true)
      HStack(spacing: 10) {
        Button {
          NSWorkspace.shared.open(BuildIdentity.repositoryURL)
        } label: {
          Label(
            HumanCopy.FullDiskAccess.readCodeAction,
            systemImage: "arrow.up.right.square"
          )
        }
        Button {
          NSPasteboard.general.clearContents()
          NSPasteboard.general.setString(
            AgentPrompts.auditBuild(buildIdentity),
            forType: .string
          )
          copiedAuditPrompt = true
        } label: {
          Label(
            copiedAuditPrompt
              ? HumanCopy.FullDiskAccess.copiedAuditPromptAction
              : HumanCopy.FullDiskAccess.copyAuditPromptAction,
            systemImage: copiedAuditPrompt ? "checkmark" : "doc.on.doc"
          )
        }
        .disabled(copiedAuditPrompt)
      }
      .controlSize(.small)
      .buttonStyle(.bordered)
      .buttonBorderShape(.capsule)
      .tint(.primary)
    }
  }
}

struct BuildStep: View {
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let aiInstruction: String
  let hasCopiedAIInstructions: Bool
  let onCopyAIInstructions: () -> Void
  let onBack: () -> Void
  let onRetryApp: (RegisteredTrawlerIdentity) -> Void
  let onRetryInitialLoad: () -> Void
  let onPermissionRecovery: () -> Void
  let onStop: () -> Void
  let onFinish: () -> Void

  private var canFinishSetup: Bool {
    !appModel.isUpdating
      && !hasGlobalPermissionFailure
      && (hasSearchableArchive || hasNoAvailableApps)
  }

  private var hasSearchableArchive: Bool {
    appModel.trawlerStatuses.contains { trawlerStatus in
      trawlerStatus.archiveContentCountsAfterLastSuccessfullyCompletedUpdate.contains {
        $0.archiveContentCount > 0
      }
    }
      || appModel.trawlerArchiveUpdateResults.contains { trawlerArchiveUpdateResult in
        !appModel.updateOperationFailures.contains {
          $0.failedTrawler == trawlerArchiveUpdateResult.registeredTrawler
        }
      }
  }

  private var hasNoAvailableApps: Bool {
    let activeTrawlers =
      appModel.trawlerStatuses.map(\.id)
      + appModel.statusOperationFailures.map(\.failedTrawler)
    let statusIsSettled =
      appModel.phase == .ready
      || (appModel.phase == .partial && appModel.statusOperationFailures.isEmpty)
    return statusIsSettled
      && activeTrawlers.allSatisfy { !appInstallations.isAvailable($0) }
  }

  private var hasGlobalPermissionFailure: Bool {
    appModel.needsFullDiskAccessRecovery
  }

  private var archiveIsReady: Bool {
    canFinishSetup && !appModel.isUpdating
  }

  var body: some View {
    TrawlFlowScaffold(page: .archive) {
      VStack(alignment: .leading, spacing: TrawlDesign.onboardingSectionSpacing) {
        OnboardingProse(
          title: archiveIsReady
            ? HumanCopy.ArchiveBuild.readyTitle
            : HumanCopy.ArchiveBuild.title,
          lede: archiveIsReady
            ? HumanCopy.ArchiveBuild.readyBody
            : HumanCopy.ArchiveBuild.body
        )
        .frame(height: 100, alignment: .topLeading)
        AIConnectionPanel(
          hasCopied: hasCopiedAIInstructions,
          onCopy: copyAIInstructions
        )
        ArchiveBuildStatus(
          appModel: appModel,
          appInstallations: appInstallations,
          comingSoonEntries: comingSoonEntries,
          hasGlobalPermissionFailure: hasGlobalPermissionFailure,
          onRetryApp: onRetryApp,
          onRetryInitialLoad: onRetryInitialLoad,
          onPermissionRecovery: onPermissionRecovery
        )
      }
    } actions: {
      OnboardingActionRow(
        backAction: onBack,
        secondaryTitle: appModel.isUpdating ? OperationalCopy.SharedAction.cancel : nil,
        secondaryAction: appModel.isUpdating ? onStop : nil,
        primaryTitle: HumanCopy.ArchiveBuild.startSearchingAction,
        primaryAction: onFinish,
        primaryDisabled: !canFinishSetup
      )
    }
  }

  private func copyAIInstructions() {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(aiInstruction, forType: .string)
    onCopyAIInstructions()
  }

  private var comingSoonEntries: [RegisteredTrawlerCatalogEntry] {
    appModel.registeredTrawlerCatalog.filter {
      $0.registeredTrawlerReleaseState == .comingSoon
    }
  }
}

private struct ArchiveBuildStatus: View {
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let comingSoonEntries: [RegisteredTrawlerCatalogEntry]
  let hasGlobalPermissionFailure: Bool
  let onRetryApp: (RegisteredTrawlerIdentity) -> Void
  let onRetryInitialLoad: () -> Void
  let onPermissionRecovery: () -> Void

  var body: some View {
    if hasGlobalPermissionFailure {
      PermissionRecoveryBanner(action: onPermissionRecovery)
    } else if appModel.blockingFailureMessage != nil, appModel.displayedTrawlers.isEmpty {
      InitialLoadRecovery(action: onRetryInitialLoad)
    } else {
      ArchiveTrawlerSummary(
        appModel: appModel,
        appInstallations: appInstallations,
        comingSoonEntries: comingSoonEntries,
        onRetryApp: onRetryApp
      )
    }
  }
}

private struct ArchiveTrawlerSummary: View {
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let comingSoonEntries: [RegisteredTrawlerCatalogEntry]
  let onRetryApp: (RegisteredTrawlerIdentity) -> Void

  private var availableTrawlers: [RegisteredTrawlerIdentity] {
    appModel.displayedTrawlers.filter {
      appModel.catalogEntry(for: $0)?.registeredTrawlerReleaseState != .comingSoon
    }
  }

  private var presentations: [(RegisteredTrawlerIdentity, AppBuildRowPresentation)] {
    availableTrawlers.map { ($0, presentation(for: $0)) }
  }

  private var searchableCount: Int {
    presentations.count { $0.1.status == .success || $0.1.status == .warning }
  }

  var body: some View {
    VStack(alignment: .leading, spacing: 10) {
      HStack(alignment: .firstTextBaseline) {
        Text(HumanCopy.ArchiveBuild.yourAppsTitle)
          .trawlText(.sectionHeader)
        Spacer()
        ArchiveProgressSummary(
          searchableCount: searchableCount,
          totalCount: presentations.count
        )
      }
      if !availableTrawlers.isEmpty {
        AppBuildList(
          registeredTrawlers: availableTrawlers,
          comingSoonEntries: [],
          appModel: appModel,
          appInstallations: appInstallations,
          suppressPermissionFailures: false,
          onRetryApp: onRetryApp
        )
      }
      if !comingSoonEntries.isEmpty {
        Text(HumanCopy.ArchiveBuild.moreAppsTitle)
          .trawlText(.sectionHeader)
          .padding(.top, TrawlDesign.onboardingSubgroupSpacing)
        AppBuildList(
          registeredTrawlers: [],
          comingSoonEntries: comingSoonEntries,
          appModel: appModel,
          appInstallations: appInstallations,
          suppressPermissionFailures: false,
          onRetryApp: onRetryApp
        )
      }
    }
  }

  private func presentation(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> AppBuildRowPresentation {
    let trawlerStatus = appModel.trawlerStatuses.first {
      $0.id == registeredTrawler
    }
    let updateFailure = appModel.updateOperationFailures.first {
      $0.failedTrawler == registeredTrawler
    }
    let failure = updateFailure
      ?? appModel.statusOperationFailures.first {
        $0.failedTrawler == registeredTrawler
      }
    let skipped = appModel.trawlersSkippedFromStatus.first {
      $0.skippedTrawler == registeredTrawler
    }
    let catalogEntry = appModel.catalogEntry(for: registeredTrawler)
    return AppBuildRowPresentation.resolve(
      name: catalogEntry?.registeredTrawlerManifest.registeredTrawlerDisplayName
        ?? trawlerStatus?.registeredTrawlerManifest.registeredTrawlerDisplayName
        ?? failure?.registeredTrawlerDisplayName
        ?? skipped?.registeredTrawlerDisplayName
        ?? registeredTrawler.registeredTrawlerIdentity,
      counts:
        trawlerStatus?.archiveContentCountsAfterLastSuccessfullyCompletedUpdate ?? [],
      progress: appModel.updateProgress[registeredTrawler],
      failure: failure,
      archiveUpdateFailed: updateFailure != nil,
      skipped: skipped,
      releaseState: catalogEntry?.registeredTrawlerReleaseState,
      isInstalled: appInstallations.isAvailable(registeredTrawler),
      suppressPermissionFailure: false
    )
  }
}

private struct ArchiveProgressSummary: View {
  let searchableCount: Int
  let totalCount: Int

  var body: some View {
    Text(summary)
      .trawlText(.meta)
      .foregroundStyle(.secondary)
  }

  private var summary: String {
    String(format: HumanCopy.ArchiveBuild.progressFormat, searchableCount, totalCount)
  }
}

private struct InitialLoadRecovery: View {
  let action: () -> Void

  var body: some View {
    ContentUnavailableView {
      Label(OperationalCopy.AppStatus.appsUnavailable, systemImage: "exclamationmark.triangle")
    } description: {
      Text(OperationalCopy.AppStatus.statusCheckFailed)
      Text(OperationalCopy.AppStatus.statusCheckRecovery)
    } actions: {
      Button(OperationalCopy.SharedAction.retry, action: action)
        .buttonStyle(.borderedProminent)
    }
  }
}

private struct AIConnectionPanel: View {
  let hasCopied: Bool
  let onCopy: () -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      Text(HumanCopy.ConnectAI.title)
        .trawlText(.sectionHeader)
      Text(HumanCopy.ConnectAI.body)
        .trawlText(.body)
        .foregroundStyle(.secondary)
        .fixedSize(horizontal: false, vertical: true)
      copyButton
    }
  }

  private var copyButton: some View {
    Button(action: onCopy) {
      Label(
        HumanCopy.ConnectAI.copyAction,
        systemImage: hasCopied ? "checkmark" : "doc.on.doc"
      )
    }
    .buttonStyle(.bordered)
    .buttonBorderShape(.capsule)
    .controlSize(.small)
    .disabled(hasCopied)
    .tint(.primary)
  }
}

struct PermissionRecoveryBanner: View {
  let action: () -> Void

  var body: some View {
    HStack(spacing: 12) {
      Image(systemName: "lock.trianglebadge.exclamationmark")
        .foregroundStyle(.orange)
      VStack(alignment: .leading, spacing: 3) {
        Text(OperationalCopy.FullDiskAccess.needed)
          .trawlText(.sectionHeader)
        Text(OperationalCopy.FullDiskAccess.instruction)
          .trawlText(.body)
          .foregroundStyle(.secondary)
      }
      Spacer()
      Button(OperationalCopy.FullDiskAccess.open, action: action)
    }
    .padding(16)
    .background(.orange.opacity(0.08), in: RoundedRectangle(cornerRadius: 10))
  }
}

private struct AppBuildList: View {
  let registeredTrawlers: [RegisteredTrawlerIdentity]
  let comingSoonEntries: [RegisteredTrawlerCatalogEntry]
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let suppressPermissionFailures: Bool
  let onRetryApp: (RegisteredTrawlerIdentity) -> Void

  var body: some View {
    VStack(spacing: 0) {
      ForEach(registeredTrawlers, id: \.self) { registeredTrawler in
        let presentation = presentation(for: registeredTrawler)
        ArchiveAppRow(
          registeredTrawler: registeredTrawler,
          name: presentation.name,
          status: presentation.status,
          statusLabel: presentation.statusLabel,
          recoveryTitle: presentation.canRetry ? HumanCopy.AppStatus.retry : nil,
          recovery: presentation.canRetry ? { onRetryApp(registeredTrawler) } : nil,
          recoveryDisabled: appModel.isUpdating
        )
        Divider()
      }
      ForEach(comingSoonEntries, id: \.id) { entry in
        ArchiveAppRow(
          registeredTrawler: entry.id,
          name: entry.registeredTrawlerManifest.registeredTrawlerDisplayName,
          status: .neutral,
          statusLabel: HumanCopy.AppStatus.comingSoon,
          accessibilityStatus: HumanCopy.AppStatus.comingSoon,
          symbolOverride: "clock",
          recoveryTitle: nil,
          recovery: nil,
          recoveryDisabled: true
        )
        Divider()
      }
    }
  }

  private func presentation(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> AppBuildRowPresentation {
    let trawlerStatus = appModel.trawlerStatuses.first {
      $0.id == registeredTrawler
    }
    let updateFailure = appModel.updateOperationFailures.first {
      $0.failedTrawler == registeredTrawler
    }
    let failure = updateFailure
      ?? appModel.statusOperationFailures.first {
        $0.failedTrawler == registeredTrawler
      }
    let skipped = appModel.trawlersSkippedFromStatus.first {
      $0.skippedTrawler == registeredTrawler
    }
    let catalogEntry = appModel.catalogEntry(for: registeredTrawler)
    return AppBuildRowPresentation.resolve(
      name: catalogEntry?.registeredTrawlerManifest.registeredTrawlerDisplayName
        ?? trawlerStatus?.registeredTrawlerManifest.registeredTrawlerDisplayName
        ?? failure?.registeredTrawlerDisplayName
        ?? skipped?.registeredTrawlerDisplayName
        ?? registeredTrawler.registeredTrawlerIdentity,
      counts:
        trawlerStatus?.archiveContentCountsAfterLastSuccessfullyCompletedUpdate ?? [],
      progress: appModel.updateProgress[registeredTrawler],
      failure: failure,
      archiveUpdateFailed: updateFailure != nil,
      skipped: skipped,
      releaseState: catalogEntry?.registeredTrawlerReleaseState,
      isInstalled: appInstallations.isAvailable(registeredTrawler),
      suppressPermissionFailure: suppressPermissionFailures
    )
  }
}

private struct ArchiveAppRow: View {
  let registeredTrawler: RegisteredTrawlerIdentity
  let name: String
  let status: TrawlStatus
  let statusLabel: String?
  var accessibilityStatus: String? = nil
  var symbolOverride: String? = nil
  let recoveryTitle: String?
  let recovery: (() -> Void)?
  let recoveryDisabled: Bool

  var body: some View {
    HStack(spacing: 8) {
      TrawlerIconView(
        registeredTrawler: registeredTrawler,
        size: 22)
      Text(name)
        .trawlText(.body)
      Spacer(minLength: 12)
      if status == .working {
        Image(systemName: "arrow.trianglehead.2.clockwise.rotate.90")
          .foregroundStyle(.secondary)
          .accessibilityHidden(true)
      } else if statusLabel != nil, status != .neutral || symbolOverride != nil {
        Image(systemName: symbolOverride ?? status.symbol)
          .foregroundStyle(status.colour)
          .accessibilityHidden(true)
      }
      if let statusLabel {
        Text(statusLabel)
          .trawlText(.meta)
          .foregroundStyle(status == .success ? Color.secondary : status.colour)
      }
      if let recoveryTitle, let recovery {
        Button(recoveryTitle, action: recovery)
          .controlSize(.small)
          .disabled(recoveryDisabled)
      }
    }
    .frame(
      maxWidth: .infinity,
      minHeight: TrawlDesign.onboardingRowHeight,
      alignment: .leading
    )
    .accessibilityElement(children: .combine)
    .accessibilityLabel(name)
    .accessibilityValue(accessibilityStatus ?? statusLabel ?? "")
  }
}

struct AppBuildRowPresentation: Equatable {
  let name: String
  let status: TrawlStatus
  let statusLabel: String
  let canRetry: Bool

  static func resolve(
    name: String,
    counts: [ArchiveContentCountAfterLastSuccessfullyCompletedUpdate],
    progress: TrawlerArchiveUpdateProgressState?,
    failure: TrawlerOperationFailure?,
    archiveUpdateFailed: Bool,
    skipped: TrawlerSkippedFromOperation?,
    releaseState: RegisteredTrawlerReleaseState? = nil,
    isInstalled: Bool,
    suppressPermissionFailure: Bool
  ) -> AppBuildRowPresentation {
    if releaseState == .comingSoon {
      return AppBuildRowPresentation(
        name: name, status: .neutral,
        statusLabel: HumanCopy.AppStatus.comingSoon, canRetry: false
      )
    }
    if skipped != nil {
      return AppBuildRowPresentation(
        name: name, status: .neutral,
        statusLabel: OperationalCopy.AppStatus.notAvailable, canRetry: false
      )
    }
    guard isInstalled else {
      return AppBuildRowPresentation(
        name: name, status: .neutral,
        statusLabel: HumanCopy.AppStatus.notInstalled, canRetry: false
      )
    }
    if suppressPermissionFailure, failure?.failureCode == .permission {
      return AppBuildRowPresentation(
        name: name, status: .neutral,
        statusLabel: OperationalCopy.AppStatus.waiting, canRetry: false
      )
    }
    if case .building = progress {
      return AppBuildRowPresentation(
        name: name, status: .working,
        statusLabel: OperationalCopy.AppStatus.building, canRetry: false
      )
    }
    if case .finalising = progress {
      return AppBuildRowPresentation(
        name: name, status: .working,
        statusLabel: OperationalCopy.AppStatus.finalising, canRetry: false
      )
    }
    if let failure {
      let hasArchive = counts.contains { $0.archiveContentCount > 0 }
      if hasArchive {
        return AppBuildRowPresentation(
          name: name,
          status: .success,
          statusLabel: archiveUpdateFailed
            ? OperationalCopy.AppStatus.searchableWithFailedUpdate
            : OperationalCopy.AppStatus.searchable,
          canRetry: false
        )
      }
      return AppBuildRowPresentation(
        name: name,
        status: .failure,
        statusLabel: archiveUpdateFailed
          ? OperationalCopy.AppStatus.updateFailed
          : OperationalCopy.AppStatus.notSearchable,
        canRetry: failure.failureCode != .authentication
          && failure.failureCode != .invalidInput
      )
    }
    if case .failed = progress {
      let hasArchive = counts.contains { $0.archiveContentCount > 0 }
      return AppBuildRowPresentation(
        name: name,
        status: hasArchive ? .success : .failure,
        statusLabel: hasArchive
          ? OperationalCopy.AppStatus.searchableWithFailedUpdate
          : OperationalCopy.AppStatus.updateFailed,
        canRetry: true
      )
    }
    if case .finished = progress {
      return AppBuildRowPresentation(
        name: name, status: .success,
        statusLabel: OperationalCopy.AppStatus.searchable,
        canRetry: false
      )
    }
    if counts.contains(where: { $0.archiveContentCount > 0 }) {
      return AppBuildRowPresentation(
        name: name, status: .success,
        statusLabel: OperationalCopy.AppStatus.searchable, canRetry: false
      )
    }
    return AppBuildRowPresentation(
      name: name, status: .neutral,
      statusLabel: OperationalCopy.AppStatus.waiting, canRetry: false
    )
  }
}
