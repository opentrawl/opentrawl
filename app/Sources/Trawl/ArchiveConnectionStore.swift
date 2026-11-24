import Foundation
import Observation
import TrawlClient

public enum ArchiveConnectionMode: Sendable, Equatable {
  case localMac
  case remoteArchive(serverBaseURL: URL)
}

@MainActor
@Observable
final class ArchiveConnectionStore {
  private static let modeKey = "OpenTrawlArchiveConnectionMode"
  private static let savedRemoteURLKey = "OpenTrawlRemoteArchiveURL"

  private let localClient: ProcessTrawlClient
  private let defaults: UserDefaults
  private let accessTokenStorage: RemoteArchiveAccessTokenStorage
  let switchableClient: SwitchableTrawlClient

  private(set) var mode: ArchiveConnectionMode
  private(set) var savedRemoteURLString: String
  private(set) var savedRemoteOriginKey: String?
  private(set) var hasSavedBearerToken: Bool
  private(set) var isEnvironmentOverride: Bool
  private(set) var connectionRevision: UInt64 = 0

  var isRemote: Bool {
    if case .remoteArchive = mode { return true }
    return false
  }

  var activeHostDisplayName: String {
    switch mode {
    case .localMac:
      return OperationalCopy.Connection.thisMac
    case .remoteArchive(let url):
      return url.host ?? url.absoluteString
    }
  }

  /// The host as a person names it: "mac-mini" for "mac-mini.example.ts.net".
  var activeHostShortDisplayName: String {
    switch mode {
    case .localMac:
      return OperationalCopy.Connection.thisMac
    case .remoteArchive(let url):
      return RemoteArchiveEndpointConfiguration.shortDisplayHostName(for: url)
    }
  }

  var activeRemoteURLString: String? {
    if case .remoteArchive(let url) = mode {
      return url.absoluteString
    }
    return nil
  }

  init(
    runtimeConfiguration: TrawlRuntimeConfiguration,
    defaults: UserDefaults = .standard,
    environment: [String: String] = ProcessInfo.processInfo.environment,
    accessTokenStorage: RemoteArchiveAccessTokenStorage = .forRunningApp()
  ) {
    let local = ProcessTrawlClient(configuration: runtimeConfiguration)
    self.localClient = local
    self.defaults = defaults
    self.accessTokenStorage = accessTokenStorage

    if let envConfig = RemoteArchiveEndpointConfiguration.fromEnvironment(environment) {
      self.mode = .remoteArchive(serverBaseURL: envConfig.serverBaseURL)
      self.savedRemoteURLString = envConfig.serverBaseURL.absoluteString
      self.savedRemoteOriginKey = RemoteArchiveEndpointConfiguration.originKey(
        for: envConfig.serverBaseURL)
      self.hasSavedBearerToken = true
      self.isEnvironmentOverride = true
      self.switchableClient = SwitchableTrawlClient(
        initialClient: HTTPTrawlClient(configuration: envConfig)
      )
      return
    }

    let storedURLString = defaults.string(forKey: Self.savedRemoteURLKey) ?? ""
    let parsedStoredURL = RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(
      from: storedURLString)
    let storedOrigin = parsedStoredURL.map(RemoteArchiveEndpointConfiguration.originKey(for:))

    self.savedRemoteURLString = storedURLString
    self.savedRemoteOriginKey = storedOrigin
    self.hasSavedBearerToken =
      storedOrigin.map(accessTokenStorage.hasAccessToken(forOriginKey:)) ?? false
    self.isEnvironmentOverride = false

    // The token itself is read only to restore a remote connection, so a Mac
    // that reads its own archive never opens secret storage at launch.
    // When the user explicitly chose a remote archive, never silently fall back
    // to This Mac if the saved token is missing: stay in remote mode and report
    // the missing token so the user can re-enter it or switch intentionally.
    if defaults.string(forKey: Self.modeKey) == "remote",
      let parsedStoredURL
    {
      self.mode = .remoteArchive(serverBaseURL: parsedStoredURL)
      if let storedOrigin,
        let storedToken = accessTokenStorage.readAccessToken(forOriginKey: storedOrigin),
        !storedToken.isEmpty
      {
        let config = RemoteArchiveEndpointConfiguration(
          serverBaseURL: parsedStoredURL,
          bearerToken: storedToken
        )
        self.switchableClient = SwitchableTrawlClient(
          initialClient: HTTPTrawlClient(configuration: config)
        )
      } else {
        self.hasSavedBearerToken = false
        let hostDisplayName = RemoteArchiveEndpointConfiguration.shortDisplayHostName(
          for: parsedStoredURL)
        self.switchableClient = SwitchableTrawlClient(
          initialClient: MissingRemoteAccessTokenClient(hostDisplayName: hostDisplayName)
        )
      }
    } else {
      self.mode = .localMac
      self.switchableClient = SwitchableTrawlClient(initialClient: local)
    }
  }

