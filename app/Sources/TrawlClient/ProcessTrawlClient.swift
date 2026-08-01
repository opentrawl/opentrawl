import Darwin
@preconcurrency import Foundation
import OSLog
import SwiftProtobuf

public struct TrawlRuntimeConfiguration: Sendable, Equatable {
  public static let stateRootEnvironmentKey = "OPENTRAWL_STATE_ROOT"

  public let helperURL: URL
  public let stateRoot: String?

  public init(
    bundleURL: URL = Bundle.main.bundleURL,
    environment: [String: String] = ProcessInfo.processInfo.environment
  ) {
    helperURL = bundleURL.appendingPathComponent("Contents/Helpers/trawl", isDirectory: false)
    stateRoot = environment[Self.stateRootEnvironmentKey]
  }

  public init(helperURL: URL, stateRoot: String?) {
    self.helperURL = helperURL
    self.stateRoot = stateRoot
  }

  public var agentCommand: String {
    let helper = Self.shellArgument(helperURL.path)
    guard let stateRoot else { return helper }
    return "env \(Self.stateRootEnvironmentKey)=\(Self.shellArgument(stateRoot)) \(helper)"
  }

  private static func shellArgument(_ value: String) -> String {
    let safe = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "/._-"))
    if !value.isEmpty, value.unicodeScalars.allSatisfy(safe.contains) { return value }
    return "'\(value.replacingOccurrences(of: "'", with: "'\\''"))'"
  }
}

public struct ProcessTrawlClient: TrawlClient {
  private static let logger = Logger(subsystem: "app.opentrawl.trawl", category: "helper")
  static let defaultSearchDeadline: Duration = .seconds(10)
  static let defaultOperationDeadline: Duration = .seconds(30)
  static let defaultUpdateTrawlerDeadline: Duration = .seconds(31 * 60)
  static let defaultPhotosPermissionDeadline: Duration = .seconds(310)

  private let binaryURL: URL
  private let stateRoot: String?
  private let searchDeadline: Duration
  private let operationDeadline: Duration
  private let receiveReceipt: (@Sendable (ProcessBoundaryReceipt) -> Void)?

  public init(binaryURL: URL = ProcessTrawlClient.embeddedBinary) {
    let configuration = TrawlRuntimeConfiguration(
      helperURL: binaryURL,
      stateRoot: ProcessInfo.processInfo.environment[
        TrawlRuntimeConfiguration.stateRootEnvironmentKey]
    )
    self.init(configuration: configuration)
  }

  public init(configuration: TrawlRuntimeConfiguration) {
    binaryURL = configuration.helperURL
    stateRoot = configuration.stateRoot
    searchDeadline = Self.defaultSearchDeadline
    operationDeadline = Self.defaultOperationDeadline
    receiveReceipt = nil
  }

  init(
    binaryURL: URL,
    stateRoot: String? = nil,
    searchDeadline: Duration = ProcessTrawlClient.defaultSearchDeadline,
    operationDeadline: Duration = ProcessTrawlClient.defaultOperationDeadline,
    receiveReceipt: @escaping @Sendable (ProcessBoundaryReceipt) -> Void
  ) {
    self.binaryURL = binaryURL
    self.stateRoot = stateRoot
    self.searchDeadline = searchDeadline
    self.operationDeadline = operationDeadline
    self.receiveReceipt = receiveReceipt
  }

  public static var embeddedBinary: URL {
    Bundle.main.bundleURL
      .appendingPathComponent("Contents/Helpers/trawl", isDirectory: false)
  }

  public func status() async throws -> FederatedTrawlerStatusOperation {
    try await response(
      arguments: ["__app", "status"],
      deadline: operationDeadline,
      as: Trawl_Federation_FederatedTrawlerStatusOperation.self
    ).decodedFederatedTrawlerStatusOperation()
  }

  public func update(
    registeredTrawlers requestedRegisteredTrawlers: [RegisteredTrawlerIdentity],
    progress: @escaping @Sendable (TrawlerArchiveUpdateProgress) -> Void
  ) async throws -> FederatedTrawlerArchiveUpdateOperation {
    var seen = Set<RegisteredTrawlerIdentity>()
    let registeredTrawlers = requestedRegisteredTrawlers.filter {
      !$0.registeredTrawlerIdentity.isEmpty && seen.insert($0).inserted
    }
    let arguments =
      ["__app", "update"]
      + registeredTrawlers.flatMap { ["--trawler", $0.registeredTrawlerIdentity] }
    return try await federatedTrawlerArchiveUpdateOperation(
      arguments: arguments,
      deadline: Self.defaultUpdateTrawlerDeadline,
      progress: progress
    )
  }

  public func search(_ request: TrawlArchiveSearchRequest) async throws
    -> FederatedTrawlerSearchOperation
  {
    var arguments = ["__app", "search"]
    if let registeredTrawler = request.onlySearchThisRegisteredTrawler,
      !registeredTrawler.registeredTrawlerIdentity.isEmpty
    {
      arguments += ["--trawler", registeredTrawler.registeredTrawlerIdentity]
    }
    if let earliestMatchingArchiveRecordTime = request.earliestMatchingArchiveRecordTimeInclusive {
      arguments += ["--after", earliestMatchingArchiveRecordTime.ISO8601Format()]
    }
    if let latestMatchingArchiveRecordTime = request.latestMatchingArchiveRecordTimeInclusive {
      arguments += ["--before", latestMatchingArchiveRecordTime.ISO8601Format()]
    }
    arguments += ["--limit", String(request.maximumReturnedSearchMatchCount)]
    if !request.searchQueryText.isEmpty {
      arguments.append(request.searchQueryText)
    }
    return try await response(
      arguments: arguments,
      deadline: searchDeadline,
      as: Trawl_Federation_FederatedTrawlerSearchOperation.self
    ).decodedFederatedTrawlerSearchOperation()
  }

