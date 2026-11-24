import AppKit
import SwiftUI
import TrawlClient
import TrawlCore

struct BuildStep: View {
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let aiInstruction: String
  let hasCopiedAIInstructions: Bool
  var skippedFullDiskAccess = false
  let onCopyAIInstructions: () -> Void
  let onBack: () -> Void
  let onRetryApp: (RegisteredTrawlerIdentity) -> Void
  let onRetryInitialLoad: () -> Void
  let onPermissionRecovery: () -> Void
  var onSignInGmail: (() -> Void)? = nil
  var onSignInX: (() -> Void)? = nil
  var onOpenSourceApp: ((String) -> Void)? = nil
  let onStop: () -> Void
  let onFinish: () -> Void

  private var canFinishSetup: Bool {
    !appModel.isUpdating
      && (hasSearchableArchive || skippedFullDiskAccess
        || (!hasGlobalPermissionFailure && hasNoAvailableApps))
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
      VStack(alignment: .leading, spacing: 18) {
        OnboardingProse(
          title: archiveIsReady
            ? HumanCopy.ArchiveBuild.readyTitle
            : HumanCopy.ArchiveBuild.title,
          lede: archiveIsReady
            ? HumanCopy.ArchiveBuild.readyBody
            : HumanCopy.ArchiveBuild.body
        )
        AIConnectionPanel(
          hasCopied: hasCopiedAIInstructions,
          onCopy: copyAIInstructions
        )
        ArchiveBuildStatus(
          appModel: appModel,
          appInstallations: appInstallations,
          hasGlobalPermissionFailure: hasGlobalPermissionFailure,
          onRetryApp: onRetryApp,
          onRetryInitialLoad: onRetryInitialLoad,
          onPermissionRecovery: onPermissionRecovery,
          onSignInGmail: onSignInGmail,
          onSignInX: onSignInX,
          onOpenSourceApp: onOpenSourceApp
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
}

private struct ArchiveBuildStatus: View {
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let hasGlobalPermissionFailure: Bool
  let onRetryApp: (RegisteredTrawlerIdentity) -> Void
  let onRetryInitialLoad: () -> Void
  let onPermissionRecovery: () -> Void
  let onSignInGmail: (() -> Void)?
  let onSignInX: (() -> Void)?
  let onOpenSourceApp: ((String) -> Void)?

  var body: some View {
    if appModel.blockingFailureMessage != nil, appModel.displayedTrawlers.isEmpty {
      InitialLoadRecovery(action: onRetryInitialLoad)
    } else {
      VStack(alignment: .leading, spacing: 12) {
        if hasGlobalPermissionFailure {
          PermissionRecoveryBanner(action: onPermissionRecovery)
        }
        ArchiveTrawlerSummary(
          appModel: appModel,
          appInstallations: appInstallations,
          onRetryApp: onRetryApp,
          onPermissionRecovery: onPermissionRecovery,
          onSignInGmail: onSignInGmail,
          onSignInX: onSignInX,
          onOpenSourceApp: onOpenSourceApp
        )
      }
    }
  }
}

private struct ArchiveTrawlerSummary: View {
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let onRetryApp: (RegisteredTrawlerIdentity) -> Void
  let onPermissionRecovery: () -> Void
  let onSignInGmail: (() -> Void)?
  let onSignInX: (() -> Void)?
  let onOpenSourceApp: ((String) -> Void)?

  private var statusesByID: [RegisteredTrawlerIdentity: TrawlerStatus] {
    Dictionary(uniqueKeysWithValues: appModel.trawlerStatuses.map { ($0.id, $0) })
  }

  private var failuresByID: [RegisteredTrawlerIdentity: TrawlerOperationFailure] {
    Dictionary(
      (appModel.statusOperationFailures + appModel.updateOperationFailures).map {
        ($0.failedTrawler, $0)
      },
      uniquingKeysWith: { _, latest in latest }
    )
  }

  private var availableTrawlers: [RestingTrawler] {
    appModel.homeTrawlers.filter { $0.state != .comingSoon }
  }

  private var searchableCount: Int {
    availableTrawlers.count { trawler in
      let op = operability(for: trawler)
      return op.archivedItemCount > 0 && !op.needsAttention
    }
  }

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      HStack(alignment: .firstTextBaseline) {
        Text(HumanCopy.ArchiveBuild.yourAppsTitle)
          .trawlText(.sectionHeader)
        Spacer()
        Text(
          String(
            format: HumanCopy.ArchiveBuild.progressFormat,
            searchableCount,
            availableTrawlers.count
          )
        )
        .trawlText(.meta)
        .foregroundStyle(.secondary)
      }

      ScrollView {
        VStack(spacing: 0) {
          ForEach(Array(availableTrawlers.enumerated()), id: \.element.id) { index, trawler in
            let state = operability(for: trawler)
            ArchiveBuildRow(
              trawler: trawler,
              state: state,
              isUpdating: appModel.isUpdating,
              onAction: { action in
                perform(action, for: trawler.id)
              }
            )
            if index < availableTrawlers.count - 1 {
              Divider()
            }
          }
        }
      }
      .frame(maxHeight: 310)
      .background(
        TrawlDesign.Colors.paper,
        in: TrawlDesign.Shape.surface
      )
      .overlay(
        TrawlDesign.Shape.surface
          .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
      )
    }
  }

  private func operability(for trawler: RestingTrawler) -> AppOperabilityState {
    AppOperabilityState.resolve(
      trawler: trawler,
      status: statusesByID[trawler.id],
      updateProgress: appModel.updateProgress[trawler.id],
      operationFailure: failuresByID[trawler.id],
      isRemoteArchive: false,
      isInstalledOnThisMac: appInstallations.isAvailable(trawler.id),
      fullDiskAccessChoice: appModel.fullDiskAccessChoice
    )
  }

  private func perform(_ action: AppAttentionAction, for trawlerID: RegisteredTrawlerIdentity) {
    switch action {
    case .openFullDiskAccessSettings:
      onPermissionRecovery()
    case .openPrivacySettings(let paneAnchor):
      if let url = URL(
        string: "x-apple.systempreferences:com.apple.preference.security?\(paneAnchor)"
      ) {
        NSWorkspace.shared.open(url)
      }
    case .openSourceApp(let bundleID, _):
      onOpenSourceApp?(bundleID)
    case .signInGmail:
      onSignInGmail?()
    case .signInX:
      onSignInX?()
    case .retryUpdate:
      onRetryApp(trawlerID)
    }
  }
}

private struct ArchiveBuildRow: View {
  let trawler: RestingTrawler
  let state: AppOperabilityState
  let isUpdating: Bool
  let onAction: (AppAttentionAction) -> Void

