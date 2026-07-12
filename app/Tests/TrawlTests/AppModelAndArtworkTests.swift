import Foundation
import PermissionGuide
import Testing

@testable import TrawlClient
@testable import TrawlCore

private struct StatusClient: TrawlClient {
  let response: StatusResponse
  func status() async throws -> StatusResponse { response }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String) async throws -> OpenResponse { fatalError() }
}

@Test func restingCopyUsesOnlyTheFirstFourHumanHeadlines() throws {
  var manifest = Trawl_Federation_V1_SourceManifest()
  manifest.sourceID = "gmail"
  manifest.surface = "Gmail"
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
}

@MainActor
@Test func appModelKeepsUsefulPartialStatusVisible() async throws {
  var source = Trawl_Federation_V1_SourceStatus()
  source.manifest = .with {
    $0.sourceID = "gmail"
    $0.surface = "Gmail"
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
}

@MainActor
@Test func cancelledRefreshLeavesVisibleStateAlone() async throws {
  var source = Trawl_Federation_V1_SourceStatus()
  source.manifest = .with {
    $0.sourceID = "gmail"
    $0.surface = "Gmail"
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
  #expect(model.syncMessage == "Some sources could not sync.")
  #expect(model.syncResults.map(\.sourceID) == ["gmail"])
  #expect(model.syncFailures.map(\.sourceID) == ["gmail"])
  client.cancelled = true
  await model.syncNow()
  #expect(model.syncMessage == "Some sources could not sync.")
  #expect(model.syncResults.map(\.sourceID) == ["gmail"])
  client.cancelled = false
  client.syncFails = true
  await model.syncNow()
  #expect(model.syncMessage == TrawlClientError.launchFailed.localizedDescription)
  #expect(model.syncResults.isEmpty)
  #expect(model.syncFailures.isEmpty)
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
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String) async throws -> OpenResponse { fatalError() }
}

private final class MutableAppClient: TrawlClient, @unchecked Sendable {
  let statusResponse: StatusResponse
  private let lock = NSLock()
  private var isCancelled = false
  private var returnsPartialSync = false
  private var returnsSyncFailure = false
  init(status: StatusResponse) { statusResponse = status }
  var cancelled: Bool {
    get { lock.withLock { isCancelled } }
    set { lock.withLock { isCancelled = newValue } }
  }
  var partialSync: Bool {
    get { lock.withLock { returnsPartialSync } }
    set { lock.withLock { returnsPartialSync = newValue } }
  }
  var syncFails: Bool {
    get { lock.withLock { returnsSyncFailure } }
    set { lock.withLock { returnsSyncFailure = newValue } }
  }
  func status() async throws -> StatusResponse {
    if cancelled { throw TrawlClientError.cancelled }
    return statusResponse
  }
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
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String) async throws -> OpenResponse { fatalError() }
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
