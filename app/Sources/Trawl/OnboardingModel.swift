import Foundation
import Observation
import PermissionGuide
import TrawlCore

enum OnboardingStage: Sendable, Equatable {
  case welcome
  case trust
  case permission
  case syncing
  case ready
  case agent
  case complete
}

@MainActor
@Observable
final class OnboardingModel {
  static let completionKey = "OpenTrawlOnboardingComplete"

  private let defaults: UserDefaults
  private let permissionGuide: PermissionGuideController
  private var syncTask: Task<Void, Never>?

  private(set) var stage: OnboardingStage

  var isComplete: Bool { stage == .complete }

  init(
    defaults: UserDefaults = .standard,
    permissionGuide: PermissionGuideController = PermissionGuideController()
  ) {
    self.defaults = defaults
    self.permissionGuide = permissionGuide
    stage = defaults.bool(forKey: Self.completionKey) ? .complete : .welcome
  }

  func showTrust() {
    stage = .trust
  }

  func requestPermission(appModel: AppModel, appIDs: [String]) {
    guard appModel.diskAccess != .granted else {
      startInitialSync(appModel: appModel, appIDs: appIDs)
      return
    }
    stage = .permission
    permissionGuide.present(copy: OnboardingStrings.permissionGuideCopy) {
      [weak self, weak appModel] in
      guard let self, let appModel else { return }
      Task { @MainActor in
        await appModel.permissionChanged()
        self.startInitialSync(appModel: appModel, appIDs: appIDs)
      }
    }
  }

  func startInitialSync(appModel: AppModel, appIDs: [String]) {
    syncTask?.cancel()
    stage = .syncing
    syncTask = Task { @MainActor [weak self, weak appModel] in
      guard let self, let appModel else { return }
      await appModel.syncNow(appIDs: appIDs)
      guard !Task.isCancelled else { return }
      self.syncTask = nil
    }
  }

  func stopSync() {
    syncTask?.cancel()
  }

  func showAgent() {
    stage = .agent
  }

  func showReady() {
    stage = .ready
  }

  func complete() {
    defaults.set(true, forKey: Self.completionKey)
    stage = .complete
  }
}
