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
      WelcomeStep {
        onboarding.showPermission(appModel: appModel)
      }
    case .permission:
      PermissionStep(
        permissionCheck: onboarding.permissionCheck,
        buildIdentity: buildIdentity,
        onBack: onboarding.showWelcome,
        onOpenSettings: {
          onboarding.requestPermission(appModel: appModel) {
            refreshedTrawlersToSync()
          }
        },
        onCheckAgain: {
          onboarding.checkPermission(
            appModel: appModel, registeredTrawlers: refreshedTrawlersToSync)
        },
        onContinue: {
          onboarding.continueWithVerifiedAccess(
            appModel: appModel,
            registeredTrawlers: refreshedTrawlersToSync
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
            refreshedTrawlersToSync()
          }
        },
        onPermissionRecovery: {
          onboarding.reopenPermissionRecovery(appModel: appModel) {
            refreshedTrawlersToSync()
          }
        },
        onStop: onboarding.stopSync,
        onFinish: onboarding.showCommandDemo
      )
      .task(id: reportedTrawlers) {
        onboarding.resumeInitialSyncIfNeeded(
          appModel: appModel,
          registeredTrawlers: refreshedTrawlersToSync()
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

  private func refreshedTrawlersToSync() -> [RegisteredTrawlerIdentity] {
    appInstallations.refresh(
      registeredTrawlerCatalog: appModel.registeredTrawlerCatalog
    )
    return flags.trawlersToSync(
      reportedTrawlers: appModel.syncCandidateTrawlers,
      unavailableTrawlers: appInstallations.unavailableTrawlers
    )
  }

}

struct WelcomeStep: View {
  var icon = NSApplication.shared.applicationIconImage
  let onContinue: () -> Void

  var body: some View {
    TrawlFlowScaffold(
      page: .welcome,
      composition: .centred,
      contentWidth: TrawlDesign.onboardingPageWidth
    ) {
      OnboardingHeroLayout {
        VStack(spacing: 24) {
          WelcomeMark(icon: icon)
          OnboardingProse(
            title: DraftCopy.Welcome.title,
            lede: DraftCopy.Welcome.body,
            statement: DraftCopy.Welcome.privacy,
            centred: true
          )
        }
      }
    } actions: {
      OnboardingActionRow(
        backAction: nil,
        secondaryTitle: nil,
        secondaryAction: nil,
        primaryTitle: DraftCopy.Welcome.primaryAction,
        primaryAction: onContinue
      )
    }
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
          title: DraftCopy.FullDiskAccess.title,
          lede: DraftCopy.FullDiskAccess.body,
          statement: DraftCopy.FullDiskAccess.purpose
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
        OnboardingInformationGroup(title: DraftCopy.FullDiskAccess.trustGroupTitle) {
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
          : OperationalCopy.FullDiskAccess.open,
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
    .accessibilityLabel(DraftCopy.FullDiskAccess.dragAccessibilityLabel)
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
        accessibilityDescription: OperationalCopy.FullDiskAccess.openTrawl
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
      Text(OperationalCopy.FullDiskAccess.systemSettings)
        .trawlText(.sectionHeader)
      Text(OperationalCopy.FullDiskAccess.addAppStep)
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
          Text(OperationalCopy.FullDiskAccess.recovery)
            .trawlText(.meta)
            .foregroundStyle(.secondary)
        }
      }
    }
  }

  private var label: String {
    switch state {
    case .idle: "Full Disk Access is not confirmed yet"
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
      Text(DraftCopy.FullDiskAccess.trustGroupBody)
        .trawlText(.body)
        .lineSpacing(2)
        .foregroundStyle(.secondary)
        .fixedSize(horizontal: false, vertical: true)
      HStack(spacing: 10) {
        Button {
          NSWorkspace.shared.open(BuildIdentity.repositoryURL)
        } label: {
          Label(
            DraftCopy.FullDiskAccess.readCodeAction,
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
              ? OperationalCopy.Trust.copiedAuditPrompt
              : OperationalCopy.Trust.copyAuditPrompt,
            systemImage: "doc.on.doc"
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
    !appModel.isSyncing
      && !hasGlobalPermissionFailure
      && (hasSearchableArchive || hasNoAvailableApps)
  }

  private var hasSearchableArchive: Bool {
    appModel.trawlerStatuses.contains { trawlerStatus in
      trawlerStatus.archiveContentCountsAfterLastSuccessfullyCompletedSync.contains {
        $0.archiveContentCount > 0
      }
    }
      || appModel.trawlerArchiveSyncResults.contains { trawlerArchiveSyncResult in
        !appModel.syncOperationFailures.contains {
          $0.failedTrawler == trawlerArchiveSyncResult.registeredTrawler
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
    canFinishSetup && !appModel.isSyncing
  }

  var body: some View {
    TrawlFlowScaffold(page: .archive) {
      VStack(alignment: .leading, spacing: TrawlDesign.onboardingSectionSpacing) {
        OnboardingProse(
          title: archiveIsReady
            ? DraftCopy.ArchiveBuild.readyTitle
            : DraftCopy.ArchiveBuild.title,
          lede: archiveIsReady
            ? DraftCopy.ArchiveBuild.readyBody
            : DraftCopy.ArchiveBuild.body
        )
        AIConnectionPanel(
          hasCopied: hasCopiedAIInstructions,
          isPrimary: appModel.isSyncing && !hasSearchableArchive,
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
        secondaryTitle: appModel.isSyncing ? OperationalCopy.SharedAction.cancel : nil,
        secondaryAction: appModel.isSyncing ? onStop : nil,
        primaryTitle: OperationalCopy.SharedAction.continueAction,
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

  private var workingCount: Int {
    presentations.count { $0.1.status == .working }
  }

  private var settledCount: Int {
    presentations.count {
      $0.1.status == .success || $0.1.status == .warning || $0.1.status == .failure
    }
  }

  var body: some View {
    VStack(alignment: .leading, spacing: 10) {
      HStack(alignment: .firstTextBaseline) {
        Text(OperationalCopy.ArchiveBuild.yourApps)
          .trawlText(.sectionHeader)
        Spacer()
        ArchiveProgressSummary(
          searchableCount: searchableCount,
          workingCount: workingCount,
          settledCount: settledCount,
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
        Text(OperationalCopy.ArchiveBuild.moreApps)
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
    let failure =
      appModel.syncOperationFailures.first {
        $0.failedTrawler == registeredTrawler
      }
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
        trawlerStatus?.archiveContentCountsAfterLastSuccessfullyCompletedSync ?? [],
      progress: appModel.syncProgress[registeredTrawler],
      failure: failure,
      skipped: skipped,
      releaseState: catalogEntry?.registeredTrawlerReleaseState,
      isInstalled: appInstallations.isAvailable(registeredTrawler),
      suppressPermissionFailure: false
    )
  }
}

private struct ArchiveProgressSummary: View {
  let searchableCount: Int
  let workingCount: Int
  let settledCount: Int
  let totalCount: Int

  var body: some View {
    Text(summary)
      .trawlText(.meta)
      .foregroundStyle(.secondary)
  }

  private var summary: String {
    if workingCount > 0 {
      return "\(searchableCount) searchable · \(workingCount) building"
    }
    if searchableCount == totalCount {
      return "\(searchableCount) searchable"
    }
    return "\(searchableCount) of \(totalCount) searchable"
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
  let isPrimary: Bool
  let onCopy: () -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      Text(DraftCopy.ConnectAI.title)
        .trawlText(.sectionHeader)
      Text(DraftCopy.ConnectAI.body)
        .trawlText(.body)
        .foregroundStyle(.secondary)
        .fixedSize(horizontal: false, vertical: true)
      copyButton
    }
  }

  @ViewBuilder
  private var copyButton: some View {
    if isPrimary {
      button
        .buttonStyle(.borderedProminent)
        .tint(TrawlDesign.brandRed)
    } else {
      button
        .buttonStyle(.bordered)
        .tint(.primary)
    }
  }

  private var button: some View {
    Button(action: onCopy) {
      Label(
        hasCopied
          ? OperationalCopy.ArchiveBuild.copiedAIInstructions
          : OperationalCopy.ArchiveBuild.copyAIInstructions,
        systemImage: "doc.on.doc"
      )
    }
    .buttonBorderShape(.capsule)
    .controlSize(.small)
    .disabled(hasCopied)
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
        Text(OperationalCopy.FullDiskAccess.recovery)
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
          recoveryTitle: presentation.canRetry ? OperationalCopy.AppStatus.retryApp : nil,
          recovery: presentation.canRetry ? { onRetryApp(registeredTrawler) } : nil,
          recoveryDisabled: appModel.isSyncing
        )
        Divider()
      }
      ForEach(comingSoonEntries, id: \.id) { entry in
        ArchiveAppRow(
          registeredTrawler: entry.id,
          name: entry.registeredTrawlerManifest.registeredTrawlerDisplayName,
          status: .neutral,
          statusLabel: OperationalCopy.AppStatus.comingSoon,
          accessibilityStatus: OperationalCopy.AppStatus.comingSoon,
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
    let failure =
      appModel.syncOperationFailures.first {
        $0.failedTrawler == registeredTrawler
      }
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
        trawlerStatus?.archiveContentCountsAfterLastSuccessfullyCompletedSync ?? [],
      progress: appModel.syncProgress[registeredTrawler],
      failure: failure,
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
    counts: [ArchiveContentCountAfterLastSuccessfullyCompletedSync],
    progress: AppSyncProgressState?,
    failure: TrawlerOperationFailure?,
    skipped: TrawlerSkippedFromOperation?,
    releaseState: RegisteredTrawlerReleaseState? = nil,
    isInstalled: Bool,
    suppressPermissionFailure: Bool
  ) -> AppBuildRowPresentation {
    if releaseState == .comingSoon || skipped != nil {
      return AppBuildRowPresentation(
        name: name, status: .neutral,
        statusLabel: OperationalCopy.AppStatus.comingSoon, canRetry: false
      )
    }
    guard isInstalled else {
      return AppBuildRowPresentation(
        name: name, status: .neutral,
        statusLabel: OperationalCopy.AppStatus.notInstalled, canRetry: false
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
          statusLabel: OperationalCopy.AppStatus.searchable,
          canRetry: false
        )
      }
      return AppBuildRowPresentation(
        name: name,
        status: .failure,
        statusLabel: OperationalCopy.AppStatus.failed,
        canRetry: failure.failureCode != .authentication
          && failure.failureCode != .invalidInput
      )
    }
    if case .failed = progress {
      return AppBuildRowPresentation(
        name: name,
        status: .failure,
        statusLabel: OperationalCopy.AppStatus.failed,
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
