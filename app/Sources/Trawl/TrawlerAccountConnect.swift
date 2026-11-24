import AppKit
import Foundation
import PermissionGuide
import SwiftUI
import TrawlClient
@preconcurrency import WebKit

/// Runs real local account connection commands through the bundled `trawl` helper
/// (`trawl gmail connect/disconnect`, `trawl twitter connect --from-env`,
/// `trawl twitter disconnect`). Passes X credentials only via child process
/// environment (never writing plaintext cookies to disk), drains output pipes
/// concurrently, enforces a timeout, and supports immediate cancellation.
enum TrawlerAccountConnector {
  enum ConnectionError: LocalizedError {
    case helperMissing
    case cancelled
    case timedOut
    case rejectedByProvider
    case wrongAccount
    case failed

    var errorDescription: String? {
      switch self {
      case .helperMissing:
        "OpenTrawl's helper is missing from this build."
      case .cancelled:
        "Sign-in was cancelled."
      case .timedOut:
        "Connecting took too long. Check your internet connection and try again."
      case .rejectedByProvider:
        "X did not accept that sign-in. Sign in to x.com again and retry."
      case .wrongAccount:
        "This archive already belongs to a different X account."
      case .failed:
        "OpenTrawl could not finish connecting this account. Try again."
      }
    }
  }

  static func connectGmail(configuration: TrawlRuntimeConfiguration) async throws {
    try await runHelper(
      configuration: configuration,
      arguments: ["gmail", "connect"],
      extraEnvironment: [:],
      timeoutSeconds: 180
    )
  }

  static func disconnectGmail(configuration: TrawlRuntimeConfiguration) async throws {
    try await runHelper(
      configuration: configuration,
      arguments: ["gmail", "disconnect"],
      extraEnvironment: [:],
      timeoutSeconds: 30
    )
  }

  static func connectTwitter(
    authToken: String,
    ct0: String,
    configuration: TrawlRuntimeConfiguration
  ) async throws {
    try await runHelper(
      configuration: configuration,
      arguments: ["twitter", "connect", "--from-env"],
      extraEnvironment: [
        "AUTH_TOKEN": authToken,
        "CT0": ct0,
      ],
      timeoutSeconds: 30
    )
  }

  static func disconnectTwitter(configuration: TrawlRuntimeConfiguration) async throws {
    try await runHelper(
      configuration: configuration,
      arguments: ["twitter", "disconnect"],
      extraEnvironment: [:],
      timeoutSeconds: 30
    )
  }

