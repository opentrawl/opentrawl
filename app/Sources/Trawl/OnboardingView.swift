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
      WelcomeStep(onContinue: onboarding.showPermission)
    case .permission:
      PermissionStep(
        permissionCheck: onboarding.permissionCheck,
        buildIdentity: buildIdentity,
        onBack: onboarding.showWelcome,
        onOpenSettings: {
          onboarding.requestPermission(appModel: appModel) { refreshedSyncAppIDs() }
        },
        onCheckAgain: {
          onboarding.checkPermission(appModel: appModel, appIDs: refreshedSyncAppIDs())
        }
      )
    case .building:
      BuildStep(
        appModel: appModel,
        appInstallations: appInstallations,
        aiInstruction: aiInstruction,
        hasCopiedAIInstructions: onboarding.hasCopiedAIInstructions,
        onCopyAIInstructions: onboarding.didCopyAIInstructions,
        onRetryApp: { appID in onboarding.retry(appModel: appModel, appID: appID) },
        onRetryInitialLoad: {
          onboarding.retryInitialLoad(appModel: appModel) { refreshedSyncAppIDs() }
        },
        onPermissionRecovery: {
          onboarding.reopenPermissionRecovery(appModel: appModel) { refreshedSyncAppIDs() }
        },
        onStop: onboarding.stopSync,
        onFinish: onFinish
      )
      .task(id: reportedAppIDs) {
        onboarding.resumeInitialSyncIfNeeded(
          appModel: appModel,
          appIDs: refreshedSyncAppIDs()
        )
      }
    case .complete:
      EmptyView()
    }
  }

  private var reportedAppIDs: [String] {
    (appModel.sources.map(\.id)
      + appModel.statusFailures.map(\.sourceID))
      .reduce(into: []) { appIDs, appID in
        if !appIDs.contains(appID) { appIDs.append(appID) }
      }
  }

  private func refreshedSyncAppIDs() -> [String] {
    appInstallations.refresh(manifests: appModel.sources.map(\.manifest))
    return flags.syncAppIDs(
      reportedAppIDs: reportedAppIDs,
      unavailableAppIDs: appInstallations.unavailableAppIDs
    )
  }

}

private struct WelcomeStep: View {
  let onContinue: () -> Void

  var body: some View {
    TrawlFlowScaffold(step: HumanCopy.welcomeStep) {
      VStack(alignment: .leading, spacing: 24) {
        HStack(alignment: .top, spacing: 40) {
          VStack(alignment: .leading, spacing: 18) {
            Text(HumanCopy.welcomeTitle)
              .font(.largeTitle.bold())
            Text(HumanCopy.welcomeBody)
              .font(.title3)
              .foregroundStyle(.secondary)
            Text(HumanCopy.archiveLocation)
              .font(.callout)
              .foregroundStyle(.secondary)
          }
          Spacer(minLength: 20)
          Image(nsImage: NSApplication.shared.applicationIconImage)
            .resizable()
            .scaledToFit()
            .frame(width: 112, height: 112)
            .accessibilityHidden(true)
        }
        WelcomeFacts()
      }
    } footer: {
      TrawlActionBar(
        backAction: nil,
        secondaryTitle: nil,
        secondaryAction: nil,
        primaryTitle: HumanCopy.start,
        primaryAction: onContinue
      )
    }
  }
}

private struct WelcomeFacts: View {
  var body: some View {
    HStack(alignment: .top, spacing: 0) {
      WelcomeFact(number: "01", text: HumanCopy.archiveStaysLocal)
      Divider()
      WelcomeFact(number: "02", text: HumanCopy.originalsStayUntouched)
      Divider()
      WelcomeFact(number: "03", text: HumanCopy.openSource)
    }
    .fixedSize(horizontal: false, vertical: true)
    .overlay(alignment: .top) { Rectangle().frame(height: 2) }
  }
}

