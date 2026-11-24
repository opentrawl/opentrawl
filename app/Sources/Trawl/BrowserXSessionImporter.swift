import CommonCrypto
import CryptoKit
import Foundation
import Security
import SQLite3

struct BrowserXSession: Sendable {
  let browserName: String
  let authToken: String
  let ct0: String
}

struct DetectedXBrowser: Identifiable, Sendable, Equatable {
  enum Kind: Sendable, Equatable {
    case safari(cookieFileURL: URL)
    case firefox(profileCookieDatabases: [URL])
    case chromium(cookieDatabases: [URL], keychainService: String, keychainAccount: String)
  }

  let id: String
  let displayName: String
  let requiresKeychainPrompt: Bool
  let isReadableNow: Bool
  let kind: Kind
}

enum BrowserXImportError: LocalizedError {
  case fullDiskAccessRequired(browserName: String)
  case noXSessionFound(browserName: String)
  case keychainCancelled(browserName: String)
  case keychainUnavailable(browserName: String)
  case decryptionFailed(browserName: String)
  case databaseReadFailed(browserName: String)

  var errorDescription: String? {
    switch self {
    case .fullDiskAccessRequired(let browserName):
      "Turn on Full Disk Access in System Settings so OpenTrawl can read \(browserName)'s saved X sign-in, or sign in inside OpenTrawl below."
    case .noXSessionFound(let browserName):
      "No active X sign-in was found in \(browserName). Sign in to x.com in \(browserName) first, or sign in inside OpenTrawl below."
    case .keychainCancelled(let browserName):
      "Keychain approval for \(browserName) was cancelled. Choose Allow when macOS asks, or sign in inside OpenTrawl below."
    case .keychainUnavailable(let browserName):
      "OpenTrawl could not find \(browserName)'s storage key in your macOS Keychain. Sign in inside OpenTrawl below instead."
    case .decryptionFailed(let browserName):
      "OpenTrawl could not decode \(browserName)'s saved cookies. Sign in inside OpenTrawl below instead."
    case .databaseReadFailed(let browserName):
      "OpenTrawl could not read \(browserName)'s cookie store. Sign in inside OpenTrawl below instead."
    }
  }
}

/// Reads an existing X (`x.com` / `twitter.com`) `auth_token` + `ct0` session
/// from a browser on this Mac ONLY when the user explicitly requests it.
/// Never reads cookies or touches Keychain on view appearance.
enum BrowserXSessionImporter {
  private static let tempDirectoryPrefix = "opentrawl-cookies-"

  static func cleanupStaleTemporaryCopies() {
    let fm = FileManager.default
    let tempRoot = fm.temporaryDirectory
    guard let entries = try? fm.contentsOfDirectory(
      at: tempRoot,
      includingPropertiesForKeys: nil,
      options: [.skipsHiddenFiles]
    ) else {
      return
    }
    for entry in entries where entry.lastPathComponent.hasPrefix(tempDirectoryPrefix) {
      try? fm.removeItem(at: entry)
    }
  }

