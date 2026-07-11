import Foundation
import PermissionGuide
import Testing
import TrawlClient

@testable import TrawlCore

@MainActor
@Test func appModelKeepsPartialStatusVisible() async {
  let source = SourceStatus(
    id: "gmail",
    name: "Gmail",
    state: "ok",
    summary: "Recently synced.",
    counts: [SourceCount(id: "messages", display: "3 messages")],
    lastSyncedDisplay: "just now",
    archiveBytes: 128
  )
  let second = SourceStatus(
    id: "calendar",
    name: "Calendar",
    state: "ok",
    summary: "Recently synced.",
    counts: [SourceCount(id: "events", display: "2 events")],
    lastSyncedDisplay: "1h ago",
    archiveBytes: 64
  )
  let failure = SourceFailure(
    sourceID: "notes",
    sourceName: "Notes",
    code: .permission,
    message: "Allow notes access.",
    remedy: "Open System Settings."
  )
  let client = StatusClient(
    response: StatusResponse(sources: [source, second], failures: [failure], outcome: .partial)
  )
  let model = AppModel(
    client: client,
    permissionProbe: FullDiskAccessProbe(canaries: [], probePath: { _ in .missing })
  )

  await model.refresh()

  #expect(model.sources == [source, second])
  #expect(model.statusFailures == [failure])
  #expect(model.completion == .partial)
  #expect(model.phase == .ready)
  #expect(model.diskAccess == .undetermined)
}

@MainActor
@Test func appModelLeavesVisibleStateAloneWhenTypedWorkIsCancelled() async {
  let source = SourceStatus(
    id: "gmail",
    name: "Gmail",
    state: "ok",
    summary: "Recently synced.",
    counts: [SourceCount(id: "messages", display: "3 messages")],
    lastSyncedDisplay: "just now",
    archiveBytes: 128
  )
  let client = CancellationClient(response: StatusResponse(sources: [source], outcome: .complete))
  let model = AppModel(
    client: client,
    permissionProbe: FullDiskAccessProbe(canaries: [], probePath: { _ in .missing })
  )
  await model.refresh()

  client.partialSync = true
  await model.syncNow()
  #expect(model.syncMessage == "Some sources could not sync.")

  client.partialSync = false
  client.cancelled = true
  await model.refresh()
  await model.syncNow()

  #expect(model.phase == .ready)
  #expect(model.sources == [source])
  #expect(model.statusFailures.isEmpty)
  #expect(model.syncMessage == "Some sources could not sync.")
  #expect(!model.isSyncing)
}

@Test func artworkLookupIsExplicitAndLimitedToApprovedSources() throws {
  let gmail = try #require(AppStoreArtwork.lookupURL(for: "gmail"))
  let twitter = try #require(AppStoreArtwork.lookupURL(for: "twitter"))

  #expect(gmail.host == "itunes.apple.com")
  #expect(gmail.query?.contains("com.google.Gmail") == true)
  #expect(twitter.query?.contains("com.atebits.Tweetie2") == true)
  #expect(AppStoreArtwork.lookupURL(for: "telegram") == nil)
}

@Test func artworkIsDownloadedOnceThenReadFromTheLocalCache() async throws {
  let cache = FileManager.default.temporaryDirectory
    .appendingPathComponent(UUID().uuidString, isDirectory: true)
  defer { try? FileManager.default.removeItem(at: cache) }
  let recorder = URLRecorder()
  let artworkBytes = Data([0x89, 0x50, 0x4e, 0x47])
  let store = AppStoreArtwork(cacheDirectory: cache) { url, maximumBytes in
    await recorder.record(url, maximumBytes: maximumBytes)
    if url.host == "itunes.apple.com" {
      return Data(
        "{\"results\":[{\"artworkUrl512\":\"https://is1-ssl.mzstatic.com/icon.png\"}]}".utf8
      )
    }
    return artworkBytes
  }

  let first = await store.data(for: "gmail")
  let second = await store.data(for: "gmail")

  #expect(first == artworkBytes)
  #expect(second == artworkBytes)
  #expect(await recorder.count == 2)
}