private struct WelcomeFact: View {
  let number: String
  let text: String

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      Text(number)
        .font(.caption.bold())
        .foregroundStyle(TrawlDesign.brandRed)
      Text(text)
        .font(.body.weight(.semibold))
        .fixedSize(horizontal: false, vertical: true)
    }
    .frame(maxWidth: .infinity, alignment: .leading)
    .padding(.horizontal, 16)
    .padding(.vertical, 18)
  }
}

private struct PermissionStep: View {
  @State private var copiedAuditPrompt = false

  let permissionCheck: PermissionCheckState
  let buildIdentity: BuildIdentity
  let onBack: () -> Void
  let onOpenSettings: () -> Void
  let onCheckAgain: () -> Void

  var body: some View {
    TrawlFlowScaffold(step: HumanCopy.permissionStep) {
      VStack(alignment: .leading, spacing: 24) {
        Text(HumanCopy.permissionTitle)
          .font(.largeTitle.bold())
        Text(HumanCopy.permissionBody)
          .font(.title3)
          .foregroundStyle(.secondary)
        PermissionTrustFacts()
        FullDiskAccessDragGuide()
        VStack(alignment: .leading, spacing: 8) {
          Label(permissionStatus, systemImage: permissionSymbol)
            .foregroundStyle(permissionColour)
          if permissionCheck == .notConfirmed {
            Text(OperationalCopy.accessRecovery)
              .foregroundStyle(.secondary)
          }
        }
        DisclosureGroup(OperationalCopy.reviewBuild) {
          VStack(alignment: .leading, spacing: 12) {
            if let sourceURL = buildIdentity.sourceURL {
              Link(
                "\(buildIdentity.version) · \(buildIdentity.shortCommit)", destination: sourceURL)
            }
            Button(
              copiedAuditPrompt
                ? OperationalCopy.copiedAuditPrompt : OperationalCopy.copyAuditPrompt
            ) {
              NSPasteboard.general.clearContents()
              NSPasteboard.general.setString(
                AgentPrompts.auditBuild(buildIdentity),
                forType: .string
              )
              copiedAuditPrompt = true
            }
          }
          .padding(.top, 8)
        }
      }
    } footer: {
      TrawlActionBar(
        backAction: onBack,
        secondaryTitle: permissionCheck == .notConfirmed
          ? OperationalCopy.checkAccessAgain : nil,
        secondaryAction: permissionCheck == .notConfirmed ? onCheckAgain : nil,
        primaryTitle: OperationalCopy.openFullDiskAccess,
        primaryAction: onOpenSettings
      )
    }
  }

  private var permissionStatus: String {
    switch permissionCheck {
    case .idle: OperationalCopy.waitingForAccess
    case .checking: OperationalCopy.checkingAccess
    case .notConfirmed: OperationalCopy.accessNotConfirmed
    }
  }

  private var permissionSymbol: String {
    switch permissionCheck {
    case .idle: "lock"
    case .checking: "arrow.trianglehead.2.clockwise.rotate.90"
    case .notConfirmed: "exclamationmark.triangle"
    }
  }

  private var permissionColour: Color {
    permissionCheck == .notConfirmed ? .orange : .secondary
  }
}

private struct PermissionTrustFacts: View {
  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      Label(HumanCopy.archiveStaysLocal, systemImage: "internaldrive")
      Label(HumanCopy.originalsStayUntouched, systemImage: "hand.raised")
      Label(HumanCopy.openSource, systemImage: "chevron.left.forwardslash.chevron.right")
    }
    .font(.body.weight(.medium))
  }
}

private struct DraggableOpenTrawlIcon: View {
  @State private var isHovering = false

  private let bundleURL = Bundle.main.bundleURL
  private let icon = NSWorkspace.shared.icon(forFile: Bundle.main.bundleURL.path)