  private static func runHelper(
    configuration: TrawlRuntimeConfiguration,
    arguments: [String],
    extraEnvironment: [String: String],
    timeoutSeconds: TimeInterval
  ) async throws {
    guard FileManager.default.isExecutableFile(atPath: configuration.helperURL.path) else {
      throw ConnectionError.helperMissing
    }
    let process = Process()
    process.executableURL = configuration.helperURL
    process.arguments = arguments
    var environment = ProcessInfo.processInfo.environment
    if let stateRoot = configuration.stateRoot, !stateRoot.isEmpty {
      environment[TrawlRuntimeConfiguration.stateRootEnvironmentKey] = stateRoot
    }
    for (key, value) in extraEnvironment {
      environment[key] = value
    }
    process.environment = environment

    let stdoutPipe = Pipe()
    let stderrPipe = Pipe()
    process.standardOutput = stdoutPipe
    process.standardError = stderrPipe

    final class HelperRunState: @unchecked Sendable {
      private let lock = NSLock()
      private var didResume = false
      private var cancelled = false
      private var stdoutData = Data()
      private var stderrData = Data()
      private let continuation: CheckedContinuation<Void, Error>

      init(continuation: CheckedContinuation<Void, Error>) {
        self.continuation = continuation
      }

      func setStdout(_ data: Data) {
        lock.lock()
        stdoutData = data
        lock.unlock()
      }

      func setStderr(_ data: Data) {
        lock.lock()
        stderrData = data
        lock.unlock()
      }

      func markCancelled() {
        lock.lock()
        cancelled = true
        lock.unlock()
      }

      func snapshot() -> (cancelled: Bool, stdout: Data, stderr: Data) {
        lock.lock()
        defer { lock.unlock() }
        return (cancelled, stdoutData, stderrData)
      }

      func finish(_ result: Result<Void, Error>) {
        lock.lock()
        guard !didResume else {
          lock.unlock()
          return
        }
        didResume = true
        lock.unlock()
        continuation.resume(with: result)
      }
    }

    final class CancellationFlag: @unchecked Sendable {
      private let lock = NSLock()
      private var state: HelperRunState?
      private var cancelledBeforeStart = false

      func attach(_ state: HelperRunState) {
        lock.lock()
        self.state = state
        let wasCancelled = cancelledBeforeStart
        lock.unlock()
        if wasCancelled {
          state.markCancelled()
          state.finish(.failure(ConnectionError.cancelled))
        }
      }

      func cancel(process: Process) {
        lock.lock()
        cancelledBeforeStart = true
        let attachedState = state
        lock.unlock()
        if process.isRunning {
          process.terminate()
        }
        attachedState?.markCancelled()
        attachedState?.finish(.failure(ConnectionError.cancelled))
      }
    }

    let cancellation = CancellationFlag()

    try await withTaskCancellationHandler {
      try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
        let state = HelperRunState(continuation: continuation)
        cancellation.attach(state)

        let outputGroup = DispatchGroup()

        outputGroup.enter()
        DispatchQueue.global(qos: .userInitiated).async {
          state.setStdout(stdoutPipe.fileHandleForReading.readDataToEndOfFile())
          outputGroup.leave()
        }
        outputGroup.enter()
        DispatchQueue.global(qos: .userInitiated).async {
          state.setStderr(stderrPipe.fileHandleForReading.readDataToEndOfFile())
          outputGroup.leave()
        }

        let timeoutTask = Task.detached(priority: .utility) {
          try? await Task.sleep(for: .seconds(timeoutSeconds))
          guard !Task.isCancelled else { return }
          if process.isRunning {
            process.terminate()
          }
          state.finish(.failure(ConnectionError.timedOut))
        }

        process.terminationHandler = { finishedProcess in
          timeoutTask.cancel()
          outputGroup.notify(queue: .global(qos: .userInitiated)) {
            let output = state.snapshot()
            if output.cancelled {
              state.finish(.failure(ConnectionError.cancelled))
            } else if finishedProcess.terminationStatus == 0 {
              state.finish(.success(()))
            } else {
              let stderrText = String(data: output.stderr, encoding: .utf8) ?? ""
              let stdoutText = String(data: output.stdout, encoding: .utf8) ?? ""
              let combined = (stderrText + "\n" + stdoutText).lowercased()
              if combined.contains("another account") || combined.contains("belongs to") {
                state.finish(.failure(ConnectionError.wrongAccount))
              } else if combined.contains("no longer accepts")
                || combined.contains("401")
                || combined.contains("403")
                || combined.contains("invalid")
              {
                state.finish(.failure(ConnectionError.rejectedByProvider))
              } else {
                state.finish(.failure(ConnectionError.failed))
              }
            }
          }
        }

        do {
          try process.run()
        } catch {
          timeoutTask.cancel()
          state.finish(.failure(ConnectionError.failed))
        }
      }
    } onCancel: {
      cancellation.cancel(process: process)
    }
  }
}

/// Native macOS sheet to connect X.
/// 1. Lists installed browsers on this Mac (without reading any cookies until clicked)
///    so users can reuse their existing signed-in browser session with password managers
///    and passkeys intact.
/// 2. Leads with the embedded non-persistent `WKWebView` fallback when no browser store
///    is readable (for example when Full Disk Access is off), and keeps `Cancel` always
///    working at bottom-right.
struct XWebSignInSheet: View {
  @Environment(\.scenePhase) private var scenePhase

  let runtimeConfiguration: TrawlRuntimeConfiguration
  let onConnected: () -> Void
  let onDismiss: () -> Void

  @State private var installedBrowsers: [DetectedXBrowser] = []
  @State private var showingManualWebLogin = false
  @State private var activeTask: Task<Void, Never>?
  @State private var webAttemptID = UUID()
  @State private var isConnecting = false
  @State private var statusMessage = "OpenTrawl reads your posts, likes, bookmarks, mentions and older messages."
  @State private var errorMessage: String?

