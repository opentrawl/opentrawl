import Foundation
import PermissionGuide
import Testing

@testable import TrawlClient
@testable import TrawlCore

private struct StatusClient: TrawlClient {
  let response: StatusResponse
  func status() async throws -> StatusResponse { response }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}

@Test func restingCopyUsesOnlyTheFirstFourHumanHeadlines() throws {
  var manifest = Trawl_Federation_V1_SourceManifest()
  manifest.sourceID = "gmail"
  manifest.displayName = "Gmail"
  manifest.headlines = ["mail", "attachments", "threads", "labels", "ignored"]
  var source = Trawl_Federation_V1_SourceStatus()
  source.manifest = manifest
  source.state = "ok"
  source.lastSyncRfc3339 = "2026-07-12T09:20:00Z"
  source.counts = [
    .with {
      $0.id = "items"
      $0.label = "Items"
      $0.value = 2
    }
  ]
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .complete
  response.sources = [source]
  let model = try response.model().sources[0]
  #expect(SourceRestingCopy.title(for: model) == "Search Gmail")
  #expect(SourceRestingCopy.detail(for: model) == "mail · attachments · threads · labels")
  #expect(SourceRestingCopy.detail(for: model)?.contains("Items") == false)
  #expect(SourceRestingCopy.detail(for: model)?.contains("T09:20:00Z") == false)
  #expect(!SourceRestingCopy.needsAttention(model))
}

@Test func restingCopyKeepsHealthySourcesQuietAndShowsDiagnosticAttention() throws {
  var manifest = Trawl_Federation_V1_SourceManifest()
  manifest.sourceID = "notes"
  manifest.displayName = "Notes"
  var source = Trawl_Federation_V1_SourceStatus()
  source.manifest = manifest
  source.state = "ok"
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .complete
  response.sources = [source]
  let healthy = try response.model().sources[0]
  #expect(SourceRestingCopy.detail(for: healthy) == nil)
  #expect(!SourceRestingCopy.needsAttention(healthy))

  source.warnings = ["Reconnect Notes to search it."]
  response.sources = [source]
  let diagnostic = try response.model().sources[0]
  #expect(SourceRestingCopy.detail(for: diagnostic) == "Reconnect Notes to search it.")
  #expect(SourceRestingCopy.needsAttention(diagnostic))

  source.warnings = []
  source.state = "stale"
  source.summary = "Notes were synced days ago; run trawl sync notes to refresh."
  response.sources = [source]
  let stale = try response.model().sources[0]
  #expect(SourceRestingCopy.detail(for: stale) == nil)
  #expect(!SourceRestingCopy.needsAttention(stale))

  source.state = "missing"
  source.summary = "Notes archive has not been created."
  response.sources = [source]
  let missing = try response.model().sources[0]
  #expect(SourceRestingCopy.detail(for: missing) == "Not set up.")
}

@MainActor
@Test func skippedOnlyStatusIsPartialNotFailed() async throws {
  var skipped = Trawl_Federation_V1_SkippedSource()
  skipped.sourceID = "synthetic"
  skipped.surface = "Synthetic"
  skipped.reason = "Status is not supported."
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .partial
  response.skippedSources = [skipped]
  let model = AppModel(client: StatusClient(response: try response.model()))
  await model.refresh()
  #expect(model.phase == .partial)
  #expect(model.skippedSources.map(\.sourceID) == ["synthetic"])
  #expect(model.restingSources.map(\.id) == ["synthetic"])
  #expect(model.restingSources.first?.detail == "Status is not supported.")
  #expect(model.restingSources.first?.needsAttention == true)
}

@MainActor
@Test func requestingPhotosAppliesTheReturnedAccessStatus() async throws {
  let notRequested = try photosStatus(state: .needsAction, action: .requestPhotos).model()
  let authorised = try photosStatus(state: .ready, action: .none).model()
  let client = PhotosRequestClient(status: notRequested, requestedStatus: authorised)
  let model = AppModel(client: client)

  await model.refresh()
  #expect(model.photosAccess?.action == .requestPhotos)

  await model.requestPhotos()
  #expect(client.didRequestPhotos)
  #expect(model.photosAccess == nil)
}