  var body: some View {
    Image(nsImage: icon)
      .resizable()
      .scaledToFit()
      .frame(width: 76, height: 76)
      .padding(12)
      .background(
        isHovering ? TrawlDesign.brandRed.opacity(0.08) : Color.secondary.opacity(0.08)
      )
      .overlay {
        Rectangle().stroke(
          isHovering ? TrawlDesign.brandRed : Color(nsColor: .separatorColor),
          lineWidth: 1
        )
      }
      .onDrag {
        NSItemProvider(object: bundleURL as NSURL)
      } preview: {
        Image(nsImage: icon).resizable().frame(width: 64, height: 64)
      }
      .onHover { isHovering = $0 }
      .accessibilityLabel(HumanCopy.permissionDragAccessibilityLabel)
  }
}

private struct FullDiskAccessDragGuide: View {
  var body: some View {
    HStack(spacing: 22) {
      DraggableOpenTrawlIcon()
      Image(systemName: "arrow.right")
        .font(.title.bold())
        .foregroundStyle(TrawlDesign.brandRed)
        .accessibilityHidden(true)
      VStack(alignment: .leading, spacing: 10) {
        HStack(alignment: .firstTextBaseline) {
          Label(OperationalCopy.systemSettings, systemImage: "gearshape")
            .font(.headline)
          Spacer()
          Text(OperationalCopy.fullDiskAccess)
            .font(.caption)
            .foregroundStyle(.secondary)
        }
        HStack(spacing: 10) {
          Image(nsImage: NSApplication.shared.applicationIconImage)
            .resizable()
            .frame(width: 28, height: 28)
          Text(OperationalCopy.openTrawl)
          Spacer()
          Image(systemName: "switch.2")
            .foregroundStyle(.secondary)
        }
        .padding(8)
        .background(Color.primary.opacity(0.05))
      }
      .padding(14)
      .frame(maxWidth: 320, alignment: .leading)
      .overlay {
        Rectangle()
          .stroke(
            Color.secondary,
            style: StrokeStyle(lineWidth: 1.5, dash: [6, 4])
          )
      }
      .accessibilityLabel(OperationalCopy.fullDiskAccess)
    }
    .frame(maxWidth: .infinity, alignment: .leading)
  }
}

private struct BuildStep: View {
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let aiInstruction: String
  let hasCopiedAIInstructions: Bool
  let onCopyAIInstructions: () -> Void
  let onRetryApp: (String) -> Void
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
    appModel.sources.contains { source in
      source.counts.contains { $0.value > 0 }
    }
      || appModel.syncResults.contains { result in
        result.failure == nil && result.outcome != .failed
      }
  }

  private var hasNoAvailableApps: Bool {
    let activeAppIDs = appModel.sources.map(\.id) + appModel.statusFailures.map(\.sourceID)
    let statusIsSettled =
      appModel.phase == .ready
      || (appModel.phase == .partial && appModel.statusFailures.isEmpty)
    return statusIsSettled
      && activeAppIDs.allSatisfy { !appInstallations.isAvailable($0) }
  }

  private var hasGlobalPermissionFailure: Bool {
    appModel.needsFullDiskAccessRecovery
  }

  var body: some View {
    TrawlFlowScaffold(step: HumanCopy.buildStep) {
      VStack(alignment: .leading, spacing: 24) {
        VStack(alignment: .leading, spacing: 6) {
          Text(HumanCopy.buildTitle)
            .font(.largeTitle.bold())
          Text(HumanCopy.buildBody)
            .font(.title3)
            .foregroundStyle(.secondary)
        }
        AIConnectionPanel(
          instruction: aiInstruction,
          hasCopied: hasCopiedAIInstructions,
          onCopy: copyAIInstructions
        )
        if hasGlobalPermissionFailure {
          PermissionRecoveryBanner(action: onPermissionRecovery)
        }
        if appModel.blockingFailureMessage != nil, reportedAppIDs.isEmpty {
          InitialLoadRecovery(action: onRetryInitialLoad)
        } else {
          AppBuildList(
            appModel: appModel,
            appInstallations: appInstallations,
            suppressPermissionFailures: hasGlobalPermissionFailure,
            onRetryApp: onRetryApp
          )
        }
      }
    } footer: {
      TrawlActionBar(
        backAction: nil,
        secondaryTitle: appModel.isSyncing ? OperationalCopy.cancel : nil,
        secondaryAction: appModel.isSyncing ? onStop : nil,
        primaryTitle: primaryTitle,
        primaryAction: primaryAction,
        primaryDisabled: hasCopiedAIInstructions && !canFinishSetup
      )
    }
  }

  private var reportedAppIDs: [String] {
    appModel.sources.map(\.id)
      + appModel.statusFailures.map(\.sourceID)
      + appModel.skippedSources.map(\.sourceID)
  }

  private var primaryTitle: String {
    if canFinishSetup { return OperationalCopy.finishSetup }
    if !hasCopiedAIInstructions { return OperationalCopy.copyAIInstructions }
    return OperationalCopy.copiedAIInstructions
  }

  private func primaryAction() {
    if canFinishSetup { return onFinish() }
    copyAIInstructions()
  }

  private func copyAIInstructions() {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(aiInstruction, forType: .string)
    onCopyAIInstructions()
  }
}

