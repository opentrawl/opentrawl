import Darwin
@preconcurrency import Foundation
import OSLog
import SwiftProtobuf

public struct ProcessTrawlClient: TrawlClient {
  private static let logger = Logger(subsystem: "app.opentrawl.trawl", category: "helper")
  static let defaultSearchDeadline: Duration = .seconds(10)
  static let defaultOperationDeadline: Duration = .seconds(30)
  static let defaultSyncSourceDeadline: Duration = .seconds(31 * 60)
  static let defaultPhotosPermissionDeadline: Duration = .seconds(310)
  public static let maximumResourceBytes: UInt32 = 4 << 20

  private let binaryURL: URL
  private let searchDeadline: Duration
  private let operationDeadline: Duration
  private let receiveReceipt: (@Sendable (ProcessBoundaryReceipt) -> Void)?

  public init(binaryURL: URL = ProcessTrawlClient.embeddedBinary) {
    self.binaryURL = binaryURL
    searchDeadline = Self.defaultSearchDeadline
    operationDeadline = Self.defaultOperationDeadline
    receiveReceipt = nil
  }

  init(
    binaryURL: URL,
    searchDeadline: Duration = ProcessTrawlClient.defaultSearchDeadline,
    operationDeadline: Duration = ProcessTrawlClient.defaultOperationDeadline,
    receiveReceipt: @escaping @Sendable (ProcessBoundaryReceipt) -> Void
  ) {
    self.binaryURL = binaryURL
    self.searchDeadline = searchDeadline
    self.operationDeadline = operationDeadline
    self.receiveReceipt = receiveReceipt
  }

  public static var embeddedBinary: URL {
    Bundle.main.bundleURL
      .appendingPathComponent("Contents/Helpers/trawl", isDirectory: false)
  }

  public func status() async throws -> StatusResponse {
    try await response(
      arguments: ["__app", "status"],
      deadline: operationDeadline,
      as: Trawl_Federation_V1_StatusResponse.self
    ).model()
  }

  public func requestPhotos() async throws -> StatusResponse {
    try await response(
      arguments: ["__app", "request-photos"],
      deadline: Self.defaultPhotosPermissionDeadline,
      as: Trawl_Federation_V1_StatusResponse.self
    ).model()
  }

  public func sync() async throws -> SyncResponse {
    try await sync { _ in }
  }

  public func sync(progress: @escaping @Sendable (SyncProgress) -> Void) async throws
    -> SyncResponse
  {
    try await sync(sourceIDs: [], progress: progress)
  }

