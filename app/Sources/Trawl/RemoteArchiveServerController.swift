import Darwin
import Foundation
import Observation
import OSLog
import TrawlClient

enum RemoteArchiveServerState: Sendable, Equatable {
  case stopped
  case disabledInRemoteMode
  case starting
  case running(advertisedAddress: String)
  case portAlreadyInUse(listenAddress: String)
  case tailscaleUnavailable
  case failed(message: String)
}

enum RemoteArchiveServerExitClassification: Sendable, Equatable {
  case portAlreadyInUse(listenAddress: String)
  case tailscaleUnavailable
  case other(message: String)
}

struct RemoteArchiveLocalAccessTokenSummary: Sendable, Equatable {
  let activeLocalTokenCount: Int

  var hasActiveLocalToken: Bool { activeLocalTokenCount > 0 }

  private struct StoredTokenFile: Decodable {
    let agentAccessTokens: [StoredTokenRecord]?

    enum CodingKeys: String, CodingKey {
      case agentAccessTokens = "agent_access_tokens"
    }
  }

  private struct StoredTokenRecord: Decodable {
    let allowedSurface: String?
    let expiresTime: String?
    let revokedTime: String?

    enum CodingKeys: String, CodingKey {
      case allowedSurface = "allowed_surface"
      case expiresTime = "expires_time"
      case revokedTime = "revoked_time"
    }
  }

  static func read(
    fromStateRootURL stateRootURL: URL,
    now: Date = Date()
  ) -> RemoteArchiveLocalAccessTokenSummary {
    let fileURL = stateRootURL
      .appending(path: "agent-access", directoryHint: .isDirectory)
      .appending(path: "access-tokens.json", directoryHint: .notDirectory)
    guard let data = try? Data(contentsOf: fileURL),
      let decoded = try? JSONDecoder().decode(StoredTokenFile.self, from: data),
      let tokens = decoded.agentAccessTokens
    else {
      return RemoteArchiveLocalAccessTokenSummary(activeLocalTokenCount: 0)
    }
    let activeCount = tokens.reduce(into: 0) { count, token in
      guard token.allowedSurface == "local", token.revokedTime == nil else {
        return
      }
      if let rawExpiry = token.expiresTime?.trimmingCharacters(in: .whitespacesAndNewlines),
        !rawExpiry.isEmpty
      {
        guard let expiryDate = parseRFC3339Date(rawExpiry), now < expiryDate else {
          return
        }
      }
      count += 1
    }
    return RemoteArchiveLocalAccessTokenSummary(activeLocalTokenCount: activeCount)
  }

  private static func parseRFC3339Date(_ rawValue: String) -> Date? {
    let withFractionalSeconds = ISO8601DateFormatter()
    withFractionalSeconds.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    if let parsed = withFractionalSeconds.date(from: rawValue) {
      return parsed
    }
    let withoutFractionalSeconds = ISO8601DateFormatter()
    withoutFractionalSeconds.formatOptions = [.withInternetDateTime]
    return withoutFractionalSeconds.date(from: rawValue)
  }
}

/// Supervises the bundled `trawl mcp --http --tailscale --listen 127.0.0.1:7391`
/// helper as a child process of `OpenTrawl.app`, so remote archive requests
/// inherit the app bundle's Apple Photos Full Access and Full Disk Access TCC
/// grants cleanly without manual `nohup` processes.
@MainActor
@Observable
final class RemoteArchiveServerController {
  nonisolated static let serveArchiveOverTailscaleKey = "OpenTrawlServeArchiveOverTailscale"
  nonisolated static let defaultListenAddress = "127.0.0.1:7391"
  nonisolated static let defaultPortDisplay = "7391"

  private static let logger = Logger(subsystem: "app.opentrawl.trawl", category: "remote-server")
  private static let cleanUptimeResetThresholdSeconds: TimeInterval = 30
  private static let portConflictRetryDelaySeconds: Duration = .seconds(60)
  private static let standardBackoffDelays: [Duration] = [
    .seconds(2), .seconds(4), .seconds(8), .seconds(16), .seconds(30), .seconds(60),
  ]
  private static let tailscaleUnavailableBackoffDelays: [Duration] = [
    .seconds(5), .seconds(10), .seconds(20), .seconds(30), .seconds(60),
  ]