  /// Detects installed browsers by file existence only — reads zero cookie bytes
  /// and triggers zero Keychain prompts.
  static func detectInstalledBrowsers(
    homeDirectory: URL = FileManager.default.homeDirectoryForCurrentUser
  ) -> [DetectedXBrowser] {
    var detected: [DetectedXBrowser] = []
    let fm = FileManager.default

    let safariCookies = homeDirectory
      .appendingPathComponent("Library/Containers/com.apple.Safari/Data/Library/Cookies/Cookies.binarycookies")
    if fm.fileExists(atPath: safariCookies.path) {
      detected.append(
        DetectedXBrowser(
          id: "safari",
          displayName: "Safari",
          requiresKeychainPrompt: false,
          isReadableNow: fm.isReadableFile(atPath: safariCookies.path),
          kind: .safari(cookieFileURL: safariCookies)
        )
      )
    }

    let geckoApps: [(id: String, name: String, relativeProfilesPath: String)] = [
      ("firefox", "Firefox", "Library/Application Support/Firefox/Profiles"),
      ("zen", "Zen", "Library/Application Support/zen/Profiles"),
    ]
    for app in geckoApps {
      let profilesDir = homeDirectory.appendingPathComponent(app.relativeProfilesPath)
      let dbs = allFiles(in: profilesDir, fileName: "cookies.sqlite")
      if !dbs.isEmpty {
        let readable = dbs.contains { fm.isReadableFile(atPath: $0.path) }
        detected.append(
          DetectedXBrowser(
            id: app.id,
            displayName: app.name,
            requiresKeychainPrompt: false,
            isReadableNow: readable,
            kind: .firefox(profileCookieDatabases: dbs)
          )
        )
      }
    }

    let chromiumApps: [(id: String, name: String, relativeBase: String, service: String, account: String)] = [
      ("chrome", "Chrome", "Library/Application Support/Google/Chrome", "Chrome Safe Storage", "Chrome"),
      ("arc", "Arc", "Library/Application Support/Arc/User Data", "Arc Safe Storage", "Arc"),
      ("brave", "Brave", "Library/Application Support/BraveSoftware/Brave-Browser", "Brave Safe Storage", "Brave"),
      ("edge", "Microsoft Edge", "Library/Application Support/Microsoft Edge", "Microsoft Edge Safe Storage", "Microsoft Edge"),
      ("vivaldi", "Vivaldi", "Library/Application Support/Vivaldi", "Vivaldi Safe Storage", "Vivaldi"),
      ("dia", "Dia", "Library/Application Support/Dia/User Data", "Dia Safe Storage", "Dia"),
      ("chrome-canary", "Chrome Canary", "Library/Application Support/Google/Chrome Canary", "Chrome Safe Storage", "Chrome"),
    ]
    for app in chromiumApps {
      let baseDir = homeDirectory.appendingPathComponent(app.relativeBase)
      let dbs = allChromiumCookieDatabases(in: baseDir)
      if !dbs.isEmpty {
        let readable = dbs.contains { fm.isReadableFile(atPath: $0.path) }
        detected.append(
          DetectedXBrowser(
            id: app.id,
            displayName: app.name,
            requiresKeychainPrompt: true,
            isReadableNow: readable,
            kind: .chromium(
              cookieDatabases: dbs,
              keychainService: app.service,
              keychainAccount: app.account
            )
          )
        )
      }
    }

    return detected
  }

  static func importSession(from browser: DetectedXBrowser) throws -> BrowserXSession {
    switch browser.kind {
    case .safari(let cookieFileURL):
      guard FileManager.default.isReadableFile(atPath: cookieFileURL.path) else {
        throw BrowserXImportError.fullDiskAccessRequired(browserName: browser.displayName)
      }
      let data: Data
      do {
        data = try Data(contentsOf: cookieFileURL)
      } catch {
        throw classifyReadError(error, browserName: browser.displayName)
      }
      guard let pair = parseSafariBinaryCookies(data) else {
        throw BrowserXImportError.noXSessionFound(browserName: browser.displayName)
      }
      return BrowserXSession(
        browserName: browser.displayName,
        authToken: pair.authToken,
        ct0: pair.ct0
      )

    case .firefox(let dbs):
      var sawPermissionError = false
      for dbURL in dbs {
        do {
          if let pair = try readFirefoxCookies(at: dbURL, browserName: browser.displayName) {
            return BrowserXSession(
              browserName: browser.displayName,
              authToken: pair.authToken,
              ct0: pair.ct0
            )
          }
        } catch BrowserXImportError.fullDiskAccessRequired {
          sawPermissionError = true
        }
      }
      if sawPermissionError {
        throw BrowserXImportError.fullDiskAccessRequired(browserName: browser.displayName)
      }
      throw BrowserXImportError.noXSessionFound(browserName: browser.displayName)

    case .chromium(let dbs, let service, let account):
      var sawPermissionError = false
      var cachedKey: Data?
      for dbURL in dbs {
        do {
          if let pair = try readChromiumCookies(
            at: dbURL,
            browserName: browser.displayName,
            keychainService: service,
            keychainAccount: account,
            cachedAESKey: &cachedKey
          ) {
            return BrowserXSession(
              browserName: browser.displayName,
              authToken: pair.authToken,
              ct0: pair.ct0
            )
          }
        } catch BrowserXImportError.fullDiskAccessRequired {
          sawPermissionError = true
        }
      }
      if sawPermissionError {
        throw BrowserXImportError.fullDiskAccessRequired(browserName: browser.displayName)
      }
      throw BrowserXImportError.noXSessionFound(browserName: browser.displayName)
    }
  }