  public func sync(
    sourceIDs requestedSourceIDs: [String],
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse {
    var seen = Set<String>()
    let sourceIDs = requestedSourceIDs.filter {
      !$0.isEmpty && seen.insert($0).inserted
    }
    for sourceID in sourceIDs {
      progress(.started(sourceID: sourceID, sourceName: sourceID))
    }
    let arguments = ["__app", "sync"] + sourceIDs.flatMap { ["--source", $0] }
    let result = try await response(
      arguments: arguments,
      deadline: Self.defaultSyncSourceDeadline,
      as: Trawl_App_V1_SyncResponse.self
    ).model()
    for source in result.sources {
      progress(.finished(source))
    }
    return result
  }

  public func downloadTelegramMessageHistory(
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse {
    progress(.started(sourceID: "telegram", sourceName: "Telegram"))
    let response = try await response(
      arguments: ["__app", "sync", "--source", "telegram", "--full-history"],
      deadline: nil,
      as: Trawl_App_V1_SyncResponse.self
    ).model()
    for result in response.sources {
      progress(.finished(result))
    }
    return response
  }

  public func search(_ query: String, source: String?) async throws -> SearchResponse {
    var arguments = ["__app", "search"]
    if let source, !source.isEmpty {
      arguments += ["--source", source]
    }
    arguments.append(query)
    return try await response(
      arguments: arguments,
      deadline: searchDeadline,
      as: Trawl_Federation_V1_SearchResponse.self
    ).model()
  }

  public func open(sourceID: String, ref: String, anchorID: String) async throws -> OpenResponse {
    guard isCanonicalSourceRef(ref, sourceID: sourceID), isValidAnchorID(anchorID) else {
      throw TrawlClientError.invalidProtobuf
    }
    let result = try await response(
      arguments: ["__app", "open", sourceID, ref, anchorID],
      deadline: operationDeadline,
      as: Trawl_Open_V1_OpenResponse.self
    ).model()
    guard result.requestedRef == ref, result.requestedAnchorID == anchorID,
      result.record == nil
        || (result.record?.sourceID == sourceID && result.record?.openRef == ref)
    else { throw TrawlClientError.invalidProtobuf }
    return result
  }

  public func resource(sourceID: String, ref: String, maxBytes: UInt32) async throws
    -> PresentationResourceData
  {
    guard maxBytes > 0, maxBytes <= Self.maximumResourceBytes,
      isCanonicalSourceRef(ref, sourceID: sourceID)
    else { throw TrawlClientError.invalidProtobuf }
    let response = try await response(
      arguments: ["__app", "resource", sourceID, ref, String(maxBytes)],
      deadline: operationDeadline,
      as: Trawl_Presentation_V1_ResourceResponse.self
    )
    guard response.resourceRef == ref, response.data.count <= Int(maxBytes) else {
      throw TrawlClientError.invalidProtobuf
    }
    return try response.model()
  }

  private func response<Message>(
    arguments: [String],
    deadline: Duration?,
    as messageType: Message.Type
  ) async throws -> Message where Message: SwiftProtobuf.Message {
    let result = try await run(arguments: arguments, deadline: deadline)
    if !result.stderr.isEmpty {
      Self.logger.error("Helper diagnostic: \(result.stderr, privacy: .private)")
    }
    if let framingError = result.framingError {
      if result.exitCode != 0, result.stdout.isEmpty {
        throw TrawlClientError.nonZeroExitBeforeFrame(result.exitCode)
      }
      throw framingError
    }
    if result.exitCode != 0, result.stdout.isEmpty {
      throw TrawlClientError.nonZeroExitBeforeFrame(result.exitCode)
    }
    guard let payload = result.payload else {
      throw TrawlClientError.invalidFrame
    }
    do {
      return try Message(serializedBytes: payload)
    } catch {
      throw TrawlClientError.invalidProtobuf
    }
  }

  private func run(arguments: [String], deadline: Duration?) async throws -> ProcessResult {
    guard FileManager.default.isExecutableFile(atPath: binaryURL.path) else {
      throw TrawlClientError.helperMissing
    }
    do {
      try Task.checkCancellation()
    } catch {
      throw TrawlClientError.cancelled
    }

    let invocation = ProcessInvocation(
      binaryURL: binaryURL,
      arguments: arguments,
      receiveReceipt: receiveReceipt
    )
    do {
      try invocation.process.run()
    } catch {
      throw TrawlClientError.launchFailed
    }

    do {
      let result = try await withTaskCancellationHandler {
        try await waitForResult(invocation, deadline: deadline)
      } onCancel: {
        invocation.terminateAfterGrace()
      }
      try Task.checkCancellation()
      return result
    } catch is CancellationError {
      invocation.terminateAfterGrace()
      throw TrawlClientError.cancelled
    }
  }

  private func waitForResult(
    _ invocation: ProcessInvocation,
    deadline: Duration?
  ) async throws -> ProcessResult {
    guard let deadline else {
      let result = await invocation.waitForResult()
      if let error = Self.unexpectedTerminationError(
        terminatedBySignal: result.terminatedBySignal,
        exitCode: result.exitCode,
        terminationWasRequested: invocation.terminationWasRequested
      ) {
        throw error
      }
      return result
    }
    return try await withThrowingTaskGroup(of: ProcessWaitOutcome.self) { group in
      group.addTask {
        .processResult(await invocation.waitForResult())
      }
      group.addTask {
        try await Task.sleep(for: deadline)
        return .deadlineReached
      }
      defer { group.cancelAll() }

      guard let first = try await group.next() else {
        throw TrawlClientError.timedOut
      }
      switch first {
      case .processResult(let result):
        if let error = Self.unexpectedTerminationError(
          terminatedBySignal: result.terminatedBySignal,
          exitCode: result.exitCode,
          terminationWasRequested: invocation.terminationWasRequested
        ) {
          throw error
        }
        return result
      case .deadlineReached:
        invocation.terminateAfterGrace()
        while let next = try await group.next() {
          if case .processResult = next {
            throw TrawlClientError.timedOut
          }
        }
        throw TrawlClientError.timedOut
      }
    }
  }

  static func unexpectedTerminationError(
    terminatedBySignal: Bool,
    exitCode: Int32,
    terminationWasRequested: Bool
  ) -> TrawlClientError? {
    guard terminatedBySignal, !terminationWasRequested else { return nil }
    return .terminatedBySignal(exitCode)
  }
}

private enum ProcessWaitOutcome: Sendable {
  case processResult(ProcessResult)
  case deadlineReached
}

private struct ProcessResult: Sendable {
  let stdout: Data
  let payload: Data?
  let framingError: TrawlClientError?
  let stderr: Data
  let terminatedBySignal: Bool
  let exitCode: Int32
}

struct ProcessBoundaryReceipt: Sendable, Equatable {
  let executableURL: URL
  let arguments: [String]
  let stdin: Data
  let stdout: Data
  let stderr: Data
  let terminatedBySignal: Bool
  let exitCode: Int32
}

private final class ProcessInvocation: @unchecked Sendable {
  private let binaryURL: URL
  private let arguments: [String]
  private let receiveReceipt: (@Sendable (ProcessBoundaryReceipt) -> Void)?
  private let terminationLock = NSLock()
  private let exitLock = NSLock()
  private let exitSemaphore = DispatchSemaphore(value: 0)
  private var requestedTermination = false
  private var exitResult: ProcessExitResult?

  let process = Process()
  let stdout = Pipe()
  let stderr = Pipe()

  init(
    binaryURL: URL,
    arguments: [String],
    receiveReceipt: (@Sendable (ProcessBoundaryReceipt) -> Void)?
  ) {
    self.binaryURL = binaryURL
    self.arguments = arguments
    self.receiveReceipt = receiveReceipt
    process.executableURL = binaryURL
    process.arguments = arguments
    process.standardInput = FileHandle.nullDevice
    process.standardOutput = stdout
    process.standardError = stderr
    process.terminationHandler = { [weak self] process in
      self?.recordExit(
        ProcessExitResult(
          terminatedBySignal: process.terminationReason == .uncaughtSignal,
          exitCode: process.terminationStatus
        )
      )
    }
  }

  var terminationWasRequested: Bool {
    terminationLock.withLock { requestedTermination }
  }

  func waitForResult() async -> ProcessResult {
    let stdoutTask = Task.detached {
      self.readOneFrame()
    }
    let stderrTask = Task.detached {
      self.stderr.fileHandleForReading.readDataToEndOfFile()
    }
    let exitTask = Task.detached {
      await self.waitForExit()
    }

    let frame = await stdoutTask.value
    if frame.error != nil, process.isRunning {
      terminateAfterGrace()
    }
    let exit = await exitTask.value
    let result = ProcessResult(
      stdout: frame.bytes,
      payload: frame.payload,
      framingError: frame.error,
      stderr: await stderrTask.value,
      terminatedBySignal: exit.terminatedBySignal,
      exitCode: exit.exitCode
    )
    receiveReceipt?(
      ProcessBoundaryReceipt(
        executableURL: binaryURL,
        arguments: arguments,
        stdin: Data(),
        stdout: result.stdout,
        stderr: result.stderr,
        terminatedBySignal: result.terminatedBySignal,
        exitCode: result.exitCode
      )
    )
    return result
  }

  func terminateAfterGrace() {
    terminationLock.withLock { requestedTermination = true }
    guard process.isRunning else { return }
    process.terminate()
    Task.detached {
      try? await Task.sleep(for: .seconds(2))
      guard self.process.isRunning else { return }
      _ = Darwin.kill(self.process.processIdentifier, SIGKILL)
    }
  }

  private func recordExit(_ result: ProcessExitResult) {
    exitLock.withLock { exitResult = result }
    exitSemaphore.signal()
  }

  private func waitForExit() async -> ProcessExitResult {
    await withCheckedContinuation { continuation in
      DispatchQueue.global().async { [self] in
        exitSemaphore.wait()
        continuation.resume(returning: exitLock.withLock { exitResult! })
      }
    }
  }

  private func readOneFrame() -> FrameRead {
    var bytes = Data()
    guard let header = readExactly(MemoryLayout<UInt32>.size, into: &bytes) else {
      return FrameRead(
        bytes: bytes,
        payload: nil,
        error: bytes.isEmpty ? .missingFrame : .invalidFrame
      )
    }
    let payloadLength = header.withUnsafeBytes { raw in
      Int(UInt32(littleEndian: raw.loadUnaligned(as: UInt32.self)))
    }
    guard payloadLength <= DelimitedFrames.maximumFrameBytes else {
      return FrameRead(bytes: bytes, payload: nil, error: .oversizedFrame)
    }
    guard let payload = readExactly(payloadLength, into: &bytes) else {
      return FrameRead(bytes: bytes, payload: nil, error: .invalidFrame)
    }
    do {
      let extra = try stdout.fileHandleForReading.read(upToCount: 1) ?? Data()
      guard extra.isEmpty else {
        bytes.append(extra)
        return FrameRead(bytes: bytes, payload: nil, error: .extraFrame)
      }
      return FrameRead(bytes: bytes, payload: payload, error: nil)
    } catch {
      return FrameRead(bytes: bytes, payload: nil, error: .invalidFrame)
    }
  }

  private func readExactly(_ count: Int, into bytes: inout Data) -> Data? {
    let start = bytes.count
    while bytes.count - start < count {
      do {
        let chunk =
          try stdout.fileHandleForReading.read(upToCount: count - (bytes.count - start)) ?? Data()
        guard !chunk.isEmpty else { return nil }
        bytes.append(chunk)
      } catch {
        return nil
      }
    }
    return bytes.suffix(count)
  }
}

private struct ProcessExitResult: Sendable {
  let terminatedBySignal: Bool
  let exitCode: Int32
}

private struct FrameRead: Sendable {
  let bytes: Data
  let payload: Data?
  let error: TrawlClientError?
}