@MainActor
@Test func failedPhotosRequestKeepsTheRefreshedStatus() async throws {
  let notRequested = try photosStatus(state: .needsAction, action: .requestPhotos).model()
  var failed = photosStatus(state: .needsAction, action: .requestPhotos)
  failed.outcome = .partial
  failed.failures = [
    .with {
      $0.sourceID = "photos"
      $0.surface = "Photos"
      $0.code = .unavailable
      $0.message = "Photos access could not be requested."
      $0.remedy = "Try again from OpenTrawl."
    }
  ]
  let client = PhotosRequestClient(status: notRequested, requestedStatus: try failed.model())
  let model = AppModel(client: client)

  await model.refresh()
  await model.requestPhotos()

  #expect(model.sources.map(\.id) == ["photos"])
  #expect(model.photosAccess?.action == .requestPhotos)
  #expect(model.statusFailures.map(\.sourceID) == ["photos"])
}

@MainActor
@Test func appModelKeepsUsefulPartialStatusVisible() async throws {
  var source = Trawl_Federation_V1_SourceStatus()
  source.manifest = .with {
    $0.sourceID = "gmail"
    $0.displayName = "Gmail"
  }
  source.state = "ok"
  var failure = Trawl_Federation_V1_SourceFailure()
  failure.sourceID = "notes"
  failure.surface = "Notes"
  failure.code = .permission
  failure.message = "Allow Notes access."
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .partial
  response.sources = [source]
  response.failures = [failure]
  let model = AppModel(
    client: StatusClient(response: try response.model()),
    permissionProbe: FullDiskAccessProbe(canaries: [], probePath: { _ in .missing }))
  await model.refresh()
  #expect(model.sources.map(\.id) == ["gmail"])
  #expect(model.statusFailures.map(\.sourceID) == ["notes"])
  #expect(model.phase == .partial)
  #expect(model.restingSources.map(\.id) == ["gmail", "notes"])
  #expect(model.restingSources.map(\.surface) == ["Gmail", "Notes"])
  #expect(model.restingSources[0].detail == nil)
  #expect(model.restingSources[1].detail == "Allow Notes access.")
  #expect(model.restingSources[1].needsAttention)
}

@MainActor
@Test func partialFailureOverridesItsExistingSourceWithoutDuplicatingIt() async throws {
  var source = Trawl_Federation_V1_SourceStatus()
  source.manifest = .with {
    $0.sourceID = "notes"
    $0.displayName = "Notes"
    $0.headlines = ["Search your notes"]
  }
  source.state = "ok"
  var failure = Trawl_Federation_V1_SourceFailure()
  failure.sourceID = "notes"
  failure.surface = "Notes"
  failure.code = .permission
  failure.message = "Allow Notes access."
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .partial
  response.sources = [source]
  response.failures = [failure]
  let model = AppModel(client: StatusClient(response: try response.model()))

  await model.refresh()

  #expect(model.restingSources.map(\.id) == ["notes"])
  #expect(model.restingSources[0].detail == "Allow Notes access.")
  #expect(model.restingSources[0].needsAttention)
}

@MainActor
@Test func totalFailureKeepsItsSourceButtonsInsteadOfUsingTheGenericFallback() async throws {
  var failure = Trawl_Federation_V1_SourceFailure()
  failure.sourceID = "notes"
  failure.surface = "Notes"
  failure.code = .permission
  failure.message = "Allow Notes access."
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .failed
  response.failures = [failure]
  let model = AppModel(client: StatusClient(response: try response.model()))

  await model.refresh()

  #expect(model.phase == .failed("Allow Notes access."))
  #expect(model.restingSources.map(\.id) == ["notes"])
  #expect(!model.shouldShowFailureFallback)
}

