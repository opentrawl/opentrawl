import AppKit
import Testing

@testable import Trawl
@testable import TrawlClient

@MainActor
@Test func presentationImageLoadsThroughTheBoundedResourceClient() async throws {
  let resource = PresentationResource(
    kind: .image,
    label: "Synthetic photo",
    ref: "photos:resource/synthetic-photo",
    metadata: [],
    anchorID: "photo"
  )
  let recorder = ResourceRequestRecorder()
  let client = ResourceViewClient(
    response: PresentationResourceData(
      ref: resource.ref,
      contentType: "image/png",
      data: try syntheticImageData()
    ),
    recorder: recorder
  )
  let loader = PresentationResourceLoader(client: client)

  guard case .loading = loader.phase else {
    Issue.record("A new image resource must start in its loading state.")
    return
  }
  await loader.load(sourceID: "photos", resource: resource)

  guard case .loaded(let image) = loader.phase else {
    Issue.record("The bounded synthetic image did not render.")
    return
  }
  #expect(image.size == NSSize(width: 2, height: 1))
  #expect(
    await recorder.request
      == ResourceRequest(
        sourceID: "photos",
        ref: resource.ref,
        maxBytes: ProcessTrawlClient.maximumResourceBytes
      )
  )
}

@MainActor
@Test func presentationImageRejectsNonImageResourceData() async {
  let resource = PresentationResource(
    kind: .image,
    label: "Synthetic photo",
    ref: "photos:resource/synthetic-photo",
    metadata: [],
    anchorID: "photo"
  )
  let loader = PresentationResourceLoader(
    client: ResourceViewClient(
      response: PresentationResourceData(
        ref: resource.ref,
        contentType: "text/plain",
        data: Data("not an image".utf8)
      ),
      recorder: ResourceRequestRecorder()
    )
  )

  await loader.load(sourceID: "photos", resource: resource)

  guard case .failed = loader.phase else {
    Issue.record("Non-image resource data must produce the image failure state.")
    return
  }
}

@MainActor
@Test func presentationImageDoesNotReuseAnotherRecordsLoadedImage() async throws {
  let first = PresentationResource(
    kind: .image,
    label: "First synthetic photo",
    ref: "photos:resource/first-synthetic-photo",
    metadata: [],
    anchorID: "photo"
  )
  let loader = PresentationResourceLoader(
    client: ResourceViewClient(
      response: PresentationResourceData(
        ref: first.ref,
        contentType: "image/png",
        data: try syntheticImageData()
      ),
      recorder: ResourceRequestRecorder()
    )
  )

  await loader.load(sourceID: "photos", resource: first)

  guard case .loaded = loader.visiblePhase(for: first.ref) else {
    Issue.record("The loaded image was not visible for its own resource.")
    return
  }
  guard case .loading = loader.visiblePhase(for: "photos:resource/second-synthetic-photo") else {
    Issue.record("A new record reused the previous record's image.")
    return
  }
}

@MainActor
@Test func presentationImageCanRetryAfterAResourceFailure() async throws {
  let resource = PresentationResource(
    kind: .image,
    label: "Synthetic photo",
    ref: "photos:resource/retry-synthetic-photo",
    metadata: [],
    anchorID: "photo"
  )
  let responses = RetryResourceResponses(
    response: PresentationResourceData(
      ref: resource.ref,
      contentType: "image/png",
      data: try syntheticImageData()
    )
  )
  let loader = PresentationResourceLoader(client: RetryResourceViewClient(responses: responses))

  await loader.load(sourceID: "photos", resource: resource)
  guard case .failed = loader.phase else {
    Issue.record("The first synthetic request did not expose the failure state.")
    return
  }

  await loader.load(sourceID: "photos", resource: resource)
  guard case .loaded = loader.phase else {
    Issue.record("Retrying did not replace the failure with the loaded image.")
    return
  }
  #expect(await responses.requestCount == 2)
}

