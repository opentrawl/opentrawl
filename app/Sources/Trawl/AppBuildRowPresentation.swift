import TrawlClient
import TrawlCore

struct AppBuildRowPresentation: Equatable {
  let name: String
  let status: TrawlStatus
  let statusLabel: String
  let canRetry: Bool

  @MainActor
  static func resolve(
    for registeredTrawler: RegisteredTrawlerIdentity,
    in appModel: AppModel,
    appInstallations: MacAppInstallations,
    suppressPermissionFailure: Bool
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
    return resolve(
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
      suppressPermissionFailure: suppressPermissionFailure
    )
  }

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