  static func isXDomain(_ domain: String) -> Bool {
    let lower = domain.lowercased()
    return lower == "x.com" || lower == ".x.com" || lower.hasSuffix(".x.com")
      || lower == "twitter.com" || lower == ".twitter.com" || lower.hasSuffix(".twitter.com")
  }

  private struct SessionPair {
    let authToken: String
    let ct0: String
  }

  /// Pairs `auth_token` and `ct0` strictly from the same host family (`x.com` first,
  /// then `twitter.com`) so two different sessions are never mixed.
  private static func selectSameHostPair(
    xComCookies: [String: String],
    twitterComCookies: [String: String]
  ) -> SessionPair? {
    if let auth = xComCookies["auth_token"], !auth.isEmpty,
      let ct0 = xComCookies["ct0"], !ct0.isEmpty
    {
      return SessionPair(authToken: auth, ct0: ct0)
    }
    if let auth = twitterComCookies["auth_token"], !auth.isEmpty,
      let ct0 = twitterComCookies["ct0"], !ct0.isEmpty
    {
      return SessionPair(authToken: auth, ct0: ct0)
    }
    return nil
  }

  // MARK: - Safari .binarycookies parser

  private static func parseSafariBinaryCookies(_ data: Data, now: Date = Date()) -> SessionPair? {
    guard data.count >= 8 else { return nil }
    let magic = String(data: data.subdata(in: 0..<4), encoding: .ascii)
    guard magic == "cook" else { return nil }

    let pageCount = Int(readUInt32BE(data, at: 4))
    let headerEnd = 8 + pageCount * 4
    guard pageCount > 0, pageCount < 10_000, headerEnd <= data.count else { return nil }

    var pageSizes: [Int] = []
    pageSizes.reserveCapacity(pageCount)
    for idx in 0..<pageCount {
      pageSizes.append(Int(readUInt32BE(data, at: 8 + idx * 4)))
    }

    var xCom: [String: String] = [:]
    var twitterCom: [String: String] = [:]
    var offset = headerEnd
    for pageSize in pageSizes {
      guard pageSize >= 12, offset + pageSize <= data.count else { break }
      let page = data.subdata(in: offset..<(offset + pageSize))
      offset += pageSize
      extractCookiesFromSafariPage(page, now: now, xCom: &xCom, twitterCom: &twitterCom)
    }
    return selectSameHostPair(xComCookies: xCom, twitterComCookies: twitterCom)
  }

  private static func extractCookiesFromSafariPage(
    _ page: Data,
    now: Date,
    xCom: inout [String: String],
    twitterCom: inout [String: String]
  ) {
    guard page.count >= 12 else { return }
    let cookieCount = Int(readUInt32LE(page, at: 4))
    let offsetsEnd = 8 + cookieCount * 4
    guard cookieCount > 0, cookieCount < 50_000, offsetsEnd <= page.count else { return }

    let nowAppleRef = now.timeIntervalSinceReferenceDate

    for idx in 0..<cookieCount {
      let cookieOffset = Int(readUInt32LE(page, at: 8 + idx * 4))
      guard cookieOffset >= 0, cookieOffset + 48 <= page.count else { continue }
      let cookieSize = Int(readUInt32LE(page, at: cookieOffset))
      guard cookieSize >= 48, cookieOffset + cookieSize <= page.count else { continue }

      let expiration = readDoubleLE(page, at: cookieOffset + 40)
      if expiration > 0 && expiration < nowAppleRef {
        continue
      }

      let urlOffset = Int(readUInt32LE(page, at: cookieOffset + 16))
      let nameOffset = Int(readUInt32LE(page, at: cookieOffset + 20))
      let valueOffset = Int(readUInt32LE(page, at: cookieOffset + 28))

      guard let domain = readCString(page, from: cookieOffset + urlOffset, limit: cookieOffset + cookieSize),
        isXDomain(domain),
        let name = readCString(page, from: cookieOffset + nameOffset, limit: cookieOffset + cookieSize),
        name == "auth_token" || name == "ct0",
        let value = readCString(page, from: cookieOffset + valueOffset, limit: cookieOffset + cookieSize),
        !value.isEmpty
      else {
        continue
      }
      if domain.lowercased().contains("x.com") {
        xCom[name] = value
      } else {
        twitterCom[name] = value
      }
    }
  }