  private static let twitterTrawlerID = RegisteredTrawlerIdentity(registeredTrawlerIdentity: "twitter")

  private var readableBrowsers: [DetectedXBrowser] {
    installedBrowsers.filter(\.isReadableNow)
  }

  private var hasUnreadableBrowsers: Bool {
    installedBrowsers.contains { !$0.isReadableNow }
  }

  var body: some View {
    Group {
      if showingManualWebLogin {
        manualWebLoginSheet
      } else {
        chooserSheet
      }
    }
    .onAppear {
      refreshDetectedBrowsers()
      if readableBrowsers.isEmpty && installedBrowsers.isEmpty {
        showingManualWebLogin = true
      }
    }
    .onChange(of: scenePhase) { _, phase in
      if phase == .active {
        refreshDetectedBrowsers()
      }
    }
    .onDisappear {
      cancelActiveAttempt()
    }
  }

  private var chooserSheet: some View {
    VStack(alignment: .leading, spacing: 16) {
      HStack(alignment: .center, spacing: 14) {
        TrawlerIconView(registeredTrawler: Self.twitterTrawlerID, size: 48)
        VStack(alignment: .leading, spacing: 3) {
          Text("Connect X")
            .font(TrawlDesign.Typography.sectionHeader)
          Text(statusMessage)
            .font(TrawlDesign.Typography.smallLabel)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
        }
      }
      .padding(.horizontal, 20)
      .padding(.top, 20)

      Form {
        if !readableBrowsers.isEmpty {
          Section {
            ForEach(readableBrowsers) { browser in
              LabeledContent {
                Button("Use \(browser.displayName)") {
                  importFromBrowser(browser)
                }
                .disabled(isConnecting)
              } label: {
                Text(browser.displayName)
                if browser.requiresKeychainPrompt {
                  Text("macOS asks for \(browser.displayName)'s Keychain key. Choose Allow, not Always Allow.")
                } else {
                  Text("Reuses your signed-in \(browser.displayName) session with no extra prompts.")
                }
              }
            }
          } header: {
            HStack {
              Text("Use your browser's sign-in")
              Spacer()
              if let xURL = URL(string: "https://x.com") {
                Link("Open x.com", destination: xURL)
              }
            }
          }

          Section {
            LabeledContent {
              Button("Sign in…") {
                openManualWebLogin()
              }
              .disabled(isConnecting)
            } label: {
              Text("Sign in here")
              Text("For a username, password and verification code.")
            }
          }
        } else {
          Section {
            if hasUnreadableBrowsers {
              LabeledContent {
                Button("Open System Settings…") {
                  PermissionGuideController.openSystemSettings()
                }
                .disabled(isConnecting)
              } label: {
                Text("Use your browser's sign-in")
                Text("Works with passkeys and Sign in with Google or Apple. Needs Full Disk Access.")
              }
            }

            LabeledContent {
              Button("Sign in…") {
                openManualWebLogin()
              }
              .buttonStyle(.borderedProminent)
              .keyboardShortcut(.defaultAction)
              .disabled(isConnecting)
            } label: {
              Text("Sign in here")
              Text("For a username, password and verification code.")
            }
          }
        }
      }
      .formStyle(.grouped)
      .scrollDisabled(true)
      .fixedSize(horizontal: false, vertical: true)

      if isConnecting || errorMessage != nil {
        HStack(spacing: 8) {
          if isConnecting {
            ProgressView()
              .controlSize(.small)
            Text(statusMessage)
              .font(TrawlDesign.Typography.meta)
              .foregroundStyle(.secondary)
          } else if let errorMessage {
            Image(systemName: "exclamationmark.triangle.fill")
              .foregroundStyle(TrawlDesign.Colors.warningAmber)
            Text(errorMessage)
              .font(TrawlDesign.Typography.meta)
              .fixedSize(horizontal: false, vertical: true)
          }
          Spacer(minLength: 0)
        }
        .padding(.horizontal, 20)
      }

      HStack {
        Spacer()
        Button("Cancel") {
          cancelActiveAttempt()
          onDismiss()
        }
        .keyboardShortcut(.cancelAction)
      }
      .padding(.horizontal, 20)
      .padding(.bottom, 20)
    }
    .frame(width: 480)
  }

