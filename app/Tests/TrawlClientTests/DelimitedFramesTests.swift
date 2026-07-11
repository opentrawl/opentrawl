import Foundation
import SwiftProtobuf
import Testing

@testable import TrawlClient

@Suite(.serialized)
struct TrawlClientProcessTests {
@Test func appFramesUseOneLittleEndianResponseAndThenEOF() throws {
  var response = Trawl_App_V1_SearchResponse()
  response.outcome = .complete
  response.resultLimit = 20
  let frame = try DelimitedFrames.encode(response)

  #expect(Array(frame.prefix(4)) == [4, 0, 0, 0])
  #expect(try DelimitedFrames.decodeExactlyOne(frame) == response.serializedData())
  #expect(throws: TrawlClientError.missingFrame) {
    try DelimitedFrames.decodeExactlyOne(Data())
  }
  #expect(throws: TrawlClientError.extraFrame) {
    try DelimitedFrames.decodeExactlyOne(frame + Data([0]))
  }
}

@Test func processClientPreservesCompleteEmptyPartialAndFailedSearches() async throws {
  let complete = searchResponse(outcome: .complete, includeHit: true, includeFailure: false)
  let empty = searchResponse(outcome: .complete, includeHit: false, includeFailure: false)
  let partial = searchResponse(outcome: .partial, includeHit: true, includeFailure: true)
  let failed = searchResponse(outcome: .failed, includeHit: false, includeFailure: true)

  let completeResult = try await searchResult(complete)
  #expect(completeResult.response.outcome == .complete)
  #expect(completeResult.response.hits.map(\.id) == ["gmail:message:example-1"])
  #expect(completeResult.response.failures.isEmpty)
  try assertSearchReceipt(completeResult.receipt, expectedStdout: try DelimitedFrames.encode(complete))

  let emptyResult = try await searchResult(empty)
  #expect(emptyResult.response.outcome == .complete)
  #expect(emptyResult.response.hits.isEmpty)

  let partialResult = try await searchResult(partial)
  #expect(partialResult.response.outcome == .partial)
  #expect(partialResult.response.hits.map(\.id) == ["gmail:message:example-1"])
  #expect(partialResult.response.failures.map(\.sourceID) == ["calendar"])
  #expect(partialResult.response.failures.first?.code == .permission)

  let failedResult = try await searchResult(failed)
  #expect(failedResult.response.outcome == .failed)
  #expect(failedResult.response.hits.isEmpty)
  #expect(failedResult.response.failures.map(\.sourceID) == ["calendar"])
}