  private let runtimeConfiguration: TrawlRuntimeConfiguration
  private let defaults: UserDefaults
  private let stateRootURL: URL
  private let isTailscaleInstalledProvider: @Sendable () -> Bool

  private var supervisionTask: Task<Void, Never>?
  private var activeProcess: ManagedServerProcess?
  private var isShuttingDown = false
  private var supervisionGeneration: UInt64 = 0
  private var receivedPreferredRemoteArchiveAddress = false

  private(set) var explicitUserPreference: Bool?
  private(set) var isRemoteArchiveMode: Bool
  private(set) var localAccessTokenSummary: RemoteArchiveLocalAccessTokenSummary
  private(set) var isTailscaleAvailable: Bool
  private(set) var state: RemoteArchiveServerState

  var isSharingEnabled: Bool {
    if let explicitUserPreference {
      return explicitUserPreference
    }
    return !isRemoteArchiveMode
      && localAccessTokenSummary.hasActiveLocalToken
      && isTailscaleAvailable
  }

  var activeLocalTokenCount: Int {
    localAccessTokenSummary.activeLocalTokenCount
  }

  var serverArguments: [String] {
    ["mcp", "--http", "--tailscale", "--listen", Self.defaultListenAddress]
  }

  init(
    runtimeConfiguration: TrawlRuntimeConfiguration,
    isRemoteArchiveMode: Bool,
    defaults: UserDefaults = .standard,
    stateRootURL: URL? = nil,
    isTailscaleInstalledProvider: @escaping @Sendable () -> Bool = RemoteArchiveServerController
      .defaultIsTailscaleInstalled,
    startImmediately: Bool = false
  ) {
    self.runtimeConfiguration = runtimeConfiguration
    self.defaults = defaults
    if let stateRootURL {
      self.stateRootURL = stateRootURL
    } else if let configuredRoot = runtimeConfiguration.stateRoot, !configuredRoot.isEmpty {
      self.stateRootURL = URL(fileURLWithPath: configuredRoot, isDirectory: true)
    } else {
      self.stateRootURL = FileManager.default.homeDirectoryForCurrentUser.appending(
        path: ".opentrawl",
        directoryHint: .isDirectory
      )
    }
    self.isTailscaleInstalledProvider = isTailscaleInstalledProvider
    self.explicitUserPreference = defaults.object(forKey: Self.serveArchiveOverTailscaleKey) as? Bool
    self.isRemoteArchiveMode = isRemoteArchiveMode
    self.localAccessTokenSummary = RemoteArchiveLocalAccessTokenSummary.read(
      fromStateRootURL: self.stateRootURL)
    self.isTailscaleAvailable = isTailscaleInstalledProvider()
    self.state = isRemoteArchiveMode ? .disabledInRemoteMode : .stopped

    if startImmediately {
      reconcileServerProcess()
    }
  }

  func setSharingEnabled(_ enabled: Bool) {
    explicitUserPreference = enabled
    defaults.set(enabled, forKey: Self.serveArchiveOverTailscaleKey)
    reconcileServerProcess()
  }

  func updateRemoteArchiveMode(_ isRemote: Bool) {
    let modeChanged = isRemote != isRemoteArchiveMode
    isRemoteArchiveMode = isRemote
    if !isRemote {
      localAccessTokenSummary = RemoteArchiveLocalAccessTokenSummary.read(
        fromStateRootURL: stateRootURL)
      isTailscaleAvailable = isTailscaleInstalledProvider()
    }
    if modeChanged || (supervisionTask == nil && isSharingEnabled && !isRemote) {
      reconcileServerProcess()
    }
  }

  func refreshLocalAccessTokenStatus() {
    localAccessTokenSummary = RemoteArchiveLocalAccessTokenSummary.read(
      fromStateRootURL: stateRootURL)
    isTailscaleAvailable = isTailscaleInstalledProvider()
    if !isRemoteArchiveMode && isSharingEnabled && supervisionTask == nil {
      reconcileServerProcess()
    }
  }