@Test func artworkLookupCannotRedirectToAnUnapprovedHost() async {
  let cache = FileManager.default.temporaryDirectory
    .appendingPathComponent(UUID().uuidString, isDirectory: true)
  defer { try? FileManager.default.removeItem(at: cache) }
  let recorder = URLRecorder()
  let store = AppStoreArtwork(cacheDirectory: cache) { url, maximumBytes in
    await recorder.record(url, maximumBytes: maximumBytes)
    return Data(
      "{\"results\":[{\"artworkUrl512\":\"https://example.com/icon.png\"}]}".utf8
    )
  }

  #expect(await store.data(for: "gmail") == nil)
  #expect(await recorder.count == 1)
}

@Test func artworkDownloadRejectsCrossHostAndInsecureRedirects() throws {
  let initial = try #require(URL(string: "https://is1-ssl.mzstatic.com/icon.png"))
  let sameHost = try #require(URL(string: "https://is2-ssl.mzstatic.com/icon.png"))
  let unapproved = try #require(URL(string: "https://example.com/icon.png"))
  let insecure = try #require(URL(string: "http://is1-ssl.mzstatic.com/icon.png"))

  #expect(AppStoreArtwork.allowsRedirect(from: initial, to: sameHost))
  #expect(!AppStoreArtwork.allowsRedirect(from: initial, to: unapproved))
  #expect(!AppStoreArtwork.allowsRedirect(from: initial, to: insecure))
}

@Test func artworkRequestsCarryTheExactTransferCaps() async {
  let cache = FileManager.default.temporaryDirectory
    .appendingPathComponent(UUID().uuidString, isDirectory: true)
  defer { try? FileManager.default.removeItem(at: cache) }
  let recorder = URLRecorder()
  let store = AppStoreArtwork(cacheDirectory: cache) { url, maximumBytes in
    await recorder.record(url, maximumBytes: maximumBytes)
    if url.host == "itunes.apple.com" {
      return Data(
        "{\"results\":[{\"artworkUrl512\":\"https://is1-ssl.mzstatic.com/icon.png\"}]}".utf8
      )
    }
    return Data([0x89, 0x50, 0x4e, 0x47])
  }

  #expect(await store.data(for: "gmail") == Data([0x89, 0x50, 0x4e, 0x47]))
  #expect(await recorder.maximumBytes == [1_048_576, 5_242_880])
}

private final class StatusClient: TrawlClient, @unchecked Sendable {
  let response: StatusResponse

  init(response: StatusResponse) {
    self.response = response
  }

  func status() async throws -> StatusResponse { response }
  func sync() async throws -> SyncResponse {
    SyncResponse(sources: [], failures: [], outcome: .complete)
  }
  func search(_ query: String, source: String?) async throws -> SearchResponse {
    SearchResponse(hits: [], outcome: .complete, resultLimit: 20, truncated: false)
  }
  func open(_ ref: String) async throws -> OpenResponse {
    OpenResponse(outcome: .complete, sourceID: "", openRef: ref, output: Data(), failure: nil)
  }
}

private final class CancellationClient: TrawlClient, @unchecked Sendable {
  let response: StatusResponse
  private let lock = NSLock()
  private var isCancelled = false
  private var returnsPartialSync = false

  init(response: StatusResponse) {
    self.response = response
  }

  var cancelled: Bool {
    get { lock.withLock { isCancelled } }
    set { lock.withLock { isCancelled = newValue } }
  }

  var partialSync: Bool {
    get { lock.withLock { returnsPartialSync } }
    set { lock.withLock { returnsPartialSync = newValue } }
  }

  func status() async throws -> StatusResponse {
    if cancelled { throw TrawlClientError.cancelled }
    return response
  }

  func sync() async throws -> SyncResponse {
    if cancelled { throw TrawlClientError.cancelled }
    return SyncResponse(
      sources: [],
      failures: [],
      outcome: partialSync ? .partial : .complete
    )
  }

  func search(_ query: String, source: String?) async throws -> SearchResponse {
    SearchResponse(hits: [], outcome: .complete, resultLimit: 20, truncated: false)
  }

  func open(_ ref: String) async throws -> OpenResponse {
    OpenResponse(outcome: .complete, sourceID: "", openRef: ref, output: Data(), failure: nil)
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