private struct InitialLoadRecovery: View {
  let action: () -> Void

  var body: some View {
    ContentUnavailableView {
      Label(OperationalCopy.appsUnavailable, systemImage: "exclamationmark.triangle")
    } description: {
      Text(OperationalCopy.statusCheckFailed)
      Text(OperationalCopy.statusCheckRecovery)
    } actions: {
      Button(OperationalCopy.retry, action: action)
        .buttonStyle(.borderedProminent)
    }
  }
}

private struct AIConnectionPanel: View {
  let instruction: String
  let hasCopied: Bool
  let onCopy: () -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: 12) {
      Text(HumanCopy.aiTitle).font(.title2.bold())
      Text(HumanCopy.aiBody).foregroundStyle(.secondary)
      Text(instruction)
        .font(.system(.callout, design: .monospaced))
        .textSelection(.enabled)
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.primary.opacity(0.05))
        .overlay { Rectangle().stroke(Color.primary.opacity(0.35), lineWidth: 1) }
      Text(HumanCopy.aiDoesNotInstall)
        .font(.callout)
        .foregroundStyle(.secondary)
      Button(
        hasCopied ? OperationalCopy.copiedAIInstructions : OperationalCopy.copyAIInstructions,
        action: onCopy
      )
      .disabled(hasCopied)
    }
    .padding(18)
    .overlay { Rectangle().stroke(Color.primary, lineWidth: 1) }
  }
}

struct PermissionRecoveryBanner: View {
  let action: () -> Void

  var body: some View {
    HStack(spacing: 12) {
      Image(systemName: "lock.trianglebadge.exclamationmark")
        .foregroundStyle(.orange)
      VStack(alignment: .leading, spacing: 3) {
        Text(OperationalCopy.accessNeeded).font(.headline)
        Text(OperationalCopy.accessRecovery).foregroundStyle(.secondary)
      }
      Spacer()
      Button(OperationalCopy.openFullDiskAccess, action: action)
    }
    .padding(16)
    .background(.orange.opacity(0.08))
    .overlay { Rectangle().stroke(.orange.opacity(0.5), lineWidth: 1) }
  }
}

private struct AppBuildList: View {
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let suppressPermissionFailures: Bool
  let onRetryApp: (String) -> Void

  private var appIDs: [String] {
    (appModel.sources.map(\.id)
      + appModel.statusFailures.map(\.sourceID)
      + appModel.skippedSources.map(\.sourceID))
      .reduce(into: []) { appIDs, appID in
        if !appIDs.contains(appID) { appIDs.append(appID) }
      }
  }

  var body: some View {
    VStack(spacing: 0) {
      ForEach(appIDs, id: \.self) { appID in
        let presentation = presentation(for: appID)
        TrawlStatusRow(
          name: presentation.name,
          counts: presentation.counts,
          detail: presentation.detail,
          status: presentation.status,
          statusLabel: presentation.statusLabel,
          recoveryTitle: presentation.canRetry ? OperationalCopy.retryApp : nil,
          recovery: presentation.canRetry ? { onRetryApp(appID) } : nil,
          recoveryDisabled: appModel.isSyncing
        )
        Divider()
      }
    }
    .overlay(alignment: .top) { Rectangle().frame(height: 2) }
    .overlay(alignment: .bottom) { Rectangle().frame(height: 1) }
  }

