import Foundation
import Security
import TrawlClient

/// Where this build keeps the access token of a saved remote archive.
///
/// A release build signed with its provisioning profile uses the Data Protection
/// Keychain, which never shows an access prompt. A build without Keychain access
/// groups (a development or ad hoc build) cannot use it, and the login Keychain
/// ties its approval to the exact binary, so every rebuild would ask for the
/// login password again. Those builds keep the token in a private file instead.
enum RemoteArchiveAccessTokenStorage {
  case dataProtectionKeychain
  case privateFiles(directory: URL)

  private static let keychainService = "org.opentrawl.trawl.remote-archive"
  private static let keychainAccessGroupsEntitlement = "keychain-access-groups"

  static func forRunningApp(bundle: Bundle = .main) -> Self {
    if runningAppHasKeychainAccessGroups() {
      return .dataProtectionKeychain
    }
    let applicationSupport =
      FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
      ?? FileManager.default.homeDirectoryForCurrentUser
        .appending(path: "Library/Application Support", directoryHint: .isDirectory)
    return .privateFiles(
      directory:
        applicationSupport
        .appending(path: bundle.bundleIdentifier ?? "org.opentrawl.trawl", directoryHint: .isDirectory)
        .appending(path: "RemoteArchiveAccessTokens", directoryHint: .isDirectory)
    )
  }

  func hasAccessToken(forOriginKey originKey: String) -> Bool {
    switch self {
    case .dataProtectionKeychain:
      SecItemCopyMatching(Self.keychainQuery(forOriginKey: originKey) as CFDictionary, nil)
        == errSecSuccess
    case .privateFiles(let directory):
      FileManager.default.fileExists(
        atPath: Self.tokenFile(in: directory, forOriginKey: originKey).path)
    }
  }

  func readAccessToken(forOriginKey originKey: String) -> String? {
    switch self {
    case .dataProtectionKeychain:
      var query = Self.keychainQuery(forOriginKey: originKey)
      query[kSecReturnData as String] = true
      query[kSecMatchLimit as String] = kSecMatchLimitOne
      var item: CFTypeRef?
      guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
        let data = item as? Data
      else {
        return nil
      }
      return String(data: data, encoding: .utf8)
    case .privateFiles(let directory):
      guard
        let data = try? Data(contentsOf: Self.tokenFile(in: directory, forOriginKey: originKey))
      else {
        return nil
      }
      return String(data: data, encoding: .utf8)
    }
  }

  func writeAccessToken(_ token: String, forOriginKey originKey: String) throws {
    let tokenData = Data(token.utf8)
    switch self {
    case .dataProtectionKeychain:
      let query = Self.keychainQuery(forOriginKey: originKey)
      let updateStatus = SecItemUpdate(
        query as CFDictionary, [kSecValueData as String: tokenData] as CFDictionary)
      if updateStatus == errSecSuccess {
        return
      }
      guard updateStatus == errSecItemNotFound else {
        throw Self.storageFailure("save", detail: "Keychain status \(updateStatus)")
      }
      var addQuery = query
      addQuery[kSecValueData as String] = tokenData
      addQuery[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
      let addStatus = SecItemAdd(addQuery as CFDictionary, nil)
      guard addStatus == errSecSuccess else {
        throw Self.storageFailure("save", detail: "Keychain status \(addStatus)")
      }
    case .privateFiles(let directory):
      do {
        try FileManager.default.createDirectory(
          at: directory,
          withIntermediateDirectories: true,
          attributes: [.posixPermissions: 0o700]
        )
        let file = Self.tokenFile(in: directory, forOriginKey: originKey)
        try tokenData.write(to: file, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: file.path)
      } catch {
        throw Self.storageFailure("save", detail: error.localizedDescription)
      }
    }
  }

  func deleteAccessToken(forOriginKey originKey: String) throws {
    switch self {
    case .dataProtectionKeychain:
      let status = SecItemDelete(Self.keychainQuery(forOriginKey: originKey) as CFDictionary)
      guard status == errSecSuccess || status == errSecItemNotFound else {
        throw Self.storageFailure("remove", detail: "Keychain status \(status)")
      }
    case .privateFiles(let directory):
      let file = Self.tokenFile(in: directory, forOriginKey: originKey)
      guard FileManager.default.fileExists(atPath: file.path) else { return }
      do {
        try FileManager.default.removeItem(at: file)
      } catch {
        throw Self.storageFailure("remove", detail: error.localizedDescription)
      }
    }
  }

  private static func runningAppHasKeychainAccessGroups() -> Bool {
    guard let task = SecTaskCreateFromSelf(nil),
      let groups = SecTaskCopyValueForEntitlement(
        task, keychainAccessGroupsEntitlement as CFString, nil) as? [String]
    else {
      return false
    }
    return !groups.isEmpty
  }

  private static func keychainQuery(forOriginKey originKey: String) -> [String: Any] {
    [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: keychainService,
      kSecAttrAccount as String: originKey,
      kSecUseDataProtectionKeychain as String: true,
    ]
  }

  private static func tokenFile(in directory: URL, forOriginKey originKey: String) -> URL {
    let fileName =
      originKey.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? originKey
    return directory.appending(path: fileName, directoryHint: .notDirectory)
  }

  private static func storageFailure(_ action: String, detail: String) -> TrawlClientError {
    .remoteRequestRejected("Could not \(action) the saved access token (\(detail)).")
  }
}