  // MARK: - Firefox / Zen SQLite reader

  private static func readFirefoxCookies(
    at dbURL: URL,
    browserName: String,
    now: Date = Date()
  ) throws -> SessionPair? {
    try withCopiedSQLiteDatabase(at: dbURL, browserName: browserName) { db in
      let sql = """
        SELECT host, name, value, expiry
        FROM moz_cookies
        WHERE (host LIKE '%x.com' OR host LIKE '%twitter.com')
          AND name IN ('auth_token', 'ct0')
        ORDER BY lastAccessed DESC;
        """
      var statement: OpaquePointer?
      guard sqlite3_prepare_v2(db, sql, -1, &statement, nil) == SQLITE_OK else {
        throw BrowserXImportError.databaseReadFailed(browserName: browserName)
      }
      defer { sqlite3_finalize(statement) }

      let nowUnix = Int64(now.timeIntervalSince1970)
      var xCom: [String: String] = [:]
      var twitterCom: [String: String] = [:]

      while true {
        let rc = sqlite3_step(statement)
        if rc == SQLITE_DONE { break }
        guard rc == SQLITE_ROW else {
          throw BrowserXImportError.databaseReadFailed(browserName: browserName)
        }
        guard let hostPtr = sqlite3_column_text(statement, 0),
          let namePtr = sqlite3_column_text(statement, 1),
          let valuePtr = sqlite3_column_text(statement, 2)
        else {
          continue
        }
        let expiry = sqlite3_column_int64(statement, 3)
        if expiry > 0 && expiry < nowUnix { continue }

        let host = String(cString: hostPtr)
        let name = String(cString: namePtr)
        let value = String(cString: valuePtr)
        guard isXDomain(host), !value.isEmpty else { continue }
        if host.lowercased().contains("x.com") {
          if xCom[name] == nil { xCom[name] = value }
        } else {
          if twitterCom[name] == nil { twitterCom[name] = value }
        }
      }
      return selectSameHostPair(xComCookies: xCom, twitterComCookies: twitterCom)
    }
  }

  // MARK: - Chromium SQLite + Keychain AES-128-CBC reader

