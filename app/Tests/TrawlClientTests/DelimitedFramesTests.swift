import Darwin
import Foundation
import Testing

@testable import TrawlClient

@Test func processClientRejectsEveryFrameAndProcessFailure() async throws {
  let binary = try #require(developmentSyntheticBinary())
  let cases: [(String, TrawlClientError)] = [
    ("process-nonzero", .nonZeroExitBeforeFrame(7)),
    ("frame-missing", .missingFrame),
    ("frame-truncated", .invalidFrame),
    ("frame-oversized", .oversizedFrame),
    ("frame-invalid-protobuf", .invalidProtobuf),
    ("frame-extra", .extraFrame),
  ]
  for (query, expected) in cases {
    await #expect(throws: expected) {
      _ = try await ProcessTrawlClient(binaryURL: binary, receiveReceipt: { _ in })
        .search(query, source: nil)
    }
  }
  await #expect(throws: TrawlClientError.timedOut) {
    _ = try await ProcessTrawlClient(
      binaryURL: binary, searchDeadline: .milliseconds(20), receiveReceipt: { _ in }
    ).search("process-hang", source: nil)
  }
  await #expect(throws: TrawlClientError.timedOut) {
    _ = try await ProcessTrawlClient(
      binaryURL: binary, searchDeadline: .milliseconds(20), receiveReceipt: { _ in }
    ).search("complete-frame-hang", source: nil)
  }
  #expect(
    ProcessTrawlClient.unexpectedTerminationError(
      terminatedBySignal: true, exitCode: SIGTERM, terminationWasRequested: false)
      == .terminatedBySignal(SIGTERM))
  #expect(
    ProcessTrawlClient.unexpectedTerminationError(
      terminatedBySignal: true, exitCode: SIGTERM, terminationWasRequested: true) == nil)
}

@Test func processClientHandlesCancellationAndLaunchFailures() async throws {
  let binary = try #require(developmentSyntheticBinary())
  let task = Task {
    try await ProcessTrawlClient(binaryURL: binary, receiveReceipt: { _ in })
      .search("process-hang", source: nil)
  }
  try await Task.sleep(for: .milliseconds(20))
  task.cancel()
  await #expect(throws: TrawlClientError.cancelled) { _ = try await task.value }

  let missing = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
  await #expect(throws: TrawlClientError.helperMissing) {
    _ = try await ProcessTrawlClient(binaryURL: missing).search("synthetic", source: nil)
  }

  let directory = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
  try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
  defer { try? FileManager.default.removeItem(at: directory) }
  await #expect(throws: TrawlClientError.launchFailed) {
    _ = try await ProcessTrawlClient(binaryURL: directory).search("synthetic", source: nil)
  }
}

@Test func delimitedFramesRejectEveryInvalidShape() throws {
  var response = Trawl_Federation_V1_SearchResponse()
  response.outcome = .complete
  response.order = .recency
  let frame = try DelimitedFrames.encode(response)
  #expect(try DelimitedFrames.decodeExactlyOne(frame) == response.serializedData())
  #expect(throws: TrawlClientError.missingFrame) { try DelimitedFrames.decodeExactlyOne(Data()) }
  #expect(throws: TrawlClientError.invalidFrame) {
    try DelimitedFrames.decodeExactlyOne(Data([1, 0, 0]))
  }
  #expect(throws: TrawlClientError.oversizedFrame) {
    try DelimitedFrames.decodeExactlyOne(Data([1, 0, 0, 1]))
  }
  #expect(throws: TrawlClientError.extraFrame) {
    try DelimitedFrames.decodeExactlyOne(frame + Data([0]))
  }
}

