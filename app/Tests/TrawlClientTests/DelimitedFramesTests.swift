import Darwin
import Foundation
import Testing

@testable import TrawlClient

@Test func processClientRejectsEveryFrameAndProcessFailure() async throws {
  let cases: [(Data, TrawlClientError)] = [
    (Data(), .missingFrame),
    (Data([1, 0, 0]), .invalidFrame),
    (Data([1, 0, 0, 1]), .oversizedFrame),
    (Data([1, 0, 0, 0, 255]), .invalidProtobuf),
    (Data([2, 0, 0, 0, 8, 1, 0]), .extraFrame),
  ]
  for (output, expected) in cases {
    let helper = try framedHelper(commands: ["search": output])
    defer { helper.remove() }
    await #expect(throws: expected) {
      _ = try await ProcessTrawlClient(binaryURL: helper.binary, receiveReceipt: { _ in })
        .search("query", source: nil)
    }
  }

  let nonzero = try temporaryHelper(script: "exit 7")
  defer { nonzero.remove() }
  await #expect(throws: TrawlClientError.nonZeroExitBeforeFrame(7)) {
    _ = try await ProcessTrawlClient(binaryURL: nonzero.binary, receiveReceipt: { _ in })
      .search("query", source: nil)
  }

  let hanging = try temporaryHelper(script: "sleep 30")
  defer { hanging.remove() }
  await #expect(throws: TrawlClientError.timedOut) {
    _ = try await ProcessTrawlClient(
      binaryURL: hanging.binary, searchDeadline: .milliseconds(20), receiveReceipt: { _ in }
    ).search("query", source: nil)
  }

  let completeThenHang = try framedHelper(
    commands: ["search": try completeSearchFrame()], trailing: "sleep 30")
  defer { completeThenHang.remove() }
  await #expect(throws: TrawlClientError.timedOut) {
    _ = try await ProcessTrawlClient(
      binaryURL: completeThenHang.binary, searchDeadline: .milliseconds(20),
      receiveReceipt: { _ in }
    ).search("query", source: nil)
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
  let hanging = try temporaryHelper(script: "sleep 30")
  defer { hanging.remove() }
  let task = Task {
    try await ProcessTrawlClient(binaryURL: hanging.binary, receiveReceipt: { _ in })
      .search("query", source: nil)
  }
  try await Task.sleep(for: .milliseconds(20))
  task.cancel()
  await #expect(throws: TrawlClientError.cancelled) { _ = try await task.value }

  let missing = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
  await #expect(throws: TrawlClientError.helperMissing) {
    _ = try await ProcessTrawlClient(binaryURL: missing).search("query", source: nil)
  }

  let directory = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
  try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
  defer { try? FileManager.default.removeItem(at: directory) }
  await #expect(throws: TrawlClientError.launchFailed) {
    _ = try await ProcessTrawlClient(binaryURL: directory).search("query", source: nil)
  }
}

@Test func processClientReapsTimedOutHelpersAcrossRepeatedCalls() async throws {
  let hanging = try temporaryHelper(script: "sleep 30")
  defer { hanging.remove() }
  for _ in 0..<8 {
    await #expect(throws: TrawlClientError.timedOut) {
      _ = try await ProcessTrawlClient(
        binaryURL: hanging.binary, searchDeadline: .milliseconds(20), receiveReceipt: { _ in }
      ).search("query", source: nil)
    }
  }
}

@Test func delimitedFramesRejectEveryInvalidShape() throws {
  let frame = try completeSearchFrame()
  var expected = Trawl_Federation_V1_SearchResponse()
  expected.outcome = .complete
  expected.order = .recency
  expected.resultLimit = 20
  #expect(try DelimitedFrames.decodeExactlyOne(frame) == expected.serializedData())
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

@Test func processClientDecodesAFramedSyncResponse() async throws {
  let helper = try framedHelper(commands: ["status": try statusFrame(), "sync": try syncFrame()])
  defer { helper.remove() }
  let progress = SyncProgressRecorder()
  let response = try await ProcessTrawlClient(
    binaryURL: helper.binary,
    receiveReceipt: { receipt in
      if receipt.arguments[1] == "status" {
        #expect(receipt.arguments == ["__app", "status"])
      } else {
        #expect(receipt.arguments == ["__app", "sync", "--source", "gmail"])
      }
      #expect(!receipt.stdout.isEmpty)
    }
  ).sync { progress.append($0) }
  #expect(response.outcome == .complete)
  #expect(response.sources.map(\.sourceID) == ["gmail"])
  #expect(
    progress.values == [
      .started(sourceID: "gmail", sourceName: "Gmail"),
      .finished(response.sources[0]),
    ])
}

@Test func processClientSyncsOnlySelectedSources() async throws {
  let helper = try framedHelper(commands: ["status": try statusFrame(), "sync": try syncFrame()])
  defer { helper.remove() }
  let progress = SyncProgressRecorder()
  let response = try await ProcessTrawlClient(
    binaryURL: helper.binary,
    receiveReceipt: { receipt in
      if receipt.arguments[1] == "sync" {
        #expect(receipt.arguments == ["__app", "sync", "--source", "gmail"])
      }
    }
  ).sync(sourceIDs: ["gmail", "gmail"]) { progress.append($0) }

  #expect(response.sources.map(\.sourceID) == ["gmail"])
  #expect(
    progress.values == [
      .started(sourceID: "gmail", sourceName: "Gmail"),
      .finished(response.sources[0]),
    ])
}