  /// True only when the entered server URL shares the exact origin of the
  /// saved token so changing servers never reuses another host's token.
  func hasSavedBearerToken(for urlText: String) -> Bool {
    guard
      !isEnvironmentOverride,
      hasSavedBearerToken,
      let savedRemoteOriginKey,
      let candidateURL = RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(from: urlText)
    else {
      return false
    }
    return RemoteArchiveEndpointConfiguration.originKey(for: candidateURL) == savedRemoteOriginKey
  }

  /// Verifies the remote archive endpoint and token before persisting and
  /// switching the active client so invalid input never disturbs a working view.
  func connectToRemoteArchive(
    urlText: String,
    bearerToken: String
  ) async throws {
    guard
      let normalizedURL = RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(from: urlText)
    else {
      throw TrawlClientError.remoteServerURLInvalid
    }
    guard RemoteArchiveEndpointConfiguration.usesEncryptedOrLoopbackTransport(for: normalizedURL)
    else {
      throw TrawlClientError.remoteInsecureTransportRejected
    }
    let candidateOrigin = RemoteArchiveEndpointConfiguration.originKey(for: normalizedURL)
    let trimmedToken = bearerToken.trimmingCharacters(in: .whitespacesAndNewlines)
    let effectiveToken: String
    if !trimmedToken.isEmpty {
      effectiveToken = trimmedToken
    } else if !isEnvironmentOverride,
      candidateOrigin == savedRemoteOriginKey,
      let savedToken = accessTokenStorage.readAccessToken(forOriginKey: candidateOrigin),
      !savedToken.isEmpty
    {
      effectiveToken = savedToken
    } else {
      throw TrawlClientError.remoteAuthenticationRequired
    }

    let config = RemoteArchiveEndpointConfiguration(
      serverBaseURL: normalizedURL,
      bearerToken: effectiveToken
    )
    let candidateClient = HTTPTrawlClient(configuration: config)
    _ = try await candidateClient.status()

    try accessTokenStorage.writeAccessToken(effectiveToken, forOriginKey: candidateOrigin)
    if let previousOrigin = savedRemoteOriginKey, previousOrigin != candidateOrigin {
      try? accessTokenStorage.deleteAccessToken(forOriginKey: previousOrigin)
    }
    defaults.set(normalizedURL.absoluteString, forKey: Self.savedRemoteURLKey)
    defaults.set("remote", forKey: Self.modeKey)
    savedRemoteURLString = normalizedURL.absoluteString
    savedRemoteOriginKey = candidateOrigin
    hasSavedBearerToken = true
    isEnvironmentOverride = false
    mode = .remoteArchive(serverBaseURL: normalizedURL)
    switchableClient.replaceActiveClient(candidateClient)
    connectionRevision &+= 1
  }

  func switchToLocalMac() {
    guard isRemote else { return }
    defaults.set("local", forKey: Self.modeKey)
    isEnvironmentOverride = false
    mode = .localMac
    switchableClient.replaceActiveClient(localClient)
    connectionRevision &+= 1
  }

  func forgetSavedRemoteArchive() throws {
    if let savedRemoteOriginKey {
      try accessTokenStorage.deleteAccessToken(forOriginKey: savedRemoteOriginKey)
    }
    defaults.removeObject(forKey: Self.savedRemoteURLKey)
    defaults.set("local", forKey: Self.modeKey)
    savedRemoteURLString = ""
    savedRemoteOriginKey = nil
    hasSavedBearerToken = false
    isEnvironmentOverride = false
    if isRemote {
      mode = .localMac
      switchableClient.replaceActiveClient(localClient)
      connectionRevision &+= 1
    }
  }
}

/// Surfaces a missing saved token loudly instead of silently reading This Mac's
/// archive or opening onboarding when a remote connection was configured.
private struct MissingRemoteAccessTokenClient: TrawlClient {
  let hostDisplayName: String

  func status() async throws -> FederatedTrawlerStatusOperation {
    throw TrawlClientError.remoteSavedAccessTokenMissing(hostDisplayName: hostDisplayName)
  }

  func update(
    registeredTrawlers _: [RegisteredTrawlerIdentity],
    progress _: @escaping @Sendable (TrawlerArchiveUpdateProgress) -> Void
  ) async throws -> FederatedTrawlerArchiveUpdateOperation {
    throw TrawlClientError.remoteArchiveReadOnly
  }

  func search(
    _: TrawlArchiveSearchRequest
  ) async throws -> FederatedTrawlerSearchOperation {
    throw TrawlClientError.remoteSavedAccessTokenMissing(hostDisplayName: hostDisplayName)
  }

  func open(
    link _: GloballyRoutableTrawlLink,
    anchor _: RecordAnchorIdentifier
  ) async throws -> OpenResponse {
    throw TrawlClientError.remoteSavedAccessTokenMissing(hostDisplayName: hostDisplayName)
  }
}
