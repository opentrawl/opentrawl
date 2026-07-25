import Foundation
import PermissionGuide
import Testing

@testable import Trawl
@testable import TrawlClient
@testable import TrawlCore

@MainActor
@Test func ordinaryRefreshDoesNotCheckProtectedSourceAccess() async {
  let recorder = PermissionProbeRecorder(outcome: .readable)
  let model = AppModel(
    client: PermissionFlowClient(),
    permissionProbe: FullDiskAccessProbe(
      canaries: [URL(fileURLWithPath: "/synthetic/protected")],
      probePath: recorder.probe
    )
  )

  await model.refresh()

  #expect(recorder.checkCount == 0)
  #expect(model.diskAccess == .undetermined)
}

@MainActor
@Test func openingFullDiskAccessDoesNotAdvanceWithoutVerifiedAccess() {
  let recorder = PermissionProbeRecorder(outcome: .permissionDenied)
  let appModel = AppModel(
    client: PermissionFlowClient(),
    permissionProbe: FullDiskAccessProbe(
      canaries: [URL(fileURLWithPath: "/synthetic/protected")],
      probePath: recorder.probe
    )
  )
  let suite = "PermissionFlowTests.\(UUID().uuidString)"
  let defaults = UserDefaults(suiteName: suite)!
  defer { defaults.removePersistentDomain(forName: suite) }
  var settingsOpenCount = 0
  let onboarding = OnboardingModel(
    defaults: defaults,
    openFullDiskAccess: { settingsOpenCount += 1 }
  )

  onboarding.showPermission()
  #expect(recorder.checkCount == 0)

  onboarding.requestPermission(appModel: appModel, appIDs: { [] })
  onboarding.checkPermission(appModel: appModel, appIDs: [])

  #expect(settingsOpenCount == 1)
  #expect(recorder.checkCount >= 1)
  #expect(appModel.diskAccess == .denied)
  #expect(onboarding.permissionCheck == .notConfirmed)
  #expect(onboarding.stage == .permission)
  onboarding.showWelcome()
}

@MainActor
@Test func onlyVerifiedAccessAdvancesToArchiveBuilding() async {
  let recorder = PermissionProbeRecorder(outcome: .readable)
  let appModel = AppModel(
    client: PermissionFlowClient(),
    permissionProbe: FullDiskAccessProbe(
      canaries: [URL(fileURLWithPath: "/synthetic/protected")],
      probePath: recorder.probe
    )
  )
  let suite = "PermissionFlowTests.\(UUID().uuidString)"
  let defaults = UserDefaults(suiteName: suite)!
  defer { defaults.removePersistentDomain(forName: suite) }
  let onboarding = OnboardingModel(defaults: defaults, openFullDiskAccess: {})

  await appModel.refresh()
  onboarding.showPermission()
  onboarding.checkPermission(appModel: appModel, appIDs: [])

  #expect(recorder.checkCount == 1)
  #expect(appModel.diskAccess == .granted)
  #expect(onboarding.stage == .building)
}

@MainActor
@Test func grantedAccessWaitsForSourceStatusBeforeStartingTheBuild() {
  let appModel = AppModel(
    client: PermissionFlowClient(),
    permissionProbe: FullDiskAccessProbe(
      canaries: [URL(fileURLWithPath: "/synthetic/protected")],
      probePath: { _ in .readable }
    )
  )
  let suite = "PermissionFlowTests.\(UUID().uuidString)"
  let defaults = UserDefaults(suiteName: suite)!
  defer { defaults.removePersistentDomain(forName: suite) }
  let onboarding = OnboardingModel(defaults: defaults, openFullDiskAccess: {})

  onboarding.showPermission()
  onboarding.checkPermission(appModel: appModel, appIDs: [])

  #expect(appModel.phase == .loading)
  #expect(appModel.diskAccess == .granted)
  #expect(onboarding.permissionCheck == .checking)
  #expect(onboarding.stage == .permission)
}

@MainActor
@Test func undeterminedAccessCannotAdvanceBeforeSourceStatusLoads() {
  let appModel = AppModel(
    client: PermissionFlowClient(),
    permissionProbe: FullDiskAccessProbe(canaries: [], probePath: { _ in .missing })
  )
  let suite = "PermissionFlowTests.\(UUID().uuidString)"
  let defaults = UserDefaults(suiteName: suite)!
  defer { defaults.removePersistentDomain(forName: suite) }
  let onboarding = OnboardingModel(defaults: defaults, openFullDiskAccess: {})

  onboarding.showPermission()
  onboarding.checkPermission(appModel: appModel, appIDs: ["notes"])

  #expect(appModel.phase == .loading)
  #expect(onboarding.permissionCheck == .notConfirmed)
  #expect(onboarding.stage == .permission)
}