  func retryNow() {
    localAccessTokenSummary = RemoteArchiveLocalAccessTokenSummary.read(
      fromStateRootURL: stateRootURL)
    isTailscaleAvailable = isTailscaleInstalledProvider()
    stopCurrentSupervision()
    reconcileServerProcess()
  }

  func stopSynchronouslyForTermination() {
    isShuttingDown = true
    supervisionGeneration &+= 1
    supervisionTask?.cancel()
    supervisionTask = nil
    activeProcess?.terminateGracefullySynchronously(graceSeconds: 1.5)
    activeProcess = nil
    state = .stopped
  }

  private func reconcileServerProcess() {
    guard !isShuttingDown else { return }
    if isRemoteArchiveMode {
      stopCurrentSupervision()
      state = .disabledInRemoteMode
      return
    }
    guard isSharingEnabled else {
      stopCurrentSupervision()
      state = .stopped
      return
    }
    guard supervisionTask == nil else { return }

    guard FileManager.default.isExecutableFile(atPath: runtimeConfiguration.helperURL.path) else {
      state = .failed(message: "OpenTrawl's bundled helper is missing. Rebuild or reinstall the app.")
      return
    }

    supervisionGeneration &+= 1
    let generation = supervisionGeneration
    state = .starting
    supervisionTask = Task { @MainActor [weak self] in
      await self?.runSupervisionLoop(generation: generation)
    }
  }

  private func stopCurrentSupervision() {
    supervisionGeneration &+= 1
    supervisionTask?.cancel()
    supervisionTask = nil
    if let running = activeProcess {
      activeProcess = nil
      Task.detached(priority: .utility) {
        running.terminateGracefullySynchronously(graceSeconds: 1.5)
      }
    }
  }

  private func runSupervisionLoop(generation: UInt64) async {
    var consecutiveFailureCount = 0
    while !Task.isCancelled && !isShuttingDown && generation == supervisionGeneration {
      guard !isRemoteArchiveMode, isSharingEnabled else { break }

      state = .starting
      receivedPreferredRemoteArchiveAddress = false
      let launchedAt = Date()
      let managedProcess = ManagedServerProcess(
        executableURL: runtimeConfiguration.helperURL,
        arguments: serverArguments,
        stateRoot: runtimeConfiguration.stateRoot,
        onOutputLine: { [weak self] line in
          guard let parsedAddress = RemoteArchiveServerController.parseAdvertisedArchiveURL(fromOutputLine: line) else {
            return
          }
          Task { @MainActor [weak self] in
            guard let self, self.supervisionGeneration == generation else { return }
            if parsedAddress.isPreferredRemoteArchiveLine || !self.receivedPreferredRemoteArchiveAddress {
              if parsedAddress.isPreferredRemoteArchiveLine {
                self.receivedPreferredRemoteArchiveAddress = true
              }
              self.state = .running(advertisedAddress: parsedAddress.address)
            }
          }
        }
      )

      do {
        try managedProcess.start()
      } catch {
        Self.logger.error("Failed to launch bundled Tailscale archive server: \(error.localizedDescription, privacy: .public)")
        state = .failed(message: "Could not start the Tailscale archive server.")
        break
      }

      activeProcess = managedProcess
      let exitOutcome = await managedProcess.waitForExitAndCollectedOutput()
      if activeProcess === managedProcess {
        activeProcess = nil
      }

      guard !Task.isCancelled && !isShuttingDown && generation == supervisionGeneration else {
        break
      }
      guard !isRemoteArchiveMode && isSharingEnabled else {
        break
      }

      let uptime = Date().timeIntervalSince(launchedAt)
      if uptime >= Self.cleanUptimeResetThresholdSeconds {
        consecutiveFailureCount = 0
      }

      let classification = Self.classifyUnexpectedExit(
        output: exitOutcome.combinedOutput,
        exitCode: exitOutcome.exitCode
      )
      let delay: Duration
      switch classification {
      case .portAlreadyInUse(let listenAddress):
        Self.logger.warning("Remote archive server port already in use (\(listenAddress, privacy: .public)); pausing retries")
        state = .portAlreadyInUse(listenAddress: listenAddress)
        delay = Self.portConflictRetryDelaySeconds
      case .tailscaleUnavailable:
        Self.logger.info("Tailscale is not running; waiting before retrying remote archive server")
        state = .tailscaleUnavailable
        let index = min(consecutiveFailureCount, Self.tailscaleUnavailableBackoffDelays.count - 1)
        delay = Self.tailscaleUnavailableBackoffDelays[index]
      case .other(let message):
        Self.logger.error("Remote archive server exited unexpectedly (\(exitOutcome.exitCode)): \(message, privacy: .private)")
        state = .failed(message: message)
        let index = min(consecutiveFailureCount, Self.standardBackoffDelays.count - 1)
        delay = Self.standardBackoffDelays[index]
      }
      consecutiveFailureCount += 1

      try? await Task.sleep(for: delay)
    }

    if generation == supervisionGeneration {
      supervisionTask = nil
      if isRemoteArchiveMode {
        state = .disabledInRemoteMode
      } else if !isSharingEnabled {
        state = .stopped
      }
    }
  }

