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
@Test func continuingFromTrustPerformsTheFirstAccessCheck() {
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
  let onboarding = OnboardingModel(defaults: defaults)

  onboarding.showTrust()
  #expect(recorder.checkCount == 0)

  onboarding.requestPermission(appModel: appModel, appIDs: { [] })

  #expect(recorder.checkCount == 1)
  #expect(appModel.diskAccess == .granted)
  #expect(onboarding.stage == .syncing)
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