@MainActor
@Test func successfulSourceReadVerifiesAccessWhenNoCanaryExists() async throws {
  let appModel = AppModel(
    client: SuccessfulPermissionFlowClient(),
    permissionProbe: FullDiskAccessProbe(
      canaries: [],
      probePath: { _ in .missing }
    )
  )
  let suite = "PermissionFlowTests.\(UUID().uuidString)"
  let defaults = UserDefaults(suiteName: suite)!
  defer { defaults.removePersistentDomain(forName: suite) }
  let onboarding = OnboardingModel(defaults: defaults, openFullDiskAccess: {})

  await appModel.refresh()
  onboarding.showPermission()
  onboarding.checkPermission(appModel: appModel, appIDs: ["notes"])

  try await confirmation { confirmed in
    while onboarding.stage == .permission {
      try await Task.sleep(for: .milliseconds(10))
    }
    confirmed()
  }
  #expect(appModel.diskAccess == .undetermined)
  #expect(onboarding.stage == .building)
}

@MainActor
@Test func returningFromSystemSettingsRechecksAccess() {
  let recorder = MutablePermissionProbeRecorder(outcome: .permissionDenied)
  let appModel = AppModel(
    client: PermissionFlowClient(),
    permissionProbe: FullDiskAccessProbe(
      canaries: [URL(fileURLWithPath: "/synthetic/protected")],
      probePath: recorder.probe
    )
  )
  let suite = "PermissionFlowTests.\(UUID().uuidString)"
  let defaults = UserDefaults(suiteName: suite)!
  defer { defaults.removePersistentDomain(forName: suite) }
  let onboarding = OnboardingModel(defaults: defaults, openFullDiskAccess: {})

  onboarding.showPermission()
  onboarding.applicationDidBecomeActive(appModel: appModel, appIDs: { ["notes"] })
  #expect(onboarding.stage == .permission)
  #expect(onboarding.permissionCheck == .notConfirmed)

  recorder.outcome = .readable
  onboarding.applicationDidBecomeActive(appModel: appModel, appIDs: { ["notes"] })
  #expect(onboarding.stage == .building)
  #expect(appModel.diskAccess == .granted)
}

private struct PermissionFlowClient: TrawlClient {
  func status() async throws -> StatusResponse {
    StatusResponse(sources: [], failures: [], skippedSources: [], outcome: .complete)
  }

  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}

private struct SuccessfulPermissionFlowClient: TrawlClient {
  func status() async throws -> StatusResponse {
    let failure = SourceFailure(
      sourceID: "notes",
      sourceName: "Notes",
      code: .permission,
      message: "Synthetic Full Disk Access failure.",
      remedy: "Open System Settings."
    )
    return StatusResponse(
      sources: [],
      failures: [failure],
      skippedSources: [],
      outcome: .failed
    )
  }

  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func sync(
    sourceIDs: [String],
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse {
    let sourceID = sourceIDs[0]
    let result = SyncSourceResult(
      sourceID: sourceID,
      sourceName: "Notes",
      outcome: .complete,
      failure: nil
    )
    progress(.finished(result))
    return SyncResponse(sources: [result], failures: [], outcome: .complete)
  }

  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}

private final class PermissionProbeRecorder: @unchecked Sendable {
  private let lock = NSLock()
  private let outcome: ProtectedPathOutcome
  private var count = 0

  init(outcome: ProtectedPathOutcome) {
    self.outcome = outcome
  }

  var checkCount: Int {
    lock.withLock { count }
  }

  func probe(_: URL) -> ProtectedPathOutcome {
    lock.withLock { count += 1 }
    return outcome
  }
}

private final class MutablePermissionProbeRecorder: @unchecked Sendable {
  private let lock = NSLock()
  private var protectedOutcome: ProtectedPathOutcome

  init(outcome: ProtectedPathOutcome) {
    protectedOutcome = outcome
  }

  var outcome: ProtectedPathOutcome {
    get { lock.withLock { protectedOutcome } }
    set { lock.withLock { protectedOutcome = newValue } }
  }

  func probe(_: URL) -> ProtectedPathOutcome {
    outcome
  }
}