@Test func processClientKeepsTypedStatusSetupAndSyncResults() async throws {
  let setup = [
    setup("full-disk", .fullDiskAccess, .ready, .none, []),
    setup("photos", .photosPermission, .needsAction, .requestPhotos, []),
    setup("account", .account, .unavailable, .runCommand, ["gog", "auth", "add"]),
    setup("pairing", .pairing, .needsAction, .openFullDiskAccess, []),
    setup("archive", .archiveImport, .needsAction, .chooseArchive, ["archive.zip"]),
  ]

  var source = Trawl_App_V1_SourceStatus()
  source.appID = "imessage"
  source.surface = "Messages"
  source.state = "error"
  source.setupRequirements = setup

  var status = Trawl_App_V1_StatusResponse()
  status.outcome = .partial
  status.sources = [source]
  status.failures = [failure()]
  let statusFixture = try fixtureBinary(stdout: DelimitedFrames.encode(status))
  defer { try? FileManager.default.removeItem(at: statusFixture.deletingLastPathComponent()) }
  let statusRecorder = ReceiptRecorder()
  let statusResponse = try await ProcessTrawlClient(
    binaryURL: statusFixture,
    receiveReceipt: statusRecorder.record
  ).status()

  #expect(statusResponse.outcome == .partial)
  let requirements = try #require(statusResponse.sources.first?.setupRequirements)
  #expect(requirements.map(\.kind) == [.fullDiskAccess, .photosPermission, .account, .pairing, .archiveImport])
  #expect(requirements.map(\.state) == [.ready, .needsAction, .unavailable, .needsAction, .needsAction])
  #expect(requirements.map(\.action) == [.none, .requestPhotos, .runCommand, .openFullDiskAccess, .chooseArchive])
  #expect(requirements.map(\.command) == [[], [], ["gog", "auth", "add"], [], ["archive.zip"]])
  #expect(statusResponse.failures.map(\.sourceID) == ["calendar"])
  let statusReceipt = try #require(statusRecorder.value)
  let statusFrame = try DelimitedFrames.encode(status)
  #expect(statusReceipt.arguments == ["__app", "status"])
  #expect(statusReceipt.stdin.isEmpty)
  #expect(statusReceipt.stdout == statusFrame)
  #expect(statusReceipt.stderr.isEmpty)
  #expect(!statusReceipt.terminatedBySignal && statusReceipt.exitCode == 0)

  var perSource = Trawl_App_V1_SyncSourceResult()
  perSource.appID = "calendar"
  perSource.surface = "Calendar"
  perSource.outcome = .failed
  perSource.failure = failure()
  var sync = Trawl_App_V1_SyncResponse()
  sync.outcome = .partial
  sync.sources = [perSource]
  sync.failures = [failure()]
  let syncFixture = try fixtureBinary(stdout: DelimitedFrames.encode(sync))
  defer { try? FileManager.default.removeItem(at: syncFixture.deletingLastPathComponent()) }
  let syncRecorder = ReceiptRecorder()
  let syncResponse = try await ProcessTrawlClient(
    binaryURL: syncFixture,
    receiveReceipt: syncRecorder.record
  ).sync()

  #expect(syncResponse.outcome == .partial)
  #expect(syncResponse.sources.map(\.sourceID) == ["calendar"])
  #expect(syncResponse.sources.first?.failure?.code == .permission)
  #expect(syncResponse.failures.map(\.sourceID) == ["calendar"])
  let syncReceipt = try #require(syncRecorder.value)
  let syncFrame = try DelimitedFrames.encode(sync)
  #expect(syncReceipt.arguments == ["__app", "sync"])
  #expect(syncReceipt.stdin.isEmpty)
  #expect(syncReceipt.stdout == syncFrame)
  #expect(syncReceipt.stderr.isEmpty)
  #expect(!syncReceipt.terminatedBySignal && syncReceipt.exitCode == 0)
}

@Test func processClientUsesTypedOpenAndRetainsExactOutputBytes() async throws {
  var response = Trawl_App_V1_OpenResponse()
  response.outcome = .complete
  response.appID = "notes"
  response.openRef = "notes:note:example-1"
  response.output = Data([0, 255, 10])
  let frame = try DelimitedFrames.encode(response)
  let fixture = try fixtureBinary(stdout: frame)
  defer { try? FileManager.default.removeItem(at: fixture.deletingLastPathComponent()) }
  let recorder = ReceiptRecorder()

  let decoded = try await ProcessTrawlClient(
    binaryURL: fixture,
    receiveReceipt: recorder.record
  ).open("notes:note:example-1")

  #expect(decoded.outcome == .complete)
  #expect(decoded.output == Data([0, 255, 10]))
  guard let receipt = recorder.value else { throw ReceiptError.missing }
  #expect(receipt.executableURL == fixture)
  #expect(receipt.arguments == ["__app", "open", "notes:note:example-1"])
  #expect(receipt.stdin.isEmpty)
  #expect(receipt.stdout == frame)
  #expect(receipt.stderr == Data())
  #expect(!receipt.terminatedBySignal)
  #expect(receipt.exitCode == 0)

  var invalid = Trawl_App_V1_OpenResponse()
  invalid.outcome = .failed
  invalid.openRef = "not-a-ref"
  invalid.failure = invalidOpenFailure()
  let invalidFrame = try DelimitedFrames.encode(invalid)
  let invalidFixture = try fixtureBinary(stdout: invalidFrame)
  defer { try? FileManager.default.removeItem(at: invalidFixture.deletingLastPathComponent()) }
  let invalidRecorder = ReceiptRecorder()
  let invalidResult = try await ProcessTrawlClient(
    binaryURL: invalidFixture,
    receiveReceipt: invalidRecorder.record
  ).open("not-a-ref")

  #expect(invalidResult.outcome == .failed)
  #expect(invalidResult.sourceID.isEmpty)
  #expect(invalidResult.failure?.sourceID.isEmpty == true)
  #expect(invalidResult.failure?.code == .invalidInput)
  let invalidReceipt = try #require(invalidRecorder.value)
  #expect(invalidReceipt.arguments == ["__app", "open", "not-a-ref"])
  #expect(invalidReceipt.stdin.isEmpty)
  #expect(invalidReceipt.stdout == invalidFrame)
  #expect(invalidReceipt.stderr.isEmpty)
  #expect(!invalidReceipt.terminatedBySignal && invalidReceipt.exitCode == 0)
}