  private var manualWebLoginSheet: some View {
    VStack(spacing: 0) {
      HStack(alignment: .center, spacing: 12) {
        TrawlerIconView(registeredTrawler: Self.twitterTrawlerID, size: 32)
        VStack(alignment: .leading, spacing: 2) {
          Text("Sign in to X")
            .font(TrawlDesign.Typography.sectionHeader)
          Text(statusMessage)
            .font(TrawlDesign.Typography.smallLabel)
            .foregroundStyle(.secondary)
            .lineLimit(1)
        }
        Spacer()
      }
      .padding(.horizontal, 20)
      .padding(.vertical, 14)

      Divider()

      if let errorMessage {
        HStack(spacing: 8) {
          Image(systemName: "exclamationmark.triangle.fill")
            .foregroundStyle(TrawlDesign.Colors.warningAmber)
          Text(errorMessage)
            .font(TrawlDesign.Typography.meta)
            .fixedSize(horizontal: false, vertical: true)
          Spacer()
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 10)
        .background(TrawlDesign.Colors.warningAmberSurface)
      }

      XLoginWebView(
        attemptID: webAttemptID,
        onLoadFailed: {
          errorMessage = "Could not load x.com. Check your internet connection and try again."
        },
        onCapturedSession: { authToken, ct0 in
          connectWithSession(authToken: authToken, ct0: ct0, sourceName: "X")
        }
      )
      .id(webAttemptID)
      .frame(maxWidth: .infinity, maxHeight: .infinity)

      Divider()

      HStack(spacing: 12) {
        if !installedBrowsers.isEmpty {
          Button("Back") {
            cancelActiveAttempt()
            errorMessage = nil
            showingManualWebLogin = false
          }
        }
        if isConnecting {
          ProgressView()
            .controlSize(.small)
        }
        Spacer()
        Button("Cancel") {
          cancelActiveAttempt()
          onDismiss()
        }
        .keyboardShortcut(.cancelAction)
      }
      .padding(.horizontal, 20)
      .padding(.vertical, 14)
    }
    .frame(width: 520, height: 660)
  }

  private func openManualWebLogin() {
    errorMessage = nil
    webAttemptID = UUID()
    showingManualWebLogin = true
  }

  private func refreshDetectedBrowsers() {
    installedBrowsers = BrowserXSessionImporter.detectInstalledBrowsers()
  }

  private func cancelActiveAttempt() {
    activeTask?.cancel()
    activeTask = nil
    isConnecting = false
  }

  private func importFromBrowser(_ browser: DetectedXBrowser) {
    cancelActiveAttempt()
    errorMessage = nil
    isConnecting = true
    statusMessage = "Reading X sign-in from \(browser.displayName)…"
    activeTask = Task {
      do {
        let session = try await Task.detached(priority: .userInitiated) {
          try BrowserXSessionImporter.importSession(from: browser)
        }.value
        try Task.checkCancellation()
        await verifyAndFinish(
          authToken: session.authToken,
          ct0: session.ct0,
          sourceName: session.browserName
        )
      } catch is CancellationError {
        isConnecting = false
      } catch {
        isConnecting = false
        statusMessage = "OpenTrawl only reads your posts, likes, bookmarks, and older messages."
        errorMessage = error.localizedDescription
      }
    }
  }

  private func connectWithSession(authToken: String, ct0: String, sourceName: String) {
    guard !isConnecting else { return }
    cancelActiveAttempt()
    errorMessage = nil
    isConnecting = true
    activeTask = Task {
      await verifyAndFinish(authToken: authToken, ct0: ct0, sourceName: sourceName)
    }
  }

  @MainActor
  private func verifyAndFinish(authToken: String, ct0: String, sourceName: String) async {
    statusMessage = "Verifying your X account from \(sourceName)…"
    do {
      try await TrawlerAccountConnector.connectTwitter(
        authToken: authToken,
        ct0: ct0,
        configuration: runtimeConfiguration
      )
      isConnecting = false
      onConnected()
      onDismiss()
    } catch TrawlerAccountConnector.ConnectionError.cancelled {
      isConnecting = false
    } catch {
      isConnecting = false
      statusMessage = "OpenTrawl only reads your posts, likes, bookmarks, and older messages."
      errorMessage = error.localizedDescription
      if showingManualWebLogin {
        webAttemptID = UUID()
      }
    }
  }
}

