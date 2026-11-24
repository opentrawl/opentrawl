import Foundation
import Observation
import PermissionGuide
import TrawlClient
import TrawlCore

enum OnboardingStage: String, Sendable, Equatable {
  case welcome
  case chooseApps
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

  private enum OnboardingFlowState: Equatable {
    case welcome(archiveBuildHasStarted: Bool)
    case chooseApps(archiveBuildHasStarted: Bool)
    case permission(
      checkState: PermissionCheckState,
      awaitingReturnFromSystemSettings: Bool,
      archiveBuildHasStarted: Bool
    )
    case building(needsInitialUpdateResume: Bool)
    case commandDemo
    case complete
  }

  private let defaults: UserDefaults
  private let openFullDiskAccess: @MainActor () -> Void
  private var permissionTask: Task<Void, Never>?
  private var updateTask: Task<Void, Never>?
  private var flowState: OnboardingFlowState

  private(set) var hasCopiedAIInstructions = false
  /// The docked guide is showing in System Settings, holding the app tile.
  private(set) var permissionGuideIsOpen = false
  /// The person explicitly chose to leave out the five Full Disk Access apps for now.
  private(set) var skippedFullDiskAccess = false

  var stage: OnboardingStage {
    switch flowState {
    case .welcome: .welcome
    case .chooseApps: .chooseApps
    case .permission: .permission
    case .building: .building
    case .commandDemo: .commandDemo
    case .complete: .complete
    }
  }

  var permissionCheck: PermissionCheckState {
    switch flowState {
    case .permission(let checkState, _, _):
      checkState
    case .building, .commandDemo, .complete:
      .confirmed
    case .welcome, .chooseApps:
      .idle
    }
  }

  var isComplete: Bool { stage == .complete }

  private var archiveBuildHasStarted: Bool {
    switch flowState {
    case .welcome(let archiveBuildHasStarted),
      .chooseApps(let archiveBuildHasStarted),
      .permission(_, _, let archiveBuildHasStarted):
      archiveBuildHasStarted
    case .building, .commandDemo, .complete:
      true
    }
  }

  init(
    defaults: UserDefaults = .standard,
    openFullDiskAccess: @escaping @MainActor () -> Void =
      PermissionGuideController.openSystemSettings
  ) {
    self.defaults = defaults
    self.openFullDiskAccess = openFullDiskAccess
    self.skippedFullDiskAccess = FullDiskAccessChoice.load(from: defaults) == .leftOut
    if defaults.bool(forKey: Self.completionKey) {
      flowState = .complete
    } else if let saved = defaults.string(forKey: Self.checkpointKey),
      let savedStage = OnboardingStage(rawValue: saved)
    {
      switch savedStage {
      case .welcome:
        flowState = .welcome(archiveBuildHasStarted: false)
      case .chooseApps:
        flowState = .chooseApps(archiveBuildHasStarted: false)
      case .permission:
        flowState = .permission(
          checkState: .idle,
          awaitingReturnFromSystemSettings: false,
          archiveBuildHasStarted: false
        )
      case .building:
        flowState = .building(needsInitialUpdateResume: true)
      case .commandDemo:
        flowState = .commandDemo
      case .complete:
        flowState = .complete
      }
    } else {
      flowState = .welcome(archiveBuildHasStarted: false)
    }
  }

  func showChooseApps() {
    FullDiskAccessGuide.dismiss()
    permissionTask?.cancel()
    permissionTask = nil
    let buildStarted = archiveBuildHasStarted
    flowState = .chooseApps(archiveBuildHasStarted: buildStarted)
    if !buildStarted {
      saveCheckpoint(.chooseApps)
    }
  }

  func includeFullDiskAccessApps(appModel: AppModel) {
    skippedFullDiskAccess = false
    appModel.setFullDiskAccessChoice(.included)
    showPermission(appModel: appModel)
  }

  func leaveOutFullDiskAccessApps(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    skippedFullDiskAccess = true
    appModel.setFullDiskAccessChoice(.leftOut)
    FullDiskAccessGuide.dismiss()
    continueAfterVerifiedAccess(
      appModel: appModel,
      registeredTrawlers: registeredTrawlers
    )
  }

  func showPermission() {
    let buildStarted = archiveBuildHasStarted
    flowState = .permission(
      checkState: .idle,
      awaitingReturnFromSystemSettings: false,
      archiveBuildHasStarted: buildStarted
    )
    if !buildStarted {
      saveCheckpoint(.permission)
    }
  }

  func showPermission(appModel: AppModel) {
    showPermission()
    if appModel.checkDiskAccess() == .granted {
      setPermissionCheckState(.confirmed, awaitingReturnFromSystemSettings: false)
    }
  }