  private func presentation(for appID: String) -> AppBuildRowPresentation {
    let app = appModel.sources.first { $0.id == appID }
    let failure =
      appModel.syncFailures.first { $0.sourceID == appID }
      ?? appModel.syncResults.first { $0.sourceID == appID }?.failure
      ?? appModel.statusFailures.first { $0.sourceID == appID }
    let skipped = appModel.skippedSources.first { $0.sourceID == appID }
    return AppBuildRowPresentation.resolve(
      appID: appID,
      name: app?.manifest.displayName ?? failure?.sourceName ?? skipped?.surface ?? appID,
      counts: app?.counts ?? [],
      progress: appModel.syncProgress[appID],
      failure: failure,
      skipped: skipped,
      isInstalled: appInstallations.isAvailable(appID),
      suppressPermissionFailure: suppressPermissionFailures
    )
  }
}

struct AppBuildRowPresentation: Equatable {
  let name: String
  let counts: String?
  let detail: String?
  let status: TrawlStatus
  let statusLabel: String
  let canRetry: Bool

  static func resolve(
    appID _: String,
    name: String,
    counts: [SourceCount],
    progress: AppSyncProgressState?,
    failure: SourceFailure?,
    skipped: SkippedSource?,
    isInstalled: Bool,
    suppressPermissionFailure: Bool
  ) -> AppBuildRowPresentation {
    let countText =
      counts.isEmpty
      ? nil
      : OperationalCopy.counts(
        counts.map { "\($0.value.formatted()) \($0.label.lowercased())" }
      )
    if skipped != nil {
      return AppBuildRowPresentation(
        name: name, counts: countText, detail: nil, status: .neutral,
        statusLabel: OperationalCopy.comingSoon, canRetry: false
      )
    }
    guard isInstalled else {
      return AppBuildRowPresentation(
        name: name, counts: countText, detail: nil, status: .neutral,
        statusLabel: OperationalCopy.notInstalled, canRetry: false
      )
    }
    if suppressPermissionFailure, failure?.code == .permission {
      return AppBuildRowPresentation(
        name: name, counts: countText, detail: nil, status: .neutral,
        statusLabel: OperationalCopy.waiting, canRetry: false
      )
    }
    let isBuilding = {
      if case .building = progress { return true }
      if case .finalising = progress { return true }
      return false
    }()
    if isBuilding {
      return AppBuildRowPresentation(
        name: name, counts: countText, detail: nil, status: .working,
        statusLabel: OperationalCopy.building, canRetry: false
      )
    }
    if let failure {
      let hasArchive = counts.contains { $0.value > 0 }
      return AppBuildRowPresentation(
        name: name,
        counts: countText,
        detail: OperationalCopy.failureDetail(for: failure.code, appName: name),
        status: hasArchive ? .warning : .failure,
        statusLabel: hasArchive ? OperationalCopy.searchable : OperationalCopy.failed,
        canRetry: failure.code != .authentication && failure.code != .invalidInput
      )
    }
    if case .failed = progress {
      return AppBuildRowPresentation(
        name: name,
        counts: countText,
        detail: OperationalCopy.failureDetail(for: .internalError, appName: name),
        status: .failure,
        statusLabel: OperationalCopy.failed,
        canRetry: true
      )
    }
    let isFinished = if case .finished = progress { true } else { false }
    if isFinished || counts.contains(where: { $0.value > 0 }) {
      return AppBuildRowPresentation(
        name: name, counts: countText, detail: nil, status: .success,
        statusLabel: OperationalCopy.searchable, canRetry: false
      )
    }
    return AppBuildRowPresentation(
      name: name, counts: countText, detail: nil, status: .neutral,
      statusLabel: OperationalCopy.waiting, canRetry: false
    )
  }
}