@Test func processClientDownloadsTelegramHistoryThroughTheSameSyncSurface() async throws {
  let helper = try framedHelper(commands: ["sync": try syncFrame()])
  defer { helper.remove() }
  let response = try await ProcessTrawlClient(
    binaryURL: helper.binary,
    receiveReceipt: { receipt in
      #expect(
        receipt.arguments
          == ["__app", "sync", "--source", "telegram", "--full-history"])
    }
  ).downloadTelegramMessageHistory { _ in }

  #expect(response.outcome == .complete)
}

@Test func defaultClientNeverTurnsScopedSyncIntoUnscopedSideEffects() async {
  await #expect(throws: TrawlClientError.scopedSyncUnsupported) {
    _ = try await UnscopedTestClient().sync(sourceIDs: ["gmail"])
  }
}

@Test func processClientCarriesTheScopedSearchArgumentsAndResponse() async throws {
  let helper = try framedHelper(commands: ["search": try searchFrame(outcome: .complete)])
  defer { helper.remove() }
  let response = try await ProcessTrawlClient(
    binaryURL: helper.binary,
    receiveReceipt: { receipt in
      #expect(receipt.arguments == ["__app", "search", "--source", "gmail", "hello"])
      #expect(!receipt.stdout.isEmpty)
    }
  ).search("hello", source: "gmail")

  #expect(response.sources.map(\.sourceID) == ["gmail"])
  #expect(response.hits.map(\.sourceID) == ["gmail"])
  #expect(response.hits.map(\.openRef) == ["gmail:message/example-1"])
}

@Test func processClientSendsThePrivatePhotosRequest() async throws {
  let helper = try framedHelper(commands: ["request-photos": try statusFrame()])
  defer { helper.remove() }
  let response = try await ProcessTrawlClient(
    binaryURL: helper.binary,
    receiveReceipt: { receipt in
      #expect(receipt.arguments == ["__app", "request-photos"])
      #expect(!receipt.stdout.isEmpty)
    }
  ).requestPhotos()
  #expect(response.outcome == .complete)
  #expect(response.sources.map(\.id) == ["gmail"])
}

@Test func processClientCarriesTheCanonicalOpenIdentityAndAnchor() async throws {
  let helper = try framedHelper(commands: ["open": try openFrame()])
  defer { helper.remove() }
  let response = try await ProcessTrawlClient(
    binaryURL: helper.binary,
    receiveReceipt: { receipt in
      #expect(
        receipt.arguments == ["__app", "open", "gmail", "gmail:record/example-1", "match"])
      #expect(!receipt.stdout.isEmpty)
    }
  ).open(sourceID: "gmail", ref: "gmail:record/example-1", anchorID: "match")
  #expect(response.requestedRef == "gmail:record/example-1")
  #expect(response.requestedAnchorID == "match")
  #expect(response.record?.openRef == "gmail:record/example-1")
  #expect(response.record?.presentation.primaryAnchorID == "match")

  await #expect(throws: TrawlClientError.invalidProtobuf) {
    _ = try await ProcessTrawlClient(binaryURL: helper.binary)
      .open(sourceID: "gmail", ref: "short-1", anchorID: "match")
  }
}

@Test func processClientCarriesMatchedReferencesAcrossSearchAndOpen() async throws {
  let helper = try framedHelper(
    commands: [
      "search": try searchFrame(outcome: .partial),
      "open": try openFrame(ref: "gmail:message/example-1"),
    ])
  defer { helper.remove() }
  let client = ProcessTrawlClient(binaryURL: helper.binary, receiveReceipt: { _ in })
  let search = try await client.search("partial", source: nil)
  let selected = try #require(search.hits.first)
  let opened = try await client.open(
    sourceID: selected.sourceID, ref: selected.openRef, anchorID: selected.anchorID)

  #expect(search.outcome == .partial)
  #expect(opened.requestedRef == selected.openRef)
  #expect(opened.requestedAnchorID == selected.anchorID)
  #expect(opened.record?.openRef == selected.openRef)
  #expect(opened.record?.presentation.primaryAnchorID == selected.anchorID)
}

@Test func processClientCarriesOneBoundedOpaqueResourceFrame() async throws {
  let helper = try framedHelper(commands: ["resource": try resourceFrame()])
  defer { helper.remove() }
  let resource = try await ProcessTrawlClient(
    binaryURL: helper.binary,
    receiveReceipt: { receipt in
      #expect(
        receipt.arguments
          == ["__app", "resource", "photos", "photos:resource/example-1", "32"])
      #expect(!receipt.stdout.isEmpty)
    }
  ).resource(sourceID: "photos", ref: "photos:resource/example-1", maxBytes: 32)
  #expect(resource.ref == "photos:resource/example-1")
  #expect(resource.contentType == "image/jpeg")
  #expect(resource.data == Data("fixture bytes".utf8))
}

