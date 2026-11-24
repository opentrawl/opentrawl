import AppKit
import Foundation
import SwiftUI
import TrawlClient
@preconcurrency import WebKit

/// Runs real local account connection commands through the bundled `trawl` helper
/// (`trawl gmail connect/disconnect`, `trawl twitter connect --file <temp>`,
/// `trawl twitter disconnect`). Never leaks cookies or tokens into logs or UI.
enum TrawlerAccountConnector {
  enum ConnectionError: LocalizedError {
    case helperMissing
    case failed(String)

    var errorDescription: String? {
      switch self {
      case .helperMissing:
        "OpenTrawl's bundled helper is missing. Rebuild the app."
      case .failed(let message):
        message.isEmpty ? "Could not complete account connection." : message
      }
    }
  }

  static func connectGmail(configuration: TrawlRuntimeConfiguration) async throws {
    try await runHelper(
      configuration: configuration,
      arguments: ["gmail", "connect"]
    )
  }

  static func disconnectGmail(configuration: TrawlRuntimeConfiguration) async throws {
    try await runHelper(
      configuration: configuration,
      arguments: ["gmail", "disconnect"]
    )
  }

  static func connectTwitter(
    authToken: String,
    ct0: String,
    configuration: TrawlRuntimeConfiguration
  ) async throws {
    let tempURL = FileManager.default.temporaryDirectory
      .appendingPathComponent("opentrawl-x-\(UUID().uuidString).env", isDirectory: false)
    let payload = "AUTH_TOKEN=\(authToken)\nCT0=\(ct0)\n"
    let data = Data(payload.utf8)

    FileManager.default.createFile(
      atPath: tempURL.path,
      contents: data,
      attributes: [.posixPermissions: NSNumber(value: Int16(0o600))]
    )
    defer {
      try? FileManager.default.removeItem(at: tempURL)
    }

    try await runHelper(
      configuration: configuration,
      arguments: ["twitter", "connect", "--file", tempURL.path]
    )
  }

  static func disconnectTwitter(configuration: TrawlRuntimeConfiguration) async throws {
    try await runHelper(
      configuration: configuration,
      arguments: ["twitter", "disconnect"]
    )
  }

  private static func runHelper(
    configuration: TrawlRuntimeConfiguration,
    arguments: [String]
  ) async throws {
    guard FileManager.default.isExecutableFile(atPath: configuration.helperURL.path) else {
      throw ConnectionError.helperMissing
    }
    let helperURL = configuration.helperURL
    let stateRoot = configuration.stateRoot
    try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
      DispatchQueue.global(qos: .userInitiated).async {
        let process = Process()
        process.executableURL = helperURL
        process.arguments = arguments
        var environment = ProcessInfo.processInfo.environment
        if let stateRoot, !stateRoot.isEmpty {
          environment[TrawlRuntimeConfiguration.stateRootEnvironmentKey] = stateRoot
        }
        process.environment = environment

        let stdoutPipe = Pipe()
        let stderrPipe = Pipe()
        process.standardOutput = stdoutPipe
        process.standardError = stderrPipe

        do {
          try process.run()
          process.waitUntilExit()
          let stderrData = stderrPipe.fileHandleForReading.readDataToEndOfFile()
          let stdoutData = stdoutPipe.fileHandleForReading.readDataToEndOfFile()
          if process.terminationStatus == 0 {
            continuation.resume()
          } else {
            let errText =
              String(data: stderrData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
              ?? ""
            let outText =
              String(data: stdoutData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
              ?? ""
            let combined = !errText.isEmpty ? errText : outText
            continuation.resume(throwing: ConnectionError.failed(combined))
          }
        } catch {
          continuation.resume(throwing: ConnectionError.failed(error.localizedDescription))
        }
      }
    }
  }
}

/// Native macOS sheet presenting x.com sign-in inside a clean non-persistent
/// WKWebView. As soon as X issues both `auth_token` and `ct0`, OpenTrawl hands
/// them to `trawl twitter connect --file <0600 temp>` and removes the temp file.
struct XWebSignInSheet: View {
  let runtimeConfiguration: TrawlRuntimeConfiguration
  let onConnected: () -> Void
  let onDismiss: () -> Void