@MainActor
@Test func failedRefreshExplainsWhyRetainedSourcesMayBeStale() async throws {
  var source = Trawl_Federation_V1_SourceStatus()
  source.manifest = .with {
    $0.sourceID = "gmail"
    $0.displayName = "Gmail"
  }
  source.state = "ok"
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .complete
  response.sources = [source]
  let client = MutableAppClient(status: try response.model())
  let model = AppModel(client: client)
  await model.refresh()
  #expect(model.phase == .ready)
  #expect(model.statusRefreshFailure == nil)

  client.statusFails = true
  await model.refresh()

  #expect(model.phase == .failed(TrawlClientError.launchFailed.localizedDescription))
  #expect(model.restingSources.map(\.id) == ["gmail"])
  #expect(model.statusRefreshFailure == TrawlClientError.launchFailed.localizedDescription)
  #expect(!model.shouldShowFailureFallback)
}

@MainActor
@Test func cancelledRefreshLeavesVisibleStateAlone() async throws {
  var source = Trawl_Federation_V1_SourceStatus()
  source.manifest = .with {
    $0.sourceID = "gmail"
    $0.displayName = "Gmail"
  }
  source.state = "ok"
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .complete
  response.sources = [source]
  let client = CancellingStatusClient(response: try response.model())
  let model = AppModel(
    client: client, permissionProbe: FullDiskAccessProbe(canaries: [], probePath: { _ in .missing })
  )
  await model.refresh()
  client.cancelled = true
  await model.refresh()
  #expect(model.sources.map(\.id) == ["gmail"])
  #expect(model.phase == .ready)
}

@MainActor
@Test func statusReductionDistinguishesTimeoutFromMixedFailure() async {
  let timeout = SourceFailure(
    sourceID: "calendar", sourceName: "Calendar", code: .timeout, message: "Calendar timed out.",
    remedy: "Try again.")
  let permission = SourceFailure(
    sourceID: "notes", sourceName: "Notes", code: .permission, message: "Allow Notes access.",
    remedy: "Open System Settings.")
  let timedOut = AppModel(
    client: StatusClient(
      response: StatusResponse(
        sources: [], failures: [timeout], skippedSources: [], outcome: .failed)))
  await timedOut.refresh()
  #expect(timedOut.phase == .timedOut)
  let mixed = AppModel(
    client: StatusClient(
      response: StatusResponse(
        sources: [], failures: [timeout, permission], skippedSources: [], outcome: .failed)))
  await mixed.refresh()
  #expect(mixed.phase == .failed(timeout.message))
}

@MainActor
@Test func cancelledAndFailedSyncKeepTruthfulVisibleState() async {
  let status = StatusResponse(sources: [], failures: [], skippedSources: [], outcome: .complete)
  let client = MutableAppClient(status: status)
  let model = AppModel(client: client)
  client.partialSync = true
  await model.syncNow()
  #expect(model.syncMessage == "Some apps could not sync.")
  #expect(model.syncProgress["gmail"] == .failed("Synthetic sync failed."))
  #expect(model.syncResults.map(\.sourceID) == ["gmail"])
  #expect(model.syncFailures.map(\.sourceID) == ["gmail"])
  client.cancelled = true
  await model.syncNow()
  #expect(model.syncMessage == "Some apps could not sync.")
  #expect(model.syncResults.map(\.sourceID) == ["gmail"])
  client.cancelled = false
  client.syncFails = true
  await model.syncNow()
  #expect(model.syncMessage == TrawlClientError.launchFailed.localizedDescription)
  #expect(model.syncResults.isEmpty)
  #expect(model.syncFailures.isEmpty)
}

