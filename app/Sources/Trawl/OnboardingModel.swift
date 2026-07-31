import Foundation
import Observation
import PermissionGuide
import TrawlClient
import TrawlCore

enum OnboardingStage: String, Sendable, Equatable {
  case welcome
  case permission
  case building
  case commandDemo
  case complete
}

enum PermissionCheckState: Sendable, Equatable {
  case idle
  case checking
  case confirmed
  case notConfirmed
}

@MainActor
@Observable
final class OnboardingModel {
  static let completionKey = "OpenTrawlOnboardingComplete"
  static let checkpointKey = "OpenTrawlOnboardingCheckpoint"
  static let checkpointOwnerKey = "OpenTrawlOnboardingCheckpointOwner"

  private let defaults: UserDefaults
  private let checkpointOwner: String
  private let openFullDiskAccess: @MainActor () -> Void
  private var permissionTask: Task<Void, Never>?
  private var syncTask: Task<Void, Never>?
  private var shouldResumeInitialSync = false
  private var isAwaitingPermissionReturn = false
  private var hasStartedArchiveBuild = false

  private(set) var stage: OnboardingStage
  private(set) var permissionCheck: PermissionCheckState = .idle
  private(set) var hasCopiedAIInstructions = false

  var isComplete: Bool { stage == .complete }

  init(
    defaults: UserDefaults = .standard,
    checkpointOwner: String = OnboardingModel.currentCheckpointOwner,
    openFullDiskAccess: @escaping @MainActor () -> Void =
      PermissionGuideController.openSystemSettings
  ) {
    self.defaults = defaults
    self.checkpointOwner = checkpointOwner
    self.openFullDiskAccess = openFullDiskAccess
    if defaults.bool(forKey: Self.completionKey) {
      stage = .complete
    } else if defaults.string(forKey: Self.checkpointOwnerKey) != checkpointOwner {
      defaults.removeObject(forKey: Self.checkpointKey)
      defaults.removeObject(forKey: Self.checkpointOwnerKey)
      stage = .welcome
    } else if defaults.string(forKey: Self.checkpointKey) == OnboardingStage.commandDemo.rawValue {
      stage = .commandDemo
      hasStartedArchiveBuild = true
    } else if defaults.string(forKey: Self.checkpointKey) == OnboardingStage.building.rawValue {
      stage = .building
      hasStartedArchiveBuild = true
      shouldResumeInitialSync = true
    } else if defaults.string(forKey: Self.checkpointKey) != nil {
      stage = .permission
    } else {
      stage = .welcome
    }
  }

  func showPermission() {
    isAwaitingPermissionReturn = false
    permissionCheck = .idle
    stage = .permission
    if !hasStartedArchiveBuild {
      saveCheckpoint(.permission)
    }
  }

  func showPermission(appModel: AppModel) {
    showPermission()
    if appModel.checkDiskAccess() == .granted {
      permissionCheck = .confirmed
    }
  }

  func showWelcome() {
    permissionTask?.cancel()
    permissionTask = nil
    permissionCheck = .idle
    isAwaitingPermissionReturn = false
    stage = .welcome
    guard !hasStartedArchiveBuild else { return }
    syncTask?.cancel()
    syncTask = nil
    defaults.removeObject(forKey: Self.checkpointKey)
    defaults.removeObject(forKey: Self.checkpointOwnerKey)
  }

  func returnToBuilding() {
    guard hasStartedArchiveBuild else { return }
    permissionTask?.cancel()
    permissionTask = nil
    isAwaitingPermissionReturn = false
    permissionCheck = .confirmed
    stage = .building
    saveCheckpoint(.building)
  }

  func showCommandDemo() {
    guard hasStartedArchiveBuild else { return }
    syncTask?.cancel()
    syncTask = nil
    stage = .commandDemo
    saveCheckpoint(.commandDemo)
  }

  func requestPermission(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    showPermission()
    if appModel.checkDiskAccess() == .granted {
      permissionCheck = .confirmed
      return
    }
    isAwaitingPermissionReturn = true
    openFullDiskAccess()
    startPermissionChecks(appModel: appModel, registeredTrawlers: registeredTrawlers)
  }

  func applicationDidBecomeActive(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    guard stage == .permission, isAwaitingPermissionReturn else { return }
    checkPermission(appModel: appModel, registeredTrawlers: registeredTrawlers)
  }

  func checkPermission(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    guard stage == .permission else { return }
    permissionCheck = .checking
    switch appModel.checkDiskAccess() {
    case .granted:
      permissionTask?.cancel()
      permissionTask = nil
      isAwaitingPermissionReturn = false
      permissionCheck = .confirmed
    case .denied:
      permissionCheck = .notConfirmed
    case .undetermined:
      let currentRegisteredTrawlers = registeredTrawlers()
      let verificationSet = Set(appModel.fullDiskAccessTrawlers)
      let verificationTrawlers =
        currentRegisteredTrawlers.filter(verificationSet.contains)
      if verificationTrawlers.isEmpty {
        let statusProvesNoPermissionFailure =
          appModel.phase == .ready
          || (appModel.phase == .partial && appModel.statusOperationFailures.isEmpty)
        if statusProvesNoPermissionFailure {
          startInitialSync(
            appModel: appModel, registeredTrawlers: currentRegisteredTrawlers)
        } else {
          permissionCheck = .notConfirmed
        }
      } else {
        verifyAccessByReadingTrawlerArchive(
          appModel: appModel,
          verificationTrawlers: verificationTrawlers,
          initialSyncTrawlers: currentRegisteredTrawlers
        )
      }
    }
  }