  struct ParsedAdvertisedArchiveAddress: Sendable, Equatable {
    let address: String
    let isPreferredRemoteArchiveLine: Bool
  }

  nonisolated static func parseAdvertisedArchiveURL(
    fromOutputLine rawLine: String
  ) -> ParsedAdvertisedArchiveAddress? {
    let trimmed = rawLine.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !trimmed.isEmpty else { return nil }
    let lower = trimmed.lowercased()
    let isRemoteArchiveLine = lower.hasPrefix("remote archive")
    let isListeningLine = lower.hasPrefix("listening")
    guard isRemoteArchiveLine || isListeningLine else { return nil }

    for token in trimmed.split(whereSeparator: \.isWhitespace) {
      let candidate = String(token)
      if candidate.hasPrefix("http://") || candidate.hasPrefix("https://"),
        let url = URL(string: candidate),
        url.host != nil
      {
        return ParsedAdvertisedArchiveAddress(
          address: candidate,
          isPreferredRemoteArchiveLine: isRemoteArchiveLine
        )
      }
    }
    return nil
  }

  nonisolated static func classifyUnexpectedExit(
    output: String,
    exitCode: Int32
  ) -> RemoteArchiveServerExitClassification {
    let lower = output.lowercased()
    if lower.contains("address already in use") {
      let listenAddress = extractListenAddress(from: output) ?? defaultListenAddress
      return .portAlreadyInUse(listenAddress: listenAddress)
    }
    if lower.contains("tailscale is not running") {
      return .tailscaleUnavailable
    }
    let firstUsefulLine = output
      .split(whereSeparator: \.isNewline)
      .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
      .first { line in
        !line.isEmpty && !line.hasPrefix("Next:") && !line.hasPrefix("Help:")
      }
    if let firstUsefulLine {
      return .other(message: firstUsefulLine)
    }
    return .other(message: "Tailscale archive server stopped unexpectedly (exit code \(exitCode)).")
  }

  private nonisolated static func extractListenAddress(from output: String) -> String? {
    guard let range = output.range(of: "Cannot listen on ") else {
      return nil
    }
    let remainder = output[range.upperBound...]
    guard let colonSpace = remainder.range(of: ": ") else {
      return nil
    }
    let candidate = String(remainder[..<colonSpace.lowerBound]).trimmingCharacters(
      in: .whitespacesAndNewlines)
    return candidate.isEmpty ? nil : candidate
  }

  nonisolated static func defaultIsTailscaleInstalled() -> Bool {
    let candidates = [
      "/Applications/Tailscale.app/Contents/MacOS/Tailscale",
      "/opt/homebrew/bin/tailscale",
      "/usr/local/bin/tailscale",
      "/run/current-system/sw/bin/tailscale",
      "/nix/var/nix/profiles/default/bin/tailscale",
      "/usr/bin/tailscale",
    ]
    return candidates.contains { FileManager.default.isExecutableFile(atPath: $0) }
  }
}

private struct ManagedServerExitOutcome: Sendable {
  let exitCode: Int32
  let combinedOutput: String
}

private final class ManagedServerProcess: @unchecked Sendable {
  private static let maximumCapturedDiagnosticBytes = 8 * 1024