@MainActor
@Test func onlyAutomaticFailuresBackOffAndSuccessResetsTheDelay() async {
  let status = StatusResponse(sources: [], failures: [], skippedSources: [], outcome: .complete)
  let client = MutableAppClient(status: status)
  let model = AppModel(client: client)
  client.partialSync = true

  await model.syncNow()
  #expect(model.automaticSyncDelay == .seconds(3_600))
  await model.syncNow(trigger: .automatic)
  #expect(model.automaticSyncDelay == .seconds(7_200))
  await model.syncNow(trigger: .automatic)
  #expect(model.automaticSyncDelay == .seconds(14_400))
  await model.syncNow(trigger: .automatic)
  #expect(model.automaticSyncDelay == .seconds(28_800))
  await model.syncNow(trigger: .automatic)
  #expect(model.automaticSyncDelay == .seconds(28_800))

  client.partialSync = false
  await model.syncNow(trigger: .automatic)
  #expect(model.automaticSyncFailureCount == 0)
  #expect(model.automaticSyncDelay == .seconds(3_600))
}

@MainActor
@Test func syncUsesTheRequestedAppsInOrder() async {
  let client = MutableAppClient(
    status: StatusResponse(sources: [], failures: [], skippedSources: [], outcome: .complete))
  let model = AppModel(client: client)
  let appIDs = ["imessage", "whatsapp", "telegram", "notes", "contacts"]

  await model.syncNow(appIDs: appIDs)

  #expect(client.requestedAppIDs == appIDs)
  #expect(model.syncProgress.keys.sorted() == appIDs.sorted())
}

@Test func artworkLookupIsExplicitAndLimitedToApprovedSources() throws {
  let gmail = try #require(AppStoreArtwork.lookupURL(for: "gmail"))
  let twitter = try #require(AppStoreArtwork.lookupURL(for: "twitter"))
  #expect(gmail.host == "itunes.apple.com")
  #expect(gmail.query?.contains("com.google.Gmail") == true)
  #expect(twitter.query?.contains("com.atebits.Tweetie2") == true)
  #expect(AppStoreArtwork.lookupURL(for: "telegram") == nil)
}

@Test func artworkIsDownloadedOnceThenReadFromTheLocalCache() async {
  let cache = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
  defer { try? FileManager.default.removeItem(at: cache) }
  let recorder = URLRecorder()
  let bytes = Data([0x89, 0x50, 0x4e, 0x47])
  let store = AppStoreArtwork(cacheDirectory: cache) { url, maximumBytes in
    await recorder.record(url, maximumBytes: maximumBytes)
    return url.host == "itunes.apple.com"
      ? Data("{\"results\":[{\"artworkUrl512\":\"https://is1-ssl.mzstatic.com/icon.png\"}]}".utf8)
      : bytes
  }
  #expect(await store.data(for: "gmail") == bytes)
  #expect(await store.data(for: "gmail") == bytes)
  #expect(await recorder.count == 2)
  #expect(await recorder.maximumBytes == [1_048_576, 5_242_880])
}

@Test func artworkLookupRejectsUnapprovedRedirects() async {
  let cache = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
  defer { try? FileManager.default.removeItem(at: cache) }
  let store = AppStoreArtwork(cacheDirectory: cache) { _, _ in
    Data("{\"results\":[{\"artworkUrl512\":\"https://example.com/icon.png\"}]}".utf8)
  }
  #expect(await store.data(for: "gmail") == nil)
}

@Test func artworkRedirectPolicyRequiresHTTPSAndApprovedHosts() throws {
  let initial = try #require(URL(string: "https://is1-ssl.mzstatic.com/icon.png"))
  let sameFamily = try #require(URL(string: "https://is2-ssl.mzstatic.com/icon.png"))
  let unapproved = try #require(URL(string: "https://example.com/icon.png"))
  let insecure = try #require(URL(string: "http://is1-ssl.mzstatic.com/icon.png"))
  #expect(AppStoreArtwork.allowsRedirect(from: initial, to: sameFamily))
  #expect(!AppStoreArtwork.allowsRedirect(from: initial, to: unapproved))
  #expect(!AppStoreArtwork.allowsRedirect(from: initial, to: insecure))
}

private final class CancellingStatusClient: TrawlClient, @unchecked Sendable {
  let response: StatusResponse
  private let lock = NSLock()
  private var isCancelled = false
  init(response: StatusResponse) { self.response = response }
  var cancelled: Bool {
    get { lock.withLock { isCancelled } }
    set { lock.withLock { isCancelled = newValue } }
  }
  func status() async throws -> StatusResponse {
    if cancelled { throw TrawlClientError.cancelled }
    return response
  }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}