  public func open(
    link: GloballyRoutableTrawlLink,
    anchor: RecordAnchorIdentifier
  ) async throws -> OpenResponse {
    guard parseGloballyRoutableTrawlLink(link) != nil,
      isValidAnchorIdentifier(anchor)
    else {
      throw TrawlClientError.invalidProtobuf
    }
    let result = try await response(
      arguments: [
        "__app",
        "open",
        link.globallyRoutableTrawlLink,
        anchor.recordAnchorIdentifier,
      ],
      deadline: operationDeadline,
      as: Trawl_Open_OpenResponse.self
    ).decodedOpenResponse()
    guard result.requestedTrawlLink == link,
      result.requestedRecordAnchor == anchor
    else { throw TrawlClientError.invalidProtobuf }
    return result
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

  private func federatedTrawlerArchiveUpdateOperation(
    arguments: [String],
    deadline: Duration?,
    progress: @escaping @Sendable (TrawlerArchiveUpdateProgress) -> Void
  ) async throws -> FederatedTrawlerArchiveUpdateOperation {
    let events = TrawlerArchiveUpdateEventRecorder(progress: progress)
    let result = try await run(
      arguments: arguments,
      deadline: deadline,
      receivePayload: events.receive
    )
    if !result.stderr.isEmpty {
      Self.logger.error("Helper diagnostic: \(result.stderr, privacy: .private)")
    }
    if let framingError = result.framingError {
      throw framingError
    }
    if result.exitCode != 0, result.stdout.isEmpty {
      throw TrawlClientError.nonZeroExitBeforeFrame(result.exitCode)
    }
    return try events.result()
  }

  private func run(
    arguments: [String],
    deadline: Duration?,
    receivePayload: (@Sendable (Data) -> Void)? = nil
  ) async throws -> ProcessResult {
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
      stateRoot: stateRoot,
      receiveReceipt: receiveReceipt
    )
    do {
      try invocation.process.run()
    } catch {
      throw TrawlClientError.launchFailed
    }

    do {
      let result = try await withTaskCancellationHandler {
        try await waitForResult(
          invocation,
          deadline: deadline,
          receivePayload: receivePayload
        )
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
    deadline: Duration?,
    receivePayload: (@Sendable (Data) -> Void)?
  ) async throws -> ProcessResult {
    guard let deadline else {
      let result = await invocation.waitForResult(receivePayload: receivePayload)
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
        .processResult(await invocation.waitForResult(receivePayload: receivePayload))
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
  let stateRoot: String?
  let stdin: Data
  let stdout: Data
  let stderr: Data
  let terminatedBySignal: Bool
  let exitCode: Int32
}

private final class TrawlerArchiveUpdateEventRecorder: @unchecked Sendable {
  private let lock = NSLock()
  private let progress: @Sendable (TrawlerArchiveUpdateProgress) -> Void
  private var terminal: FederatedTrawlerArchiveUpdateOperation?
  private var error: TrawlClientError?

  init(progress: @escaping @Sendable (TrawlerArchiveUpdateProgress) -> Void) {
    self.progress = progress
  }

  func receive(_ payload: Data) {
    do {
      let event = try Trawl_App_TrawlerArchiveUpdateEvent(serializedBytes: payload)
      let update: TrawlerArchiveUpdateProgress? = try lock.withLock {
        guard error == nil, terminal == nil else {
          error = .invalidProtobuf
          return nil
        }
        guard let kind = event.kind else {
          error = .invalidProtobuf
          return nil
        }
        switch kind {
        case .progress(let value):
          return try value.decodedTrawlerArchiveUpdateProgress()
        case .result(let value):
          let response = try value.decodedFederatedTrawlerArchiveUpdateOperation()
          terminal = response
          return nil
        }
      }
      if let update {
        progress(update)
      }
    } catch {
      lock.withLock { self.error = .invalidProtobuf }
    }
  }

  func result() throws -> FederatedTrawlerArchiveUpdateOperation {
    try lock.withLock {
      if let error { throw error }
      guard let terminal else { throw TrawlClientError.missingFrame }
      return terminal
    }
  }
}

private final class ProcessInvocation: @unchecked Sendable {
  private let binaryURL: URL
  private let arguments: [String]
  private let stateRoot: String?
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
    stateRoot: String?,
    receiveReceipt: (@Sendable (ProcessBoundaryReceipt) -> Void)?
  ) {
    self.binaryURL = binaryURL
    self.arguments = arguments
    self.stateRoot = stateRoot
    self.receiveReceipt = receiveReceipt
    process.executableURL = binaryURL
    process.arguments = arguments
    if let stateRoot {
      var environment = ProcessInfo.processInfo.environment
      environment[TrawlRuntimeConfiguration.stateRootEnvironmentKey] = stateRoot
      process.environment = environment
    }
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

  func waitForResult(
    receivePayload: (@Sendable (Data) -> Void)?
  ) async -> ProcessResult {
    let stdoutTask = Task.detached {
      if let receivePayload {
        self.readFrames(receivePayload: receivePayload)
      } else {
        self.readOneFrame()
      }
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
        stateRoot: stateRoot,
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

  private func readFrames(receivePayload: @Sendable (Data) -> Void) -> FrameRead {
    var bytes = Data()
    var frameCount = 0
    while true {
      let beforeHeader = bytes.count
      guard let header = readExactly(MemoryLayout<UInt32>.size, into: &bytes) else {
        if bytes.count == beforeHeader, frameCount > 0 {
          return FrameRead(bytes: bytes, payload: nil, error: nil)
        }
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
      frameCount += 1
      receivePayload(payload)
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