@Test func developmentSyntheticHelperUsesTheSameTypedBoundary() async throws {
  let binary = try #require(developmentSyntheticBinary())
  let recorder = ReceiptRecorder()
  let client = ProcessTrawlClient(binaryURL: binary, receiveReceipt: recorder.record)

  let status = try await client.status()
  let search = try await client.search("partial", source: nil)
  let sync = try await client.sync()
  let open = try await client.open("notes:note:example-3")

  #expect(status.outcome == .complete)
  #expect(status.sources.count == 9)
  #expect(search.outcome == .partial)
  #expect(search.hits.count == 3)
  #expect(search.failures.map(\.sourceID) == ["calendar"])
  #expect(sync.outcome == .complete)
  #expect(sync.sources.count == 9)
  #expect(open.outcome == .complete)
  #expect(open.output == Data("Source: Notes\nTitle: Packing list\n\nPassport, charger and the example train ticket.\n".utf8))

  let receipts = recorder.values
  #expect(receipts.map(\.executableURL) == Array(repeating: binary, count: 4))
  #expect(receipts.map(\.arguments) == [
    ["__app", "status"],
    ["__app", "search", "partial"],
    ["__app", "sync"],
    ["__app", "open", "notes:note:example-3"],
  ])
  #expect(receipts.allSatisfy { $0.stdin.isEmpty })
  #expect(receipts.allSatisfy { !$0.stdout.isEmpty && $0.stderr.isEmpty && !$0.terminatedBySignal && $0.exitCode == 0 })
}