private final class MutableAppClient: TrawlClient, @unchecked Sendable {
  let statusResponse: StatusResponse
  private let lock = NSLock()
  private var isCancelled = false
  private var returnsStatusFailure = false
  private var returnsPartialSync = false
  private var returnsSyncFailure = false
  private var lastRequestedAppIDs: [String] = []
  init(status: StatusResponse) { statusResponse = status }
  var cancelled: Bool {
    get { lock.withLock { isCancelled } }
    set { lock.withLock { isCancelled = newValue } }
  }
  var partialSync: Bool {
    get { lock.withLock { returnsPartialSync } }
    set { lock.withLock { returnsPartialSync = newValue } }
  }
  var statusFails: Bool {
    get { lock.withLock { returnsStatusFailure } }
    set { lock.withLock { returnsStatusFailure = newValue } }
  }
  var syncFails: Bool {
    get { lock.withLock { returnsSyncFailure } }
    set { lock.withLock { returnsSyncFailure = newValue } }
  }
  var requestedAppIDs: [String] { lock.withLock { lastRequestedAppIDs } }
  func status() async throws -> StatusResponse {
    if cancelled { throw TrawlClientError.cancelled }
    if statusFails { throw TrawlClientError.launchFailed }
    return statusResponse
  }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse {
    if cancelled { throw TrawlClientError.cancelled }
    if syncFails { throw TrawlClientError.launchFailed }
    let failure = SourceFailure(
      sourceID: "gmail", sourceName: "Gmail", code: .unavailable, message: "Synthetic sync failed.",
      remedy: "Try again.")
    return partialSync
      ? SyncResponse(
        sources: [
          SyncSourceResult(
            sourceID: "gmail", sourceName: "Gmail", outcome: .partial, failure: failure)
        ], failures: [failure], outcome: .partial)
      : SyncResponse(sources: [], failures: [], outcome: .complete)
  }
  func sync(progress: @escaping @Sendable (SyncProgress) -> Void) async throws -> SyncResponse {
    if partialSync { progress(.started(sourceID: "gmail", sourceName: "Gmail")) }
    let response = try await sync()
    for source in response.sources {
      progress(.finished(source))
    }
    return response
  }
  func sync(
    sourceIDs: [String], progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse {
    lock.withLock { lastRequestedAppIDs = sourceIDs }
    return try await sync(progress: progress)
  }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}

private final class PhotosRequestClient: TrawlClient, @unchecked Sendable {
  let statusResponse: StatusResponse
  let requestedStatus: StatusResponse
  private(set) var didRequestPhotos = false

  init(status: StatusResponse, requestedStatus: StatusResponse) {
    self.statusResponse = status
    self.requestedStatus = requestedStatus
  }

  func status() async throws -> StatusResponse { statusResponse }
  func requestPhotos() async throws -> StatusResponse {
    didRequestPhotos = true
    return requestedStatus
  }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}

private func photosStatus(
  state: Trawl_Federation_V1_SetupState,
  action: Trawl_Federation_V1_SetupActionKind
) -> Trawl_Federation_V1_StatusResponse {
  .with {
    $0.outcome = .complete
    $0.sources = [
      .with {
        $0.manifest = .with {
          $0.sourceID = "photos"
          $0.displayName = "Photos"
        }
        $0.state = "ok"
        $0.setupRequirements = [
          .with {
            $0.id = "photos_access"
            $0.kind = .photosPermission
            $0.state = state
            $0.explanation = "Synthetic Photos permission state."
            $0.action = action
          }
        ]
      }
    ]
  }
}

private actor URLRecorder {
  private var urls: [URL] = []
  private var limits: [Int] = []
  var count: Int { urls.count }
  var maximumBytes: [Int] { limits }
  func record(_ url: URL, maximumBytes: Int) {
    urls.append(url)
    limits.append(maximumBytes)
  }
}
