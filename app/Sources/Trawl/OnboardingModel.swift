import Foundation
import Observation
import PermissionGuide
import TrawlCore

enum OnboardingStage: String, Sendable, Equatable {
  case welcome
  case permission
  case building
  case complete
}

enum PermissionCheckState: Sendable, Equatable {
  case idle
  case checking
  case notConfirmed
}

@MainActor
@Observable
final class OnboardingModel {
  static let completionKey = "OpenTrawlOnboardingComplete"
  static let checkpointKey = "OpenTrawlOnboardingCheckpoint"

  private let defaults: UserDefaults
  private let openFullDiskAccess: @MainActor () -> Void
  private var permissionTask: Task<Void, Never>?
  private var syncTask: Task<Void, Never>?
  private var shouldResumeInitialSync = false

  private(set) var stage: OnboardingStage
  private(set) var permissionCheck: PermissionCheckState = .idle
  private(set) var hasCopiedAIInstructions = false

  var isComplete: Bool { stage == .complete }

  init(
    defaults: UserDefaults = .standard,
    openFullDiskAccess: @escaping @MainActor () -> Void =
      PermissionGuideController.openSystemSettings
  ) {
    self.defaults = defaults
    self.openFullDiskAccess = openFullDiskAccess
    if defaults.bool(forKey: Self.completionKey) {
      stage = .complete
    } else if defaults.string(forKey: Self.checkpointKey) == OnboardingStage.building.rawValue {
      stage = .building
      shouldResumeInitialSync = true
    } else if defaults.string(forKey: Self.checkpointKey) != nil {
      stage = .permission
    } else {
      stage = .welcome
    }
  }

  func showPermission() {
    stage = .permission
    defaults.set(OnboardingStage.permission.rawValue, forKey: Self.checkpointKey)
  }

  func showWelcome() {
    permissionTask?.cancel()
    permissionTask = nil
    syncTask?.cancel()
    syncTask = nil
    permissionCheck = .idle
    stage = .welcome
    defaults.removeObject(forKey: Self.checkpointKey)
  }

  func requestPermission(appModel: AppModel, appIDs: @escaping @MainActor () -> [String]) {
    showPermission()
    openFullDiskAccess()
    startPermissionChecks(appModel: appModel, appIDs: appIDs)
  }

  func applicationDidBecomeActive(
    appModel: AppModel,
    appIDs: @escaping @MainActor () -> [String]
  ) {
    guard stage == .permission else { return }
    checkPermission(appModel: appModel, appIDs: appIDs())
  }

  func checkPermission(appModel: AppModel, appIDs: [String]) {
    guard stage == .permission else { return }
    permissionCheck = .checking
    switch appModel.checkDiskAccess() {
    case .granted:
      let statusIsSettled =
        appModel.phase == .ready
        || (appModel.phase == .partial && appModel.statusFailures.isEmpty)
      guard !appIDs.isEmpty || statusIsSettled else { return }
      permissionTask?.cancel()
      permissionTask = nil
      startInitialSync(appModel: appModel, appIDs: appIDs)
    case .denied:
      permissionCheck = .notConfirmed
    case .undetermined:
      let verificationSet = Set(appModel.fullDiskAccessAppIDs)
      let verificationAppIDs = appIDs.filter(verificationSet.contains)
      if verificationAppIDs.isEmpty {
        let statusProvesNoPermissionFailure =
          appModel.phase == .ready
          || (appModel.phase == .partial && appModel.statusFailures.isEmpty)
        if statusProvesNoPermissionFailure {
          startInitialSync(appModel: appModel, appIDs: appIDs)
        } else {
          permissionCheck = .notConfirmed
        }
      } else {
        verifyAccessByReadingSource(
          appModel: appModel,
          verificationAppIDs: verificationAppIDs,
          initialSyncAppIDs: appIDs
        )
      }
    }
  }

  func startInitialSync(appModel: AppModel, appIDs: [String]) {
    permissionTask?.cancel()
    permissionTask = nil
    syncTask?.cancel()
    stage = .building
    defaults.set(OnboardingStage.building.rawValue, forKey: Self.checkpointKey)
    guard !appIDs.isEmpty else { return }
    syncTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.syncNow(appIDs: appIDs)
      guard !Task.isCancelled else { return }
      self.syncTask = nil
    }
  }

  func retry(appModel: AppModel, appID: String) {
    guard stage == .building else { return }
    syncTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.syncNow(appIDs: [appID])
      guard !Task.isCancelled else { return }
      self.syncTask = nil
    }
  }

  func retryInitialLoad(
    appModel: AppModel,
    appIDs: @escaping @MainActor () -> [String]
  ) {
    guard stage == .building else { return }
    syncTask?.cancel()
    syncTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.refresh()
      guard !Task.isCancelled else { return }
      let refreshedAppIDs = appIDs()
      if !refreshedAppIDs.isEmpty {
        await appModel.syncNow(appIDs: refreshedAppIDs)
      }
      guard !Task.isCancelled else { return }
      self.syncTask = nil
    }
  }

  func resumeInitialSyncIfNeeded(appModel: AppModel, appIDs: [String]) {
    guard stage == .building, shouldResumeInitialSync, !appIDs.isEmpty else { return }
    shouldResumeInitialSync = false
    startInitialSync(appModel: appModel, appIDs: appIDs)
  }

  func reopenPermissionRecovery(
    appModel: AppModel,
    appIDs: @escaping @MainActor () -> [String]
  ) {
    stage = .permission
    defaults.set(OnboardingStage.permission.rawValue, forKey: Self.checkpointKey)
    requestPermission(appModel: appModel, appIDs: appIDs)
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
    stage = .complete
  }

  private func startPermissionChecks(
    appModel: AppModel,
    appIDs: @escaping @MainActor () -> [String]
  ) {
    permissionTask?.cancel()
    permissionTask = Task { @MainActor [weak self, weak appModel] in
      while !Task.isCancelled {
        guard let self, let appModel, self.stage == .permission else { return }
        self.permissionCheck = .checking
        switch appModel.checkDiskAccess() {
        case .granted:
          self.startInitialSync(appModel: appModel, appIDs: appIDs())
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

  private func verifyAccessByReadingSource(
    appModel: AppModel,
    verificationAppIDs: [String],
    initialSyncAppIDs: [String]
  ) {
    guard syncTask == nil else { return }
    let requestedIDs = Set(verificationAppIDs)
    syncTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.syncNow(appIDs: verificationAppIDs)
      guard !Task.isCancelled else { return }
      let verified = appModel.syncResults.contains {
        requestedIDs.contains($0.sourceID)
          && $0.failure == nil
          && $0.outcome != .failed
      }
      self.syncTask = nil
      if verified {
        self.permissionTask?.cancel()
        self.permissionTask = nil
        self.stage = .building
        self.defaults.set(OnboardingStage.building.rawValue, forKey: Self.checkpointKey)
        let remainingAppIDs = initialSyncAppIDs.filter { !requestedIDs.contains($0) }
        if !remainingAppIDs.isEmpty {
          await appModel.syncNow(appIDs: remainingAppIDs)
        }
      } else {
        self.permissionCheck = .notConfirmed
      }
    }
  }
}