  @State private var statusMessage = "Sign in to X below. OpenTrawl only reads your posts, bookmarks, likes, mentions, and older messages."
  @State private var isConnecting = false
  @State private var errorMessage: String?

  var body: some View {
    VStack(spacing: 0) {
      HStack(spacing: 12) {
        VStack(alignment: .leading, spacing: 2) {
          Text("Sign in to X")
            .font(.headline)
          Text(statusMessage)
            .font(.caption)
            .foregroundStyle(.secondary)
            .lineLimit(2)
        }
        Spacer()
        if isConnecting {
          ProgressView()
            .controlSize(.small)
        }
        Button("Cancel") {
          onDismiss()
        }
        .disabled(isConnecting)
      }
      .padding(.horizontal, 16)
      .padding(.vertical, 12)
      .background(TrawlDesign.Colors.surface)

      Divider()

      if let errorMessage {
        HStack(spacing: 8) {
          Image(systemName: "exclamationmark.triangle.fill")
            .foregroundStyle(TrawlDesign.Colors.warningAmber)
          Text(errorMessage)
            .font(.caption)
            .foregroundStyle(TrawlDesign.Colors.ink)
          Spacer()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .background(TrawlDesign.Colors.warningAmber.opacity(0.12))
      }

      XLoginWebView(
        onCapturedSession: { authToken, ct0 in
          guard !isConnecting else { return }
          isConnecting = true
          errorMessage = nil
          statusMessage = "Verifying your X account…"
          Task { @MainActor in
            do {
              try await TrawlerAccountConnector.connectTwitter(
                authToken: authToken,
                ct0: ct0,
                configuration: runtimeConfiguration
              )
              isConnecting = false
              onConnected()
              onDismiss()
            } catch {
              isConnecting = false
              statusMessage = "Sign in to X below."
              errorMessage = error.localizedDescription
            }
          }
        }
      )
    }
    .frame(width: 520, height: 680)
  }
}

private struct XLoginWebView: NSViewRepresentable {
  let onCapturedSession: @MainActor (String, String) -> Void

  func makeCoordinator() -> Coordinator {
    Coordinator(onCapturedSession: onCapturedSession)
  }

  func makeNSView(context: Context) -> WKWebView {
    let configuration = WKWebViewConfiguration()
    configuration.websiteDataStore = .nonPersistent()
    let webView = WKWebView(frame: .zero, configuration: configuration)
    webView.navigationDelegate = context.coordinator
    context.coordinator.startObservingCookies(in: webView.configuration.websiteDataStore.httpCookieStore)
    if let loginURL = URL(string: "https://x.com/i/flow/login") {
      webView.load(URLRequest(url: loginURL))
    }
    return webView
  }

  func updateNSView(_: WKWebView, context _: Context) {}

  @MainActor
  final class Coordinator: NSObject, WKNavigationDelegate, WKHTTPCookieStoreObserver {
    private let onCapturedSession: @MainActor (String, String) -> Void
    private var hasDelivered = false
    private weak var cookieStore: WKHTTPCookieStore?

    init(onCapturedSession: @escaping @MainActor (String, String) -> Void) {
      self.onCapturedSession = onCapturedSession
    }

    func startObservingCookies(in store: WKHTTPCookieStore) {
      self.cookieStore = store
      store.add(self)
    }

    nonisolated func cookiesDidChange(in cookieStore: WKHTTPCookieStore) {
      Task { @MainActor in
        self.inspectCookies(in: cookieStore)
      }
    }

    func webView(_ webView: WKWebView, didFinish _: WKNavigation!) {
      inspectCookies(in: webView.configuration.websiteDataStore.httpCookieStore)
    }

    private func inspectCookies(in store: WKHTTPCookieStore) {
      guard !hasDelivered else { return }
      store.getAllCookies { [weak self] cookies in
        Task { @MainActor [weak self] in
          guard let self, !self.hasDelivered else { return }
          var authToken: String?
          var ct0: String?
          for cookie in cookies {
            let domain = cookie.domain.lowercased()
            guard domain.hasSuffix("x.com") || domain.hasSuffix("twitter.com") else { continue }
            if cookie.name == "auth_token" && !cookie.value.isEmpty {
              authToken = cookie.value
            } else if cookie.name == "ct0" && !cookie.value.isEmpty {
              ct0 = cookie.value
            }
          }
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