  private static func readChromiumCookies(
    at dbURL: URL,
    browserName: String,
    keychainService: String,
    keychainAccount: String,
    cachedAESKey: inout Data?,
    now: Date = Date()
  ) throws -> SessionPair? {
    struct RawRow {
      let hostKey: String
      let name: String
      let plainValue: String
      let encryptedValue: Data
    }

    let (metaVersion, rows): (Int, [RawRow]) = try withCopiedSQLiteDatabase(
      at: dbURL,
      browserName: browserName
    ) { db in
      let metaVersion = readChromiumMetaVersion(db: db)
      let sql = """
        SELECT host_key, name, value, encrypted_value, expires_utc
        FROM cookies
        WHERE (host_key LIKE '%x.com' OR host_key LIKE '%twitter.com')
          AND name IN ('auth_token', 'ct0')
        ORDER BY last_access_utc DESC;
        """
      var statement: OpaquePointer?
      guard sqlite3_prepare_v2(db, sql, -1, &statement, nil) == SQLITE_OK else {
        throw BrowserXImportError.databaseReadFailed(browserName: browserName)
      }
      defer { sqlite3_finalize(statement) }

      // Chromium stores expires_utc as microseconds since 1601-01-01 UTC (0 = session cookie).
      let nowWebKitMicros = Int64((now.timeIntervalSince1970 + 11_644_473_600) * 1_000_000)
      var collected: [RawRow] = []
      while true {
        let rc = sqlite3_step(statement)
        if rc == SQLITE_DONE { break }
        guard rc == SQLITE_ROW else {
          throw BrowserXImportError.databaseReadFailed(browserName: browserName)
        }
        guard let hostPtr = sqlite3_column_text(statement, 0),
          let namePtr = sqlite3_column_text(statement, 1)
        else {
          continue
        }
        let expiresUTC = sqlite3_column_int64(statement, 4)
        if expiresUTC > 0 && expiresUTC < nowWebKitMicros { continue }

        let hostKey = String(cString: hostPtr)
        guard isXDomain(hostKey) else { continue }
        let name = String(cString: namePtr)
        let plainValue = sqlite3_column_text(statement, 2).map { String(cString: $0) } ?? ""
        let blobBytes = sqlite3_column_bytes(statement, 3)
        let blobData: Data
        if blobBytes > 0, let blobPtr = sqlite3_column_blob(statement, 3) {
          blobData = Data(bytes: blobPtr, count: Int(blobBytes))
        } else {
          blobData = Data()
        }
        collected.append(
          RawRow(hostKey: hostKey, name: name, plainValue: plainValue, encryptedValue: blobData)
        )
      }
      return (metaVersion, collected)
    }

    guard !rows.isEmpty else { return nil }

    var xCom: [String: String] = [:]
    var twitterCom: [String: String] = [:]
    var sawDecryptionFailure = false

    for row in rows {
      var decoded: String?
      if !row.plainValue.isEmpty {
        decoded = row.plainValue
      } else if !row.encryptedValue.isEmpty {
        if cachedAESKey == nil {
          let password = try readChromiumSafeStoragePassword(
            service: keychainService,
            account: keychainAccount,
            browserName: browserName
          )
          cachedAESKey = deriveChromiumAESKey(password: password)
        }
        guard let key = cachedAESKey else {
          throw BrowserXImportError.decryptionFailed(browserName: browserName)
        }
        if let value = decryptChromiumCookieValue(
          row.encryptedValue,
          hostKey: row.hostKey,
          requiresHostDigestPrefix: metaVersion >= 24,
          aesKey: key
        ) {
          decoded = value
        } else {
          sawDecryptionFailure = true
        }
      }
      if let decoded, !decoded.isEmpty {
        if row.hostKey.lowercased().contains("x.com") {
          if xCom[row.name] == nil { xCom[row.name] = decoded }
        } else {
          if twitterCom[row.name] == nil { twitterCom[row.name] = decoded }
        }
      }
    }

    if let pair = selectSameHostPair(xComCookies: xCom, twitterComCookies: twitterCom) {
      return pair
    }
    if sawDecryptionFailure {
      throw BrowserXImportError.decryptionFailed(browserName: browserName)
    }
    return nil
  }

  private static func readChromiumMetaVersion(db: OpaquePointer) -> Int {
    let sql = "SELECT value FROM meta WHERE key = 'version' LIMIT 1;"
    var statement: OpaquePointer?
    guard sqlite3_prepare_v2(db, sql, -1, &statement, nil) == SQLITE_OK else { return 0 }
    defer { sqlite3_finalize(statement) }
    guard sqlite3_step(statement) == SQLITE_ROW,
      let ptr = sqlite3_column_text(statement, 0),
      let version = Int(String(cString: ptr))
    else {
      return 0
    }
    return version
  }