  func showWelcome() {
    FullDiskAccessGuide.dismiss()
    permissionTask?.cancel()
    permissionTask = nil
    let buildStarted = archiveBuildHasStarted
    flowState = .welcome(archiveBuildHasStarted: buildStarted)
    guard !buildStarted else { return }
    updateTask?.cancel()
    updateTask = nil
    defaults.removeObject(forKey: Self.checkpointKey)
  }

  func returnToBuilding() {
    guard archiveBuildHasStarted else { return }
    permissionTask?.cancel()
    permissionTask = nil
    flowState = .building(needsInitialUpdateResume: false)
    saveCheckpoint(.building)
  }

  func showCommandDemo() {
    guard archiveBuildHasStarted else { return }
    updateTask?.cancel()
    updateTask = nil
    flowState = .commandDemo
    saveCheckpoint(.commandDemo)
  }

  func requestPermission(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    appModel.setFullDiskAccessChoice(.included)
    skippedFullDiskAccess = false
    showPermission()
    if appModel.checkDiskAccess() == .granted {
      setPermissionCheckState(.confirmed, awaitingReturnFromSystemSettings: false)
      return
    }
    setPermissionCheckState(.idle, awaitingReturnFromSystemSettings: true)
    permissionGuideIsOpen = true
    openFullDiskAccess()
    startPermissionChecks(appModel: appModel, registeredTrawlers: registeredTrawlers)
  }

  func permissionGuideDidClose() {
    permissionGuideIsOpen = false
  }

  func applicationDidBecomeActive(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    guard case .permission(_, awaitingReturnFromSystemSettings: true, _) = flowState else {
      return
    }
    checkPermission(appModel: appModel, registeredTrawlers: registeredTrawlers)
  }

