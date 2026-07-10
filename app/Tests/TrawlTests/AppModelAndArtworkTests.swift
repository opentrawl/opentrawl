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
  let client = StatusClient(
    response: StatusResponse(sources: [source, second], completion: .partial)
  )
  let model = AppModel(
    client: client,
    permissionProbe: FullDiskAccessProbe(canaries: [], probePath: { _ in .missing })
  )

  await model.refresh()

  #expect(model.sources == [source, second])
  #expect(model.completion == .partial)
  #expect(model.phase == .ready)
  #expect(model.diskAccess == .undetermined)
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
  func sync() async throws -> FanoutCompletion { .complete }
  func search(_ query: String, source: String?) async throws -> SearchResponse {
    SearchResponse(hits: [], completion: .complete)
  }
  func open(_ ref: String) async throws -> String { "" }
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