  private let process = Process()
  private let stdoutPipe = Pipe()
  private let stderrPipe = Pipe()
  private let outputLock = NSLock()
  private let exitLock = NSLock()
  private let exitSemaphore = DispatchSemaphore(value: 0)
  private let onOutputLine: @Sendable (String) -> Void

  private var capturedOutputBytes = Data()
  private var exitCode: Int32?

  init(
    executableURL: URL,
    arguments: [String],
    stateRoot: String?,
    onOutputLine: @escaping @Sendable (String) -> Void
  ) {
    self.onOutputLine = onOutputLine
    process.executableURL = executableURL
    process.arguments = arguments
    var environment = ProcessInfo.processInfo.environment
    environment["COLUMNS"] = "200"
    if let stateRoot, !stateRoot.isEmpty {
      environment[TrawlRuntimeConfiguration.stateRootEnvironmentKey] = stateRoot
    }
    process.environment = environment
    process.standardInput = FileHandle.nullDevice
    process.standardOutput = stdoutPipe
    process.standardError = stderrPipe
    process.terminationHandler = { [weak self] finishedProcess in
      self?.recordExit(finishedProcess.terminationStatus)
    }
  }

  func start() throws {
    try process.run()
  }

  func waitForExitAndCollectedOutput() async -> ManagedServerExitOutcome {
    let stdoutTask = Task.detached(priority: .utility) {
      self.drainPipeLines(self.stdoutPipe.fileHandleForReading, forwardLines: true)
    }
    let stderrTask = Task.detached(priority: .utility) {
      self.drainPipeLines(self.stderrPipe.fileHandleForReading, forwardLines: false)
    }
    let exitTask = Task.detached(priority: .utility) {
      await self.waitForTerminationStatus()
    }

    let finalExitCode = await exitTask.value
    _ = await stdoutTask.value
    _ = await stderrTask.value
    let combined = outputLock.withLock {
      String(decoding: capturedOutputBytes, as: UTF8.self)
    }
    return ManagedServerExitOutcome(exitCode: finalExitCode, combinedOutput: combined)
  }

  func terminateGracefullySynchronously(graceSeconds: Double) {
    guard process.isRunning else { return }
    process.terminate()
    let deadline = Date().addingTimeInterval(graceSeconds)
    while process.isRunning && Date() < deadline {
      Thread.sleep(forTimeInterval: 0.025)
    }
    if process.isRunning {
      _ = Darwin.kill(process.processIdentifier, SIGKILL)
    }
  }

  private func recordExit(_ status: Int32) {
    exitLock.withLock {
      exitCode = status
    }
    exitSemaphore.signal()
  }

  private func waitForTerminationStatus() async -> Int32 {
    await withCheckedContinuation { continuation in
      DispatchQueue.global(qos: .utility).async { [self] in
        exitSemaphore.wait()
        let status = exitLock.withLock { exitCode ?? -1 }
        continuation.resume(returning: status)
      }
    }
  }

  private func drainPipeLines(_ handle: FileHandle, forwardLines: Bool) {
    var lineBuffer = Data()
    while true {
      let chunk: Data
      do {
        chunk = try handle.read(upToCount: 4096) ?? Data()
      } catch {
        break
      }
      if chunk.isEmpty {
        break
      }
      appendDiagnosticBytes(chunk)
      if forwardLines {
        lineBuffer.append(chunk)
        while let newlineIndex = lineBuffer.firstIndex(of: 0x0A) {
          let lineData = lineBuffer.prefix(upTo: newlineIndex)
          lineBuffer.removeSubrange(...newlineIndex)
          let line = String(decoding: lineData, as: UTF8.self)
          onOutputLine(line)
        }
      }
    }
    if forwardLines && !lineBuffer.isEmpty {
      let line = String(decoding: lineBuffer, as: UTF8.self)
      onOutputLine(line)
    }
  }

  private func appendDiagnosticBytes(_ chunk: Data) {
    outputLock.withLock {
      capturedOutputBytes.append(chunk)
      if capturedOutputBytes.count > Self.maximumCapturedDiagnosticBytes {
        let overflow = capturedOutputBytes.count - Self.maximumCapturedDiagnosticBytes
        capturedOutputBytes.removeFirst(overflow)
      }
    }
  }
}