private struct TemporaryHelper {
  let directory: URL
  let binary: URL

  func remove() {
    try? FileManager.default.removeItem(at: directory)
  }
}

private final class SyncProgressRecorder: @unchecked Sendable {
  private let lock = NSLock()
  private var recorded: [SyncProgress] = []

  var values: [SyncProgress] {
    lock.withLock { recorded }
  }

  func append(_ progress: SyncProgress) {
    lock.withLock { recorded.append(progress) }
  }
}

private func temporaryHelper(script: String) throws -> TemporaryHelper {
  let directory = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
  let binary = directory.appending(path: "helper")
  try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
  try Data("#!/bin/sh\nset -eu\n\(script)\n".utf8).write(to: binary)
  try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: binary.path)
  return TemporaryHelper(directory: directory, binary: binary)
}

private func framedHelper(commands: [String: Data], trailing: String = "") throws -> TemporaryHelper
{
  let cases = commands.map { command, frame in
    "\(command)) printf '\(shellBytes(frame))' ;;"
  }
  .sorted()
  .joined(separator: "\n")
  return try temporaryHelper(
    script: """
      case "$2" in
      \(cases)
      *) exit 2 ;;
      esac
      \(trailing)
      """
  )
}

private struct UnscopedTestClient: TrawlClient {
  func status() async throws -> StatusResponse { fatalError("not used") }
  func requestPhotos() async throws -> StatusResponse { fatalError("not used") }
  func sync() async throws -> SyncResponse { fatalError("scoped sync must not call this") }
  func search(_: String, source _: String?) async throws -> SearchResponse {
    fatalError("not used")
  }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError("not used")
  }
}

private func shellBytes(_ data: Data) -> String {
  data.map { String(format: "\\%03o", $0) }.joined()
}

private func completeSearchFrame() throws -> Data {
  var response = Trawl_Federation_V1_SearchResponse()
  response.outcome = .complete
  response.order = .recency
  response.resultLimit = 20
  return try DelimitedFrames.encode(response)
}

private func statusFrame() throws -> Data {
  var source = Trawl_Federation_V1_SourceStatus()
  source.manifest = .with {
    $0.sourceID = "gmail"
    $0.displayName = "Gmail"
  }
  source.appID = "example.gmail"
  source.schemaVersion = "1"
  source.state = "ok"
  source.summary = "Ready"
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .complete
  response.sources = [source]
  return try DelimitedFrames.encode(response)
}

private func syncFrame() throws -> Data {
  var response = Trawl_App_V1_SyncResponse()
  response.outcome = .complete
  response.sources = [
    .with {
      $0.appID = "gmail"
      $0.surface = "Gmail"
      $0.outcome = .complete
    }
  ]
  return try DelimitedFrames.encode(response)
}

private func searchFrame(outcome: Trawl_Federation_V1_OperationOutcome) throws -> Data {
  var hit = Trawl_Federation_V1_SearchHit()
  hit.sourceID = "gmail"
  hit.openRef = "gmail:message/example-1"
  hit.shortRef = "example-1"
  hit.anchorID = "match"
  hit.summary = .with {
    $0.title = "Example"
    $0.subtitle = "Example sender"
  }
  hit.archiveContext = [
    .with {
      $0.kind = "direction"
      $0.label = "Received"
    }
  ]
  hit.evidence = [
    .with {
      $0.label = "Message"
      $0.text = .with {
        $0.runs = [
          .with {
            $0.text = "Matched text"
            $0.matched = true
          }
        ]
      }
    }
  ]
  var source = Trawl_Federation_V1_SearchSourceResult()
  source.sourceID = "gmail"
  source.displayName = "Gmail"
  source.hits = [hit]
  source.totalMatches = 1
  source.totalIsExact = true
  var response = Trawl_Federation_V1_SearchResponse()
  response.outcome = outcome
  response.order = .recency
  response.resultLimit = 20
  response.hits = [hit]
  response.sources = [source]
  return try DelimitedFrames.encode(response)
}

private func openFrame(ref: String = "gmail:record/example-1") throws -> Data {
  var response = Trawl_Open_V1_OpenResponse()
  response.outcome = .complete
  response.requestedRef = ref
  response.requestedAnchorID = "match"
  response.record = .with {
    $0.sourceID = "gmail"
    $0.openRef = ref
    $0.data = .with { $0.typeURL = "type.example/Message" }
    $0.presentation = .with {
      $0.title = "Example"
      $0.primaryAnchorID = "match"
      $0.blocks = [
        .with {
          $0.anchorID = "match"
          $0.prose = .with { $0.text = "Matched text" }
        }
      ]
    }
  }
  return try DelimitedFrames.encode(response)
}

private func resourceFrame() throws -> Data {
  var response = Trawl_Presentation_V1_ResourceResponse()
  response.resourceRef = "photos:resource/example-1"
  response.contentType = "image/jpeg"
  response.data = Data("fixture bytes".utf8)
  return try DelimitedFrames.encode(response)
}