@Test func processClientStillDecodesTheAppOnlySyncFrame() async throws {
  let binary = try #require(developmentSyntheticBinary())
  let response = try await ProcessTrawlClient(
    binaryURL: binary,
    receiveReceipt: { receipt in
      #expect(receipt.arguments == ["__app", "sync"])
      #expect(!receipt.stdout.isEmpty)
    }
  ).sync()
  #expect(response.outcome == .complete)
  #expect(
    response.sources.map(\.sourceID) == [
      "calendar", "contacts", "gmail", "imessage", "notes", "photos", "telegram", "twitter",
      "whatsapp",
    ])
  #expect(response.failures.isEmpty)
}

@Test func processClientSendsThePrivatePhotosRequest() async throws {
  let binary = try #require(developmentSyntheticBinary())
  let response = try await ProcessTrawlClient(
    binaryURL: binary,
    receiveReceipt: { receipt in
      #expect(receipt.arguments == ["__app", "request-photos"])
      #expect(!receipt.stdout.isEmpty)
    }
  ).requestPhotos()
  #expect(response.outcome == .complete)
  #expect(response.sources.map(\.id) == ["gmail", "notes"])
}

@Test func processClientCarriesTheCanonicalOpenIdentityAndAnchor() async throws {
  let binary = try #require(developmentSyntheticBinary())
  let response = try await ProcessTrawlClient(
    binaryURL: binary,
    receiveReceipt: { receipt in
      #expect(
        receipt.arguments
          == ["__app", "open", "synthetic", "synthetic:record/example-1", "match"])
      #expect(!receipt.stdout.isEmpty)
    }
  ).open(sourceID: "synthetic", ref: "synthetic:record/example-1", anchorID: "match")
  #expect(response.requestedRef == "synthetic:record/example-1")
  #expect(response.requestedAnchorID == "match")
  #expect(response.record?.openRef == "synthetic:record/example-1")
  #expect(response.record?.presentation.primaryAnchorID == "match")

  await #expect(throws: TrawlClientError.invalidProtobuf) {
    _ = try await ProcessTrawlClient(binaryURL: binary)
      .open(sourceID: "synthetic", ref: "short-1", anchorID: "match")
  }
  await #expect(throws: TrawlClientError.invalidProtobuf) {
    _ = try await ProcessTrawlClient(binaryURL: binary)
      .open(
        sourceID: "synthetic", ref: "synthetic:record/example-1",
        anchorID: "matching passage")
  }
}

@Test func processClientCarriesOneBoundedOpaqueResourceFrame() async throws {
  let binary = try #require(developmentSyntheticBinary())
  let resource = try await ProcessTrawlClient(
    binaryURL: binary,
    receiveReceipt: { receipt in
      #expect(
        receipt.arguments
          == ["__app", "resource", "photos", "photos:resource/example-1", "32"])
      #expect(!receipt.stdout.isEmpty)
    }
  ).resource(sourceID: "photos", ref: "photos:resource/example-1", maxBytes: 32)
  #expect(resource.ref == "photos:resource/example-1")
  #expect(resource.contentType == "image/jpeg")
  #expect(resource.data == Data("synthetic resource bytes".utf8))

  await #expect(throws: TrawlClientError.invalidProtobuf) {
    _ = try await ProcessTrawlClient(binaryURL: binary)
      .resource(sourceID: "photos", ref: "/tmp/synthetic.jpg", maxBytes: 32)
  }
  await #expect(throws: TrawlClientError.invalidProtobuf) {
    _ = try await ProcessTrawlClient(binaryURL: binary)
      .resource(
        sourceID: "photos", ref: "photos:resource/example-1",
        maxBytes: ProcessTrawlClient.maximumResourceBytes + 1)
  }
}

private func developmentSyntheticBinary() -> URL? {
  let workingDirectory = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
  return [
    workingDirectory.appending(path: "app/.build/out/Products/Debug/TrawlSynthetic"),
    workingDirectory.appending(path: ".build/out/Products/Debug/TrawlSynthetic"),
  ].first(where: { FileManager.default.isExecutableFile(atPath: $0.path) })
}