  private static func readChromiumSafeStoragePassword(
    service: String,
    account: String,
    browserName: String
  ) throws -> Data {
    let query: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: service,
      kSecAttrAccount as String: account,
      kSecReturnData as String: true,
      kSecMatchLimit as String: kSecMatchLimitOne,
    ]
    var item: CFTypeRef?
    let status = SecItemCopyMatching(query as CFDictionary, &item)
    switch status {
    case errSecSuccess:
      guard let data = item as? Data, !data.isEmpty else {
        throw BrowserXImportError.keychainUnavailable(browserName: browserName)
      }
      return data
    case errSecUserCanceled, errSecAuthFailed, errSecInteractionNotAllowed:
      throw BrowserXImportError.keychainCancelled(browserName: browserName)
    default:
      throw BrowserXImportError.keychainUnavailable(browserName: browserName)
    }
  }

  static func deriveChromiumAESKey(password: Data) -> Data? {
    let salt = Data("saltysalt".utf8)
    var derivedKey = Data(count: 16)
    let result = derivedKey.withUnsafeMutableBytes { derivedBytes in
      password.withUnsafeBytes { passwordBytes in
        salt.withUnsafeBytes { saltBytes in
          CCKeyDerivationPBKDF(
            CCPBKDFAlgorithm(kCCPBKDF2),
            passwordBytes.baseAddress?.assumingMemoryBound(to: Int8.self),
            password.count,
            saltBytes.baseAddress?.assumingMemoryBound(to: UInt8.self),
            salt.count,
            CCPseudoRandomAlgorithm(kCCPRFHmacAlgSHA1),
            1_003,
            derivedBytes.baseAddress?.assumingMemoryBound(to: UInt8.self),
            16
          )
        }
      }
    }
    return result == kCCSuccess ? derivedKey : nil
  }

  /// Decrypts a Chromium `v10` cookie value and fails CLOSED on Chrome 130+
  /// (meta version >= 24) SHA-256 host-key integrity mismatch.
  static func decryptChromiumCookieValue(
    _ encrypted: Data,
    hostKey: String,
    requiresHostDigestPrefix: Bool,
    aesKey: Data
  ) -> String? {
    let prefix = Data("v10".utf8)
    guard encrypted.count > 3, encrypted.prefix(3) == prefix else { return nil }
    let ciphertext = encrypted.dropFirst(3)
    let iv = Data(repeating: 0x20, count: 16)

    var outBuffer = Data(count: ciphertext.count + kCCBlockSizeAES128)
    var numBytesDecrypted: size_t = 0
    let outLength = outBuffer.count

    let status = outBuffer.withUnsafeMutableBytes { outBytes in
      ciphertext.withUnsafeBytes { inBytes in
        aesKey.withUnsafeBytes { keyBytes in
          iv.withUnsafeBytes { ivBytes in
            CCCrypt(
              CCOperation(kCCDecrypt),
              CCAlgorithm(kCCAlgorithmAES128),
              CCOptions(kCCOptionPKCS7Padding),
              keyBytes.baseAddress,
              aesKey.count,
              ivBytes.baseAddress,
              inBytes.baseAddress,
              ciphertext.count,
              outBytes.baseAddress,
              outLength,
              &numBytesDecrypted
            )
          }
        }
      }
    }
    guard status == kCCSuccess, numBytesDecrypted > 0 else { return nil }
    let plainData = outBuffer.prefix(numBytesDecrypted)

    if requiresHostDigestPrefix {
      guard plainData.count > 32 else { return nil }
      let hostDigest = Data(SHA256.hash(data: Data(hostKey.utf8)))
      guard plainData.prefix(32) == hostDigest else { return nil }
      return String(data: plainData.dropFirst(32), encoding: .utf8)?
        .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    if plainData.count > 32 {
      let hostDigest = Data(SHA256.hash(data: Data(hostKey.utf8)))
      if plainData.prefix(32) == hostDigest {
        return String(data: plainData.dropFirst(32), encoding: .utf8)?
          .trimmingCharacters(in: .whitespacesAndNewlines)
      }
    }
    return String(data: plainData, encoding: .utf8)?
      .trimmingCharacters(in: .whitespacesAndNewlines)
  }

  // MARK: - Helpers

  private static func classifyReadError(_ error: Error, browserName: String) -> Error {
    let nsError = error as NSError
    if nsError.domain == NSCocoaErrorDomain && nsError.code == NSFileReadNoPermissionError {
      return BrowserXImportError.fullDiskAccessRequired(browserName: browserName)
    }
    if nsError.domain == NSPOSIXErrorDomain && (nsError.code == EPERM || nsError.code == EACCES) {
      return BrowserXImportError.fullDiskAccessRequired(browserName: browserName)
    }
    return BrowserXImportError.databaseReadFailed(browserName: browserName)
  }

  private static func withCopiedSQLiteDatabase<T>(
    at sourceURL: URL,
    browserName: String,
    body: (OpaquePointer) throws -> T
  ) throws -> T {
    let fm = FileManager.default
    guard fm.isReadableFile(atPath: sourceURL.path) else {
      throw BrowserXImportError.fullDiskAccessRequired(browserName: browserName)
    }
    let tempDir = fm.temporaryDirectory.appendingPathComponent(
      "\(tempDirectoryPrefix)\(UUID().uuidString)",
      isDirectory: true
    )
    try fm.createDirectory(
      at: tempDir,
      withIntermediateDirectories: true,
      attributes: [.posixPermissions: NSNumber(value: Int16(0o700))]
    )
    defer { try? fm.removeItem(at: tempDir) }

    let destDB = tempDir.appendingPathComponent("Cookies.sqlite")
    do {
      try fm.copyItem(at: sourceURL, to: destDB)
      for suffix in ["-wal", "-shm"] {
        let sidecar = URL(fileURLWithPath: sourceURL.path + suffix)
        if fm.fileExists(atPath: sidecar.path) {
          try? fm.copyItem(at: sidecar, to: URL(fileURLWithPath: destDB.path + suffix))
        }
      }
    } catch {
      throw classifyReadError(error, browserName: browserName)
    }

    var db: OpaquePointer?
    guard sqlite3_open_v2(destDB.path, &db, SQLITE_OPEN_READONLY, nil) == SQLITE_OK, let db else {
      throw BrowserXImportError.databaseReadFailed(browserName: browserName)
    }
    defer { sqlite3_close(db) }
    return try body(db)
  }

  private static func allChromiumCookieDatabases(in baseDir: URL) -> [URL] {
    let fm = FileManager.default
    var candidates: [URL] = []
    if let entries = try? fm.contentsOfDirectory(
      at: baseDir,
      includingPropertiesForKeys: [.isDirectoryKey],
      options: [.skipsHiddenFiles]
    ) {
      for entry in entries {
        let name = entry.lastPathComponent
        guard name == "Default" || name.hasPrefix("Profile ") else { continue }
        let direct = entry.appendingPathComponent("Cookies")
        if fm.fileExists(atPath: direct.path) {
          candidates.append(direct)
        }
      }
    }
    if candidates.isEmpty {
      for profile in ["Default", "Profile 1", "Profile 2", "Profile 3", "Profile 4", "Profile 5"] {
        let direct = baseDir.appendingPathComponent(profile).appendingPathComponent("Cookies")
        if fm.fileExists(atPath: direct.path) {
          candidates.append(direct)
        }
      }
    }
    return candidates.sorted { modificationDate(of: $0) > modificationDate(of: $1) }
  }

  private static func allFiles(in dir: URL, fileName: String) -> [URL] {
    let fm = FileManager.default
    guard let entries = try? fm.contentsOfDirectory(
      at: dir,
      includingPropertiesForKeys: [.isDirectoryKey],
      options: [.skipsHiddenFiles]
    ) else {
      return []
    }
    return entries
      .map { $0.appendingPathComponent(fileName) }
      .filter { fm.fileExists(atPath: $0.path) }
      .sorted { modificationDate(of: $0) > modificationDate(of: $1) }
  }

  private static func modificationDate(of url: URL) -> Date {
    (try? url.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate)
      ?? .distantPast
  }

  private static func readUInt32BE(_ data: Data, at offset: Int) -> UInt32 {
    (UInt32(data[offset]) << 24)
      | (UInt32(data[offset + 1]) << 16)
      | (UInt32(data[offset + 2]) << 8)
      | UInt32(data[offset + 3])
  }

  private static func readUInt32LE(_ data: Data, at offset: Int) -> UInt32 {
    UInt32(data[offset])
      | (UInt32(data[offset + 1]) << 8)
      | (UInt32(data[offset + 2]) << 16)
      | (UInt32(data[offset + 3]) << 24)
  }

  private static func readDoubleLE(_ data: Data, at offset: Int) -> Double {
    var raw: UInt64 = 0
    for idx in 0..<8 {
      raw |= UInt64(data[offset + idx]) << (UInt64(idx) * 8)
    }
    return Double(bitPattern: raw)
  }

  private static func readCString(_ data: Data, from start: Int, limit: Int) -> String? {
    guard start >= 0, start < limit, limit <= data.count else { return nil }
    var end = start
    while end < limit && data[end] != 0 {
      end += 1
    }
    return String(data: data.subdata(in: start..<end), encoding: .utf8)
  }
}
