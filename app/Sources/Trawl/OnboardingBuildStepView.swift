import AppKit
import SwiftUI
import TrawlClient
import TrawlCore

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
      && (hasSearchableArchive || hasNoAvailableApps || !appModel.zeroFullDiskAccessTrawlers.isEmpty)
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
    VStack(alignment: .leading, spacing: 14) {
      if hasGlobalPermissionFailure {
        OptionalFullDiskAccessCard(
          fdaAppNames: appModel.fullDiskAccessOptionalTrawlers.map(\.registeredTrawlerDisplayName),
          onUnlock: onPermissionRecovery
        )
      }
      if appModel.blockingFailureMessage != nil, appModel.displayedTrawlers.isEmpty {
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
    availableTrawlers.map {
      (
        $0,
        AppBuildRowPresentation.resolve(
          for: $0,
          in: appModel,
          appInstallations: appInstallations,
          suppressPermissionFailure: false
        )
      )
    }
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
    .tint(.primary)
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
        let presentation = AppBuildRowPresentation.resolve(
          for: registeredTrawler,
          in: appModel,
          appInstallations: appInstallations,
          suppressPermissionFailure: suppressPermissionFailures
        )
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
}

private struct ArchiveAppRow: View {
  let registeredTrawler: RegisteredTrawlerIdentity
  let name: String
  let status: TrawlStatus
  let statusLabel: String?
  var accessibilityStatus: String?
  var symbolOverride: String?
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
