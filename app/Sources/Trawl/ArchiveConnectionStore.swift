import Foundation
import Observation
import Security
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
  private static let keychainService = "org.opentrawl.trawl.remote-archive"

  private let localClient: ProcessTrawlClient
  private let defaults: UserDefaults
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

  var activeRemoteURLString: String? {
    if case .remoteArchive(let url) = mode {
      return url.absoluteString
    }
    return nil
  }

  init(
    runtimeConfiguration: TrawlRuntimeConfiguration,
    defaults: UserDefaults = .standard,
    environment: [String: String] = ProcessInfo.processInfo.environment
  ) {
    let local = ProcessTrawlClient(configuration: runtimeConfiguration)
    self.localClient = local
    self.defaults = defaults

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
    let storedToken = storedOrigin.flatMap(Self.readBearerTokenFromKeychain(forOriginKey:))

    self.savedRemoteURLString = storedURLString
    self.savedRemoteOriginKey = storedOrigin
    self.hasSavedBearerToken = storedToken != nil
    self.isEnvironmentOverride = false

    if defaults.string(forKey: Self.modeKey) == "remote",
      let parsedStoredURL,
      let storedToken,
      !storedToken.isEmpty
    {
      let config = RemoteArchiveEndpointConfiguration(
        serverBaseURL: parsedStoredURL,
        bearerToken: storedToken
      )
      self.mode = .remoteArchive(serverBaseURL: parsedStoredURL)
      self.switchableClient = SwitchableTrawlClient(
        initialClient: HTTPTrawlClient(configuration: config)
      )
    } else {
      self.mode = .localMac
      self.switchableClient = SwitchableTrawlClient(initialClient: local)
    }
  }

  /// True only when the entered server URL shares the exact origin of the
  /// stored Keychain token so changing servers never reuses another host's token.
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
      let savedToken = Self.readBearerTokenFromKeychain(forOriginKey: candidateOrigin),
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

    try Self.writeBearerTokenToKeychain(effectiveToken, forOriginKey: candidateOrigin)
    if let previousOrigin = savedRemoteOriginKey, previousOrigin != candidateOrigin {
      try? Self.deleteBearerTokenFromKeychain(forOriginKey: previousOrigin)
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
      try Self.deleteBearerTokenFromKeychain(forOriginKey: savedRemoteOriginKey)
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

  private static func readBearerTokenFromKeychain(forOriginKey originKey: String) -> String? {
    let query: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: keychainService,
      kSecAttrAccount as String: originKey,
      kSecReturnData as String: true,
      kSecMatchLimit as String: kSecMatchLimitOne,
    ]
    var item: CFTypeRef?
    let status = SecItemCopyMatching(query as CFDictionary, &item)
    guard status == errSecSuccess, let data = item as? Data else {
      return nil
    }
    return String(data: data, encoding: .utf8)
  }

  private static func writeBearerTokenToKeychain(
    _ token: String,
    forOriginKey originKey: String
  ) throws {
    let tokenData = Data(token.utf8)
    let baseQuery: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: keychainService,
      kSecAttrAccount as String: originKey,
    ]
    let updateAttributes: [String: Any] = [
      kSecValueData as String: tokenData,
    ]
    let updateStatus = SecItemUpdate(baseQuery as CFDictionary, updateAttributes as CFDictionary)
    if updateStatus == errSecSuccess {
      return
    }
    if updateStatus == errSecItemNotFound {
      var addQuery = baseQuery
      addQuery[kSecValueData as String] = tokenData
      addQuery[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
      let addStatus = SecItemAdd(addQuery as CFDictionary, nil)
      if addStatus == errSecSuccess {
        return
      }
      throw TrawlClientError.remoteRequestRejected(
        "Could not save the access token to Keychain (status \(addStatus))."
      )
    }
    throw TrawlClientError.remoteRequestRejected(
      "Could not update the access token in Keychain (status \(updateStatus))."
    )
  }

  private static func deleteBearerTokenFromKeychain(forOriginKey originKey: String) throws {
    let query: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: keychainService,
      kSecAttrAccount as String: originKey,
    ]
    let status = SecItemDelete(query as CFDictionary)
    guard status == errSecSuccess || status == errSecItemNotFound else {
      throw TrawlClientError.remoteRequestRejected(
        "Could not remove the saved access token from Keychain (status \(status))."
      )
    }
  }
}