@Test func processClientReportsEveryFramingAndProcessFailure() async throws {
  #expect(ProcessTrawlClient.defaultSearchDeadline == .seconds(10))
  #expect(ProcessTrawlClient.defaultOperationDeadline == .seconds(30))

  let nonzero = try await expectSearchError(stdout: Data(), exit: 7, expected: .nonZeroExitBeforeFrame(7))
  let missing = try await expectSearchError(stdout: Data(), exit: 0, expected: .missingFrame)
  let truncated = try await expectSearchError(stdout: Data([1, 0, 0]), exit: 0, expected: .invalidFrame)
  let oversized = try await expectSearchError(
    stdout: Data([1, 0, 0, 1]),
    exit: 0,
    expected: .oversizedFrame
  )
  let invalid = try await expectSearchError(
    stdout: Data([1, 0, 0, 0, 255]),
    exit: 0,
    expected: .invalidProtobuf
  )
  let extra = try await expectSearchError(
    stdout: Data([2, 0, 0, 0, 8, 1, 0]),
    exit: 0,
    expected: .extraFrame
  )

  #expect(nonzero.stdout.isEmpty && nonzero.stderr.isEmpty && nonzero.exitCode == 7)
  #expect(missing.stdout.isEmpty && missing.stderr.isEmpty && missing.exitCode == 0)
  #expect(truncated.stdout == Data([1, 0, 0]) && truncated.stderr.isEmpty)
  #expect(oversized.stdout == Data([1, 0, 0, 1]) && oversized.stderr.isEmpty)
  #expect(invalid.stdout == Data([1, 0, 0, 0, 255]) && invalid.stderr.isEmpty)
  #expect(extra.stdout == Data([2, 0, 0, 0, 8, 1, 0]) && extra.stderr.isEmpty)

  let streamingFixture = try fixtureBinary(stdout: Data(), oversizedStreaming: true)
  defer { try? FileManager.default.removeItem(at: streamingFixture.deletingLastPathComponent()) }
  let streamingRecorder = ReceiptRecorder()
  await #expect(throws: TrawlClientError.oversizedFrame) {
    _ = try await ProcessTrawlClient(
      binaryURL: streamingFixture,
      receiveReceipt: streamingRecorder.record
    ).search("synthetic", source: nil)
  }
  let streamingReceipt = try #require(streamingRecorder.value)
  #expect(streamingReceipt.stdout == Data([1, 0, 0, 1]))
  #expect(streamingReceipt.stderr.isEmpty)
  #expect(streamingReceipt.terminatedBySignal)

  let signalPIDFile = FileManager.default.temporaryDirectory
    .appendingPathComponent(UUID().uuidString)
  let signalFixture = try fixtureBinary(stdout: Data(), writePIDTo: signalPIDFile)
  defer { try? FileManager.default.removeItem(at: signalFixture.deletingLastPathComponent()) }
  defer { try? FileManager.default.removeItem(at: signalPIDFile) }
  let signalRecorder = ReceiptRecorder()
  let signalTask = Task {
    try await ProcessTrawlClient(
      binaryURL: signalFixture,
      receiveReceipt: signalRecorder.record
    ).search("synthetic", source: nil)
  }
  let signalPID = try await waitForPID(at: signalPIDFile)
  #expect(Darwin.kill(signalPID, SIGTERM) == 0)
  await #expect(throws: TrawlClientError.terminatedBySignal(SIGTERM)) {
    _ = try await signalTask.value
  }
  let signalReceipt = try #require(signalRecorder.value)
  #expect(signalReceipt.stdout.isEmpty && signalReceipt.stderr.isEmpty && signalReceipt.terminatedBySignal)

  let timeoutFixture = try fixtureBinary(stdout: Data(), ignoreTermination: true)
  defer { try? FileManager.default.removeItem(at: timeoutFixture.deletingLastPathComponent()) }
  let timeoutRecorder = ReceiptRecorder()
  await #expect(throws: TrawlClientError.timedOut) {
    _ = try await ProcessTrawlClient(
      binaryURL: timeoutFixture,
      searchDeadline: .milliseconds(20),
      receiveReceipt: timeoutRecorder.record
    ).search("synthetic", source: nil)
  }
  let timeoutReceipt = try #require(timeoutRecorder.value)
  #expect(timeoutReceipt.stdout.isEmpty && timeoutReceipt.stderr.isEmpty && timeoutReceipt.terminatedBySignal)

  let completeFrame = try DelimitedFrames.encode(searchResponse(
    outcome: .complete,
    includeHit: true,
    includeFailure: false
  ))
  let completeThenHangFixture = try fixtureBinary(
    stdout: completeFrame,
    completeFrameThenIgnoreTermination: true
  )
  defer { try? FileManager.default.removeItem(at: completeThenHangFixture.deletingLastPathComponent()) }
  let completeThenHangRecorder = ReceiptRecorder()
  await #expect(throws: TrawlClientError.timedOut) {
    _ = try await ProcessTrawlClient(
      binaryURL: completeThenHangFixture,
      searchDeadline: .milliseconds(250),
      receiveReceipt: completeThenHangRecorder.record
    ).search("synthetic", source: nil)
  }
  let completeThenHangReceipt = try #require(completeThenHangRecorder.value)
  #expect(completeThenHangReceipt.stdout == completeFrame)
  #expect(completeThenHangReceipt.stderr.isEmpty)
  #expect(completeThenHangReceipt.terminatedBySignal)
  #expect(completeThenHangReceipt.exitCode == SIGKILL)

  let cancellationFixture = try fixtureBinary(stdout: Data(), sleep: true)
  defer { try? FileManager.default.removeItem(at: cancellationFixture.deletingLastPathComponent()) }
  let cancellationRecorder = ReceiptRecorder()
  let cancelled = Task {
    try await ProcessTrawlClient(
      binaryURL: cancellationFixture,
      receiveReceipt: cancellationRecorder.record
    ).search("synthetic", source: nil)
  }
  try await Task.sleep(for: .milliseconds(20))
  cancelled.cancel()
  await #expect(throws: TrawlClientError.cancelled) {
    _ = try await cancelled.value
  }
  let cancellationReceipt = try #require(cancellationRecorder.value)
  #expect(cancellationReceipt.stdout.isEmpty && cancellationReceipt.stderr.isEmpty && cancellationReceipt.terminatedBySignal)

  let missingBinary = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
  await #expect(throws: TrawlClientError.helperMissing) {
    _ = try await ProcessTrawlClient(binaryURL: missingBinary).search("synthetic", source: nil)
  }

  let directory = FileManager.default.temporaryDirectory
    .appendingPathComponent(UUID().uuidString, isDirectory: true)
  try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
  defer { try? FileManager.default.removeItem(at: directory) }
  await #expect(throws: TrawlClientError.launchFailed) {
    _ = try await ProcessTrawlClient(binaryURL: directory).search("synthetic", source: nil)
  }
}
}