  func checkPermission(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    guard case .permission(_, let awaitingReturn, _) = flowState else { return }
    setPermissionCheckState(.checking, awaitingReturnFromSystemSettings: awaitingReturn)
    switch appModel.checkDiskAccess() {
    case .granted:
      permissionTask?.cancel()
      permissionTask = nil
      setPermissionCheckState(.confirmed, awaitingReturnFromSystemSettings: false)
    case .denied:
      setPermissionCheckState(.notConfirmed, awaitingReturnFromSystemSettings: awaitingReturn)
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
          startInitialUpdate(
            appModel: appModel, registeredTrawlers: currentRegisteredTrawlers)
        } else {
          setPermissionCheckState(
            .notConfirmed, awaitingReturnFromSystemSettings: awaitingReturn)
        }
      } else {
        verifyAccessByReadingTrawlerArchive(
          appModel: appModel,
          verificationTrawlers: verificationTrawlers,
          initialUpdateTrawlers: currentRegisteredTrawlers
        )
      }
    }
  }

  func continueWithVerifiedAccess(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    guard case .permission(checkState: .confirmed, _, _) = flowState else { return }
    skippedFullDiskAccess = false
    appModel.setFullDiskAccessChoice(.included)
    continueAfterVerifiedAccess(
      appModel: appModel, registeredTrawlers: registeredTrawlers)
  }

  func continueWithoutFullDiskAccess(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    guard case .permission = flowState else { return }
    skippedFullDiskAccess = true
    appModel.setFullDiskAccessChoice(.leftOut)
    FullDiskAccessGuide.dismiss()
    continueAfterVerifiedAccess(
      appModel: appModel, registeredTrawlers: registeredTrawlers)
  }

  func startInitialUpdate(
    appModel: AppModel,
    registeredTrawlers: [RegisteredTrawlerIdentity]
  ) {
    permissionTask?.cancel()
    permissionTask = nil
    updateTask?.cancel()
    flowState = .building(needsInitialUpdateResume: false)
    saveCheckpoint(.building)
    guard !registeredTrawlers.isEmpty else { return }
    updateTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.updateNow(registeredTrawlers: registeredTrawlers)
      guard !Task.isCancelled else { return }
      self.updateTask = nil
    }
  }

  func retry(appModel: AppModel, registeredTrawler: RegisteredTrawlerIdentity) {
    guard case .building = flowState else { return }
    updateTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.updateNow(registeredTrawlers: [registeredTrawler])
      guard !Task.isCancelled else { return }
      self.updateTask = nil
    }
  }

  func retryInitialLoad(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    guard case .building = flowState else { return }
    updateTask?.cancel()
    updateTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.refresh()
      guard !Task.isCancelled else { return }
      let refreshedRegisteredTrawlers = registeredTrawlers()
      if !refreshedRegisteredTrawlers.isEmpty {
        await appModel.updateNow(
          registeredTrawlers: refreshedRegisteredTrawlers)
      }
      guard !Task.isCancelled else { return }
      self.updateTask = nil
    }
  }

  func resumeInitialUpdateIfNeeded(
    appModel: AppModel,
    registeredTrawlers: [RegisteredTrawlerIdentity]
  ) {
    guard case .building(needsInitialUpdateResume: true) = flowState,
      !registeredTrawlers.isEmpty
    else { return }
    startInitialUpdate(appModel: appModel, registeredTrawlers: registeredTrawlers)
  }

  func reopenPermissionRecovery(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    requestPermission(appModel: appModel, registeredTrawlers: registeredTrawlers)
  }

  func stopUpdate() {
    updateTask?.cancel()
  }

  func didCopyAIInstructions() {
    hasCopiedAIInstructions = true
  }

  func complete() {
    defaults.set(true, forKey: Self.completionKey)
    defaults.removeObject(forKey: Self.checkpointKey)
    flowState = .complete
  }

  func reset() {
    FullDiskAccessGuide.dismiss()
    permissionTask?.cancel()
    permissionTask = nil
    updateTask?.cancel()
    updateTask = nil
    hasCopiedAIInstructions = false
    permissionGuideIsOpen = false
    skippedFullDiskAccess = false
    defaults.removeObject(forKey: Self.completionKey)
    defaults.removeObject(forKey: Self.checkpointKey)
    defaults.removeObject(forKey: FullDiskAccessChoice.defaultsKey)
    flowState = .welcome(archiveBuildHasStarted: false)
  }

  private func setPermissionCheckState(
    _ checkState: PermissionCheckState,
    awaitingReturnFromSystemSettings: Bool
  ) {
    guard case .permission(_, _, let buildStarted) = flowState else { return }
    flowState = .permission(
      checkState: checkState,
      awaitingReturnFromSystemSettings: awaitingReturnFromSystemSettings,
      archiveBuildHasStarted: buildStarted
    )
  }

  private func startPermissionChecks(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    permissionTask?.cancel()
    permissionTask = Task { @MainActor [weak self, weak appModel] in
      // While the person is in System Settings the page stays quiet; it says
      // access is missing only when they come back without it.
      while !Task.isCancelled {
        guard let self, let appModel, case .permission = self.flowState else { return }
        if appModel.checkDiskAccess() == .granted {
          self.setPermissionCheckState(.confirmed, awaitingReturnFromSystemSettings: false)
          return
        }
        try? await Task.sleep(for: .seconds(1))
      }
    }
  }

  private func continueAfterVerifiedAccess(
    appModel: AppModel,
    registeredTrawlers: @escaping @MainActor () -> [RegisteredTrawlerIdentity]
  ) {
    if archiveBuildHasStarted {
      returnToBuilding()
      return
    }
    let currentRegisteredTrawlers = registeredTrawlers()
    guard currentRegisteredTrawlers.isEmpty, appModel.phase == .loading else {
      startInitialUpdate(
        appModel: appModel, registeredTrawlers: currentRegisteredTrawlers)
      return
    }

    permissionTask?.cancel()
    permissionTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.refresh()
      guard !Task.isCancelled, case .permission = self.flowState else { return }
      self.startInitialUpdate(
        appModel: appModel, registeredTrawlers: registeredTrawlers())
    }
  }

  private func verifyAccessByReadingTrawlerArchive(
    appModel: AppModel,
    verificationTrawlers: [RegisteredTrawlerIdentity],
    initialUpdateTrawlers: [RegisteredTrawlerIdentity]
  ) {
    guard updateTask == nil else { return }
    let requestedTrawlers = Set(verificationTrawlers)
    updateTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.updateNow(registeredTrawlers: verificationTrawlers)
      guard !Task.isCancelled else { return }
      let verified = appModel.trawlerArchiveUpdateResults.contains {
        trawlerArchiveUpdateResult in
        requestedTrawlers.contains(trawlerArchiveUpdateResult.registeredTrawler)
          && !appModel.updateOperationFailures.contains { failure in
            failure.failedTrawler == trawlerArchiveUpdateResult.registeredTrawler
          }
      }
      self.updateTask = nil
      if verified {
        self.permissionTask?.cancel()
        self.permissionTask = nil
        self.flowState = .building(needsInitialUpdateResume: false)
        self.saveCheckpoint(.building)
        let remainingTrawlers = initialUpdateTrawlers.filter {
          !requestedTrawlers.contains($0)
        }
        if !remainingTrawlers.isEmpty {
          await appModel.updateNow(registeredTrawlers: remainingTrawlers)
        }
      } else if case .permission(_, let awaitingReturn, _) = self.flowState {
        self.setPermissionCheckState(
          .notConfirmed, awaitingReturnFromSystemSettings: awaitingReturn)
      }
    }
  }

  private func saveCheckpoint(_ stage: OnboardingStage) {
    defaults.set(stage.rawValue, forKey: Self.checkpointKey)
  }
}