  var body: some View {
    HStack(spacing: 10) {
      TrawlerIconView(registeredTrawler: trawler.id, size: 22)
      Text(trawler.registeredTrawlerDisplayName)
        .font(TrawlDesign.Typography.laneLabel)
        .foregroundStyle(TrawlDesign.Colors.ink)
      Spacer(minLength: TrawlDesign.Space.sm)
      Text(state.statusLine)
        .font(TrawlDesign.Typography.meta)
        .foregroundStyle(state.needsAttention ? TrawlDesign.Colors.warningAmberText : TrawlDesign.Colors.mutedInk)
        .lineLimit(1)
        .truncationMode(.tail)
      if let actionTitle = state.actionTitle, let action = state.action {
        Button(actionTitle) {
          onAction(action)
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .disabled(isUpdating && action == .retryUpdate)
      }
    }
    .padding(.horizontal, 12)
    .frame(minHeight: 38)
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
    HStack(alignment: .center, spacing: 14) {
      VStack(alignment: .leading, spacing: 4) {
        Text(HumanCopy.ConnectAI.title)
          .trawlText(.sectionHeader)
        Text(HumanCopy.ConnectAI.body)
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
          .fixedSize(horizontal: false, vertical: true)
      }
      Spacer(minLength: TrawlDesign.Space.sm)
      Button(action: onCopy) {
        Label(
          hasCopied ? "Copied" : HumanCopy.ConnectAI.copyAction,
          systemImage: hasCopied ? "checkmark" : "doc.on.doc"
        )
      }
      .buttonStyle(.bordered)
      .buttonBorderShape(.capsule)
      .controlSize(.small)
      .tint(.primary)
    }
    .padding(TrawlDesign.Space.md)
    .background(
      TrawlDesign.Colors.surface,
      in: TrawlDesign.Shape.surface
    )
  }
}