private struct XLoginWebView: NSViewRepresentable {
  let attemptID: UUID
  let onLoadFailed: @MainActor () -> Void
  let onCapturedSession: @MainActor (String, String) -> Void

  func makeCoordinator() -> Coordinator {
    Coordinator(onLoadFailed: onLoadFailed, onCapturedSession: onCapturedSession)
  }

  func makeNSView(context: Context) -> WKWebView {
    let configuration = WKWebViewConfiguration()
    configuration.websiteDataStore = .nonPersistent()
    let webView = WKWebView(frame: .zero, configuration: configuration)
    webView.navigationDelegate = context.coordinator
    context.coordinator.startObserving(webView: webView)
    if let loginURL = URL(string: "https://x.com/i/flow/login") {
      webView.load(URLRequest(url: loginURL))
    }
    return webView
  }

  func updateNSView(_: WKWebView, context _: Context) {}

  static func dismantleNSView(_ nsView: WKWebView, coordinator: Coordinator) {
    coordinator.stopObserving()
    nsView.navigationDelegate = nil
    nsView.stopLoading()
  }

  @MainActor
  final class Coordinator: NSObject, WKNavigationDelegate, WKHTTPCookieStoreObserver {
    private let onLoadFailed: @MainActor () -> Void
    private let onCapturedSession: @MainActor (String, String) -> Void
    private var hasDelivered = false
    private weak var webView: WKWebView?
    private weak var cookieStore: WKHTTPCookieStore?

    init(
      onLoadFailed: @escaping @MainActor () -> Void,
      onCapturedSession: @escaping @MainActor (String, String) -> Void
    ) {
      self.onLoadFailed = onLoadFailed
      self.onCapturedSession = onCapturedSession
    }

    func startObserving(webView: WKWebView) {
      self.webView = webView
      let store = webView.configuration.websiteDataStore.httpCookieStore
      self.cookieStore = store
      store.add(self)
    }

    func stopObserving() {
      hasDelivered = true
      cookieStore?.remove(self)
      cookieStore = nil
      webView = nil
    }

    nonisolated func cookiesDidChange(in cookieStore: WKHTTPCookieStore) {
      Task { @MainActor in
        self.inspectCookiesIfSignedIn(in: cookieStore)
      }
    }

    func webView(_ webView: WKWebView, didFinish _: WKNavigation!) {
      inspectCookiesIfSignedIn(in: webView.configuration.websiteDataStore.httpCookieStore)
    }

    func webView(_: WKWebView, didFailProvisionalNavigation _: WKNavigation!, withError error: Error) {
      let nsError = error as NSError
      guard nsError.code != NSURLErrorCancelled else { return }
      onLoadFailed()
    }

    private func inspectCookiesIfSignedIn(in store: WKHTTPCookieStore) {
      guard !hasDelivered else { return }
      // Wait until X leaves the login flow so we never capture a guest `ct0` before sign-in completes.
      let currentPath = webView?.url?.path.lowercased() ?? ""
      guard !currentPath.isEmpty && !currentPath.contains("/flow/login") else { return }
      let now = Date()
      store.getAllCookies { [weak self] cookies in
        Task { @MainActor [weak self] in
          guard let self, !self.hasDelivered else { return }
          var xCom: [String: String] = [:]
          var twitterCom: [String: String] = [:]
          for cookie in cookies {
            if let exp = cookie.expiresDate, exp < now { continue }
            guard BrowserXSessionImporter.isXDomain(cookie.domain) else { continue }
            guard cookie.name == "auth_token" || cookie.name == "ct0", !cookie.value.isEmpty else {
              continue
            }
            if cookie.domain.lowercased().contains("x.com") {
              xCom[cookie.name] = cookie.value
            } else {
              twitterCom[cookie.name] = cookie.value
            }
          }
          let authToken = xCom["auth_token"] ?? twitterCom["auth_token"]
          let ct0 = (xCom["auth_token"] != nil ? xCom["ct0"] : nil) ?? twitterCom["ct0"]
          if let authToken, let ct0 {
            self.hasDelivered = true
            self.cookieStore?.remove(self)
            self.onCapturedSession(authToken, ct0)
          }
        }
      }
    }
  }
}