  func continueWithVerifiedAccess(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    guard stage == .permission, permissionCheck == .confirmed else { return }
    continueAfterVerifiedAccess(
      appModel: appModel, registeredTrawlers: registeredTrawlers)
  }

  func startInitialSync(
    appModel: AppModel,
    registeredTrawlers: [RegisteredTrawlerIdentity]
  ) {
    permissionTask?.cancel()
    permissionTask = nil
    syncTask?.cancel()
    isAwaitingPermissionReturn = false
    hasStartedArchiveBuild = true
    stage = .building
    saveCheckpoint(.building)
    guard !registeredTrawlers.isEmpty else { return }
    syncTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.syncNow(registeredTrawlers: registeredTrawlers)
      guard !Task.isCancelled else { return }
      self.syncTask = nil
    }
  }

  func retry(appModel: AppModel, registeredTrawler: RegisteredTrawlerIdentity) {
    guard stage == .building else { return }
    syncTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.syncNow(registeredTrawlers: [registeredTrawler])
      guard !Task.isCancelled else { return }
      self.syncTask = nil
    }
  }

  func retryInitialLoad(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    guard stage == .building else { return }
    syncTask?.cancel()
    syncTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.refresh()
      guard !Task.isCancelled else { return }
      let refreshedRegisteredTrawlers = registeredTrawlers()
      if !refreshedRegisteredTrawlers.isEmpty {
        await appModel.syncNow(
          registeredTrawlers: refreshedRegisteredTrawlers)
      }
      guard !Task.isCancelled else { return }
      self.syncTask = nil
    }
  }

  func resumeInitialSyncIfNeeded(
    appModel: AppModel,
    registeredTrawlers: [RegisteredTrawlerIdentity]
  ) {
    guard stage == .building, shouldResumeInitialSync, !registeredTrawlers.isEmpty
    else { return }
    shouldResumeInitialSync = false
    startInitialSync(appModel: appModel, registeredTrawlers: registeredTrawlers)
  }

  func reopenPermissionRecovery(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    requestPermission(appModel: appModel, registeredTrawlers: registeredTrawlers)
  }

  func stopSync() {
    syncTask?.cancel()
  }

  func didCopyAIInstructions() {
    hasCopiedAIInstructions = true
  }

  func complete() {
    defaults.set(true, forKey: Self.completionKey)
    defaults.removeObject(forKey: Self.checkpointKey)
    defaults.removeObject(forKey: Self.checkpointOwnerKey)
    stage = .complete
    isAwaitingPermissionReturn = false
  }

  private func startPermissionChecks(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    permissionTask?.cancel()
    permissionTask = Task { @MainActor [weak self, weak appModel] in
      while !Task.isCancelled {
        guard let self, let appModel, self.stage == .permission else { return }
        self.permissionCheck = .checking
        switch appModel.checkDiskAccess() {
        case .granted:
          self.isAwaitingPermissionReturn = false
          self.permissionCheck = .confirmed
          return
        case .denied:
          self.permissionCheck = .notConfirmed
        case .undetermined:
          break
        }
        try? await Task.sleep(for: .seconds(1))
      }
    }
  }

  private func continueAfterVerifiedAccess(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    if hasStartedArchiveBuild {
      returnToBuilding()
      return
    }
    let currentRegisteredTrawlers = registeredTrawlers()
    guard currentRegisteredTrawlers.isEmpty, appModel.phase == .loading else {
      startInitialSync(
        appModel: appModel, registeredTrawlers: currentRegisteredTrawlers)
      return
    }

    permissionTask?.cancel()
    permissionTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.refresh()
      guard !Task.isCancelled, self.stage == .permission else { return }
      self.startInitialSync(
        appModel: appModel, registeredTrawlers: registeredTrawlers())
    }
  }

  private func verifyAccessByReadingTrawlerArchive(
    appModel: AppModel,
    verificationTrawlers: [RegisteredTrawlerIdentity],
    initialSyncTrawlers: [RegisteredTrawlerIdentity]
  ) {
    guard syncTask == nil else { return }
    let requestedTrawlers = Set(verificationTrawlers)
    syncTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.syncNow(registeredTrawlers: verificationTrawlers)
      guard !Task.isCancelled else { return }
      let verified = appModel.trawlerArchiveSyncResults.contains {
        trawlerArchiveSyncResult in
        requestedTrawlers.contains(trawlerArchiveSyncResult.registeredTrawler)
          && !appModel.syncOperationFailures.contains { failure in
            failure.failedTrawler == trawlerArchiveSyncResult.registeredTrawler
          }
      }
      self.syncTask = nil
      if verified {
        self.permissionTask?.cancel()
        self.permissionTask = nil
        self.permissionCheck = .confirmed
        self.isAwaitingPermissionReturn = false
        self.hasStartedArchiveBuild = true
        self.saveCheckpoint(.building)
        let remainingTrawlers = initialSyncTrawlers.filter {
          !requestedTrawlers.contains($0)
        }
        if !remainingTrawlers.isEmpty {
          await appModel.syncNow(registeredTrawlers: remainingTrawlers)
        }
      } else {
        self.permissionCheck = .notConfirmed
      }
    }
  }

  private func saveCheckpoint(_ stage: OnboardingStage) {
    defaults.set(stage.rawValue, forKey: Self.checkpointKey)
    defaults.set(checkpointOwner, forKey: Self.checkpointOwnerKey)
  }

  static var currentCheckpointOwner: String {
    let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
    let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String
    let commit = Bundle.main.object(forInfoDictionaryKey: "GitCommit") as? String
    return [version, build, commit]
      .compactMap { $0 }
      .joined(separator: ":")
  }
}