private func searchResult(
  _ message: Trawl_App_V1_SearchResponse
) async throws -> (response: SearchResponse, receipt: ProcessBoundaryReceipt) {
  let bytes = try DelimitedFrames.encode(message)
  let binary = try fixtureBinary(stdout: bytes, stderr: Data("synthetic stderr\n".utf8))
  defer { try? FileManager.default.removeItem(at: binary.deletingLastPathComponent()) }
  let recorder = ReceiptRecorder()
  let response = try await ProcessTrawlClient(
    binaryURL: binary,
    receiveReceipt: recorder.record
  ).search("synthetic", source: "gmail")
  guard let receipt = recorder.value else { throw ReceiptError.missing }
  return (response, receipt)
}

private func assertSearchReceipt(_ receipt: ProcessBoundaryReceipt, expectedStdout: Data) throws {
  #expect(receipt.arguments == ["__app", "search", "--source", "gmail", "synthetic"])
  #expect(receipt.stdin.isEmpty)
  #expect(receipt.stdout == expectedStdout)
  #expect(receipt.stderr == Data("synthetic stderr\n".utf8))
  #expect(!receipt.terminatedBySignal)
  #expect(receipt.exitCode == 0)
}

private func expectSearchError(
  stdout: Data,
  exit: Int32,
  expected: TrawlClientError
) async throws -> ProcessBoundaryReceipt {
  let binary = try fixtureBinary(stdout: stdout, exit: exit)
  defer { try? FileManager.default.removeItem(at: binary.deletingLastPathComponent()) }
  let recorder = ReceiptRecorder()
  await #expect(throws: expected) {
    _ = try await ProcessTrawlClient(
      binaryURL: binary,
      receiveReceipt: recorder.record
    ).search("synthetic", source: nil)
  }
  return try #require(recorder.value)
}

private func searchResponse(
  outcome: Trawl_App_V1_OperationOutcome,
  includeHit: Bool,
  includeFailure: Bool
) -> Trawl_App_V1_SearchResponse {
  var response = Trawl_App_V1_SearchResponse()
  response.outcome = outcome
  response.resultLimit = 20
  if includeHit {
    var hit = Trawl_App_V1_SearchHit()
    hit.openRef = "gmail:message:example-1"
    hit.appID = "gmail"
    hit.title = "Avery Example"
    hit.snippet = "Synthetic result"
    hit.whenDisplay = "10 Jul"
    response.hits = [hit]
  }
  if includeFailure {
    response.failures = [failure()]
  }
  return response
}

private func failure() -> Trawl_App_V1_SourceFailure {
  var value = Trawl_App_V1_SourceFailure()
  value.appID = "calendar"
  value.surface = "Calendar"
  value.code = .permission
  value.message = "Allow calendar access."
  value.remedy = "Open System Settings."
  return value
}