@MainActor
@Test func lateImageRetryDoesNotOverwriteTheNextRecordsLoad() async throws {
  let first = PresentationResource(
    kind: .image,
    label: "First synthetic photo",
    ref: "photos:resource/first-synthetic-photo",
    metadata: [],
    anchorID: "photo"
  )
  let second = PresentationResource(
    kind: .image,
    label: "Second synthetic photo",
    ref: "photos:resource/second-synthetic-photo",
    metadata: [],
    anchorID: "photo"
  )
  let responses = OverlappingResourceResponses(firstRef: first.ref)
  let loader = PresentationResourceLoader(
    client: OverlappingResourceViewClient(responses: responses)
  )

  await loader.load(sourceID: "photos", resource: first)
  guard case .failed = loader.phase else {
    Issue.record("The first request must fail before its retry starts.")
    return
  }

  let lateRetry = Task { await loader.load(sourceID: "photos", resource: first) }
  await responses.waitUntilRequested(first.ref)
  let nextLoad = Task { await loader.load(sourceID: "photos", resource: second) }
  await responses.waitUntilRequested(second.ref)

  await responses.resolve(
    ref: first.ref,
    with: PresentationResourceData(
      ref: first.ref,
      contentType: "image/png",
      data: try syntheticImageData()
    )
  )
  await lateRetry.value
  guard case .loading = loader.visiblePhase(for: second.ref) else {
    Issue.record("A late retry replaced the next record's loading state.")
    return
  }

  await responses.resolve(
    ref: second.ref,
    with: PresentationResourceData(
      ref: second.ref,
      contentType: "image/png",
      data: try syntheticImageData()
    )
  )
  await nextLoad.value
  guard case .loaded = loader.visiblePhase(for: second.ref) else {
    Issue.record("The next record did not finish loading its own image.")
    return
  }
}

private struct ResourceRequest: Sendable, Equatable {
  let sourceID: String
  let ref: String
  let maxBytes: UInt32
}

private actor ResourceRequestRecorder {
  private(set) var request: ResourceRequest?

  func record(sourceID: String, ref: String, maxBytes: UInt32) {
    request = ResourceRequest(sourceID: sourceID, ref: ref, maxBytes: maxBytes)
  }
}

private struct ResourceViewClient: TrawlClient {
  let response: PresentationResourceData
  let recorder: ResourceRequestRecorder

  func status() async throws -> StatusResponse { fatalError() }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
  func resource(sourceID: String, ref: String, maxBytes: UInt32) async throws
    -> PresentationResourceData
  {
    await recorder.record(sourceID: sourceID, ref: ref, maxBytes: maxBytes)
    return response
  }
}

private actor RetryResourceResponses {
  let response: PresentationResourceData
  private(set) var requestCount = 0

  init(response: PresentationResourceData) {
    self.response = response
  }

  func next() throws -> PresentationResourceData {
    requestCount += 1
    if requestCount == 1 { throw TrawlClientError.invalidProtobuf }
    return response
  }
}

private struct RetryResourceViewClient: TrawlClient {
  let responses: RetryResourceResponses

  func status() async throws -> StatusResponse { fatalError() }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
  func resource(sourceID _: String, ref _: String, maxBytes _: UInt32) async throws
    -> PresentationResourceData
  {
    try await responses.next()
  }
}

private actor OverlappingResourceResponses {
  private let firstRef: String
  private var failedFirstRequest = false
  private var pendingResponses: [String: CheckedContinuation<PresentationResourceData, Never>] = [:]
  private var requestWaiters: [String: CheckedContinuation<Void, Never>] = [:]

  init(firstRef: String) {
    self.firstRef = firstRef
  }

  func next(for ref: String) async throws -> PresentationResourceData {
    if ref == firstRef, !failedFirstRequest {
      failedFirstRequest = true
      throw TrawlClientError.invalidProtobuf
    }
    requestWaiters.removeValue(forKey: ref)?.resume()
    return await withCheckedContinuation { continuation in
      pendingResponses[ref] = continuation
    }
  }

  func waitUntilRequested(_ ref: String) async {
    guard pendingResponses[ref] == nil else { return }
    await withCheckedContinuation { continuation in
      requestWaiters[ref] = continuation
    }
  }

  func resolve(ref: String, with response: PresentationResourceData) {
    pendingResponses.removeValue(forKey: ref)?.resume(returning: response)
  }
}

private struct OverlappingResourceViewClient: TrawlClient {
  let responses: OverlappingResourceResponses

  func status() async throws -> StatusResponse { fatalError() }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
  func resource(sourceID _: String, ref: String, maxBytes _: UInt32) async throws
    -> PresentationResourceData
  {
    try await responses.next(for: ref)
  }
}

@MainActor
private func syntheticImageData() throws -> Data {
  let representation = try #require(
    NSBitmapImageRep(
      bitmapDataPlanes: nil,
      pixelsWide: 2,
      pixelsHigh: 1,
      bitsPerSample: 8,
      samplesPerPixel: 4,
      hasAlpha: true,
      isPlanar: false,
      colorSpaceName: .deviceRGB,
      bytesPerRow: 0,
      bitsPerPixel: 0
    )
  )
  representation.setColor(
    NSColor(deviceRed: 0.1, green: 0.4, blue: 0.9, alpha: 1),
    atX: 0,
    y: 0
  )
  representation.setColor(
    NSColor(deviceRed: 0.9, green: 0.4, blue: 0.1, alpha: 1),
    atX: 1,
    y: 0
  )
  return try #require(representation.representation(using: .png, properties: [:]))
}