private func invalidOpenFailure() -> Trawl_App_V1_SourceFailure {
  var value = Trawl_App_V1_SourceFailure()
  value.code = .invalidInput
  value.message = "Ref is missing a source or path."
  value.remedy = "Search again and select a result."
  return value
}

private func setup(
  _ id: String,
  _ kind: Trawl_App_V1_SetupKind,
  _ state: Trawl_App_V1_SetupState,
  _ action: Trawl_App_V1_SetupActionKind,
  _ command: [String]
) -> Trawl_App_V1_SetupRequirement {
  var value = Trawl_App_V1_SetupRequirement()
  value.id = id
  value.kind = kind
  value.state = state
  value.explanation = "Synthetic setup requirement."
  value.action = action
  value.command = command
  return value
}

private func fixtureBinary(
  stdout: Data,
  stderr: Data = Data(),
  exit: Int32 = 0,
  ignoreTermination: Bool = false,
  sleep: Bool = false,
  oversizedStreaming: Bool = false,
  completeFrameThenIgnoreTermination: Bool = false,
  writePIDTo: URL? = nil
) throws -> URL {
  let directory = FileManager.default.temporaryDirectory
    .appendingPathComponent(UUID().uuidString, isDirectory: true)
  try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
  let binary = directory.appendingPathComponent("trawl")
  let body: String
  if let writePIDTo {
    body = """
      /usr/bin/printf '%s\\n' $$ > '\(writePIDTo.path)'
      while :; do /bin/sleep 1; done
      """
  } else if completeFrameThenIgnoreTermination {
    body = """
      /usr/bin/printf '\(octal(stdout))'
      trap '' TERM
      while :; do /bin/sleep 1; done
      """
  } else if ignoreTermination {
    body = "trap '' TERM\nwhile :; do /bin/sleep 1; done"
  } else if sleep {
    body = "exec /bin/sleep 60"
  } else if oversizedStreaming {
    body = """
      /usr/bin/printf '\\001\\000\\000\\001'
      while :; do /usr/bin/printf x; done
      """
  } else {
    body = """
      /usr/bin/printf '\(octal(stdout))'
      /usr/bin/printf '\(octal(stderr))' >&2
      exit \(exit)
      """
  }
  let script = "#!/bin/sh\n\(body)\n"
  try Data(script.utf8).write(to: binary)
  try FileManager.default.setAttributes(
    [.posixPermissions: 0o755],
    ofItemAtPath: binary.path
  )
  return binary
}

private func waitForPID(at path: URL) async throws -> Int32 {
  for _ in 0 ..< 100 {
    if let value = try? String(contentsOf: path, encoding: .utf8), let pid = Int32(value.trimmingCharacters(in: .whitespacesAndNewlines)) {
      return pid
    }
    try await Task.sleep(for: .milliseconds(10))
  }
  throw ReceiptError.missing
}

private func octal(_ data: Data) -> String {
  data.map { String(format: "\\%03o", $0) }.joined()
}

private func developmentSyntheticBinary() -> URL? {
  let workingDirectory = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
  let candidates = [
    workingDirectory.appendingPathComponent("app/.build/out/Products/Debug/TrawlSynthetic"),
    workingDirectory.appendingPathComponent(".build/out/Products/Debug/TrawlSynthetic"),
  ]
  return candidates.first { FileManager.default.isExecutableFile(atPath: $0.path) }
}

private final class ReceiptRecorder: @unchecked Sendable {
  private let lock = NSLock()
  private var receipts: [ProcessBoundaryReceipt] = []

  var value: ProcessBoundaryReceipt? {
    lock.withLock { receipts.last }
  }

  var values: [ProcessBoundaryReceipt] {
    lock.withLock { receipts }
  }

  func record(_ value: ProcessBoundaryReceipt) {
    lock.withLock { receipts.append(value) }
  }
}

private enum ReceiptError: Error {
  case missing
}
