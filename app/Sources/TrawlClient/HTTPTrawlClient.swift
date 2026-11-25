import Foundation
import SwiftProtobuf

public struct RemoteArchiveEndpointConfiguration: Sendable, Equatable {
  public static let remoteURLEnvironmentKey = "OPENTRAWL_REMOTE_URL"
  public static let accessTokenEnvironmentKey = "OPENTRAWL_ACCESS_TOKEN"

  public let serverBaseURL: URL
  public let bearerToken: String

  public init(serverBaseURL: URL, bearerToken: String) {
    self.serverBaseURL = serverBaseURL
    self.bearerToken = bearerToken.trimmingCharacters(in: .whitespacesAndNewlines)
  }

  public var displayHostName: String {
    guard let host = serverBaseURL.host, !host.isEmpty else {
      return serverBaseURL.absoluteString
    }
    return host
  }

  /// The first DNS label of a host name, which is how a person names a Mac
  /// ("mac-mini" for "mac-mini.example.ts.net"). An IP address stays whole.
  public static func shortDisplayHostName(for serverBaseURL: URL) -> String {
    guard let host = serverBaseURL.host, !host.isEmpty else {
      return serverBaseURL.absoluteString
    }
    let isIPAddress =
      host.contains(":") || host.allSatisfy { $0.isNumber || $0 == "." }
    guard !isIPAddress, let firstLabel = host.split(separator: ".").first else {
      return host
    }
    return String(firstLabel)
  }

  /// Stable origin identifier (`scheme://host[:port]`) used to bind saved
  /// Keychain tokens to one exact server so a token is never sent to another host.
  public static func originKey(for serverBaseURL: URL) -> String {
    let scheme = (serverBaseURL.scheme ?? "https").lowercased()
    let host = (serverBaseURL.host ?? "").lowercased()
    if let port = serverBaseURL.port {
      return "\(scheme)://\(host):\(port)"
    }
    return "\(scheme)://\(host)"
  }

  /// True when transport is HTTPS, loopback, or a syntactically valid
  /// Tailscale MagicDNS/CGNAT host. Plain HTTP to a Tailscale host is
  /// additionally verified against active local Tailscale peers by
  /// `HTTPTrawlClient` before sending a bearer token.
  public static func usesEncryptedOrLoopbackTransport(for serverBaseURL: URL) -> Bool {
    if serverBaseURL.scheme?.lowercased() == "https" {
      return true
    }
    guard serverBaseURL.scheme?.lowercased() == "http" else {
      return false
    }
    let host = Self.normalizedHost(for: serverBaseURL)
    return Self.isLoopbackHost(host)
      || Self.isTailscaleMagicDNSHost(host)
      || Self.isTailscaleCGNATIPv4(host)
  }

  static func normalizedHost(for url: URL) -> String {
    let raw = (url.host ?? "").lowercased()
    if raw.hasSuffix(".") {
      return String(raw.dropLast())
    }
    return raw
  }

  static func isLoopbackHost(_ host: String) -> Bool {
    host == "localhost" || host == "127.0.0.1" || host == "::1"
  }

  static func isTailscaleMagicDNSHost(_ host: String) -> Bool {
    guard host.hasSuffix(".ts.net"), host.count > ".ts.net".count else {
      return false
    }
    let prefix = host.dropLast(".ts.net".count)
    return !prefix.isEmpty && !prefix.hasPrefix(".") && !prefix.hasSuffix(".")
  }

  public static func fromEnvironment(
    _ environment: [String: String] = ProcessInfo.processInfo.environment
  ) -> RemoteArchiveEndpointConfiguration? {
    guard
      let rawURL = environment[remoteURLEnvironmentKey],
      let rawToken = environment[accessTokenEnvironmentKey]
    else {
      return nil
    }
    let token = rawToken.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !token.isEmpty, let url = normalizedServerBaseURL(from: rawURL) else {
      return nil
    }
    return RemoteArchiveEndpointConfiguration(serverBaseURL: url, bearerToken: token)
  }

  /// Normalises a pasted server address into a clean base URL. Bare hostnames
  /// (including host:port) default to HTTPS unless the host is loopback or a
  /// Tailscale WireGuard host, and any pasted `/mcp` or `/app/...` suffix is
  /// stripped so copying from `trawl mcp --http` works directly.
  public static func normalizedServerBaseURL(from rawText: String) -> URL? {
    let trimmed = rawText.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !trimmed.isEmpty else { return nil }

    let candidate: String
    if trimmed.contains("://") {
      candidate = trimmed
    } else {
      let lower = trimmed.lowercased()
      let hostPart = lower.split(separator: "/", maxSplits: 1).first.map(String.init) ?? lower
      let bareHost = hostPart.split(separator: ":", maxSplits: 1).first.map(String.init) ?? hostPart
      let prefersHTTP =
        bareHost == "localhost"
        || bareHost == "127.0.0.1"
        || bareHost == "::1"
        || bareHost.hasSuffix(".ts.net")
        || Self.isTailscaleCGNATIPv4(bareHost)
      candidate = (prefersHTTP ? "http://" : "https://") + trimmed
    }

    guard var components = URLComponents(string: candidate) else { return nil }
    guard
      let scheme = components.scheme?.lowercased(),
      scheme == "http" || scheme == "https",
      let host = components.host,
      !host.isEmpty
    else {
      return nil
    }
    components.scheme = scheme
    components.query = nil
    components.fragment = nil

    var path = components.path
    for suffix in ["/mcp", "/app/status", "/app/search", "/app/open", "/app/photo", "/healthz"] {
      if path.hasSuffix(suffix) {
        path = String(path.dropLast(suffix.count))
        break
      }
    }
    while path.hasSuffix("/") {
      path.removeLast()
    }
    components.path = path
    return components.url
  }

  private static func isTailscaleCGNATIPv4(_ host: String) -> Bool {
    let parts = host.split(separator: ".", omittingEmptySubsequences: false)
    guard parts.count == 4 else {
      return false
    }
    var octets: [Int] = []
    octets.reserveCapacity(4)
    for part in parts {
      guard !part.isEmpty, part.count <= 3,
        part.allSatisfy({ $0.isASCII && $0.isNumber }),
        let value = Int(part),
        (0...255).contains(value)
      else {
        return false
      }
      octets.append(value)
    }
    return octets[0] == 100 && (64...127).contains(octets[1])
  }
}

private final class LocalTailscalePeerCache: @unchecked Sendable {
  static let shared = LocalTailscalePeerCache()

  private let lock = NSLock()
  private var cachedHosts: Set<String> = []
  private var cachedAt: Date = .distantPast
  private let ttl: TimeInterval = 30

  func activePeerHosts() -> Set<String> {
    lock.lock()
    if Date().timeIntervalSince(cachedAt) < ttl {
      let current = cachedHosts
      lock.unlock()
      return current
    }
    lock.unlock()

    let resolved = Self.queryLocalTailscaleStatus()
    lock.lock()
    cachedHosts = resolved
    cachedAt = Date()
    lock.unlock()
    return resolved
  }

  private static func queryLocalTailscaleStatus() -> Set<String> {
    let candidates = [
      "/Applications/Tailscale.app/Contents/MacOS/Tailscale",
      "/opt/homebrew/bin/tailscale",
      "/usr/local/bin/tailscale",
      "/usr/bin/tailscale",
    ]
    for executablePath in candidates {
      guard FileManager.default.isExecutableFile(atPath: executablePath) else {
        continue
      }
      let process = Process()
      process.executableURL = URL(fileURLWithPath: executablePath)
      process.arguments = ["status", "--json"]
      let stdoutPipe = Pipe()
      process.standardOutput = stdoutPipe
      process.standardError = FileHandle.nullDevice
      do {
        try process.run()
      } catch {
        continue
      }
      let data = stdoutPipe.fileHandleForReading.readDataToEndOfFile()
      process.waitUntilExit()
      guard process.terminationStatus == 0, !data.isEmpty else {
        continue
      }
      if let hosts = Self.parsePeerHosts(from: data) {
        return hosts
      }
    }
    return []
  }

  static func parsePeerHosts(from jsonData: Data) -> Set<String>? {
    struct NodeStatus: Decodable {
      let DNSName: String?
      let TailscaleIPs: [String]?
    }
    struct TailscaleStatus: Decodable {
      let BackendState: String?
      let SelfNode: NodeStatus?
      let Peer: [String: NodeStatus]?

      enum CodingKeys: String, CodingKey {
        case BackendState
        case SelfNode = "Self"
        case Peer
      }
    }
    guard let status = try? JSONDecoder().decode(TailscaleStatus.self, from: jsonData) else {
      return nil
    }
    if let state = status.BackendState, state != "Running" {
      return []
    }
    var hosts: Set<String> = []
    func collect(_ node: NodeStatus?) {
      guard let node else { return }
      if let dns = node.DNSName?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(),
        !dns.isEmpty
      {
        let normalized = dns.hasSuffix(".") ? String(dns.dropLast()) : dns
        if !normalized.isEmpty {
          hosts.insert(normalized)
        }
      }
      for rawIP in node.TailscaleIPs ?? [] {
        let normalized = rawIP.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if !normalized.isEmpty {
          hosts.insert(normalized)
        }
      }
    }
    collect(status.SelfNode)
    for node in status.Peer?.values ?? Dictionary<String, NodeStatus>().values {
      collect(node)
    }
    return hosts
  }
}

public struct HTTPTrawlClient: TrawlClient {
  static let defaultStatusTimeout: TimeInterval = 15
  static let defaultSearchTimeout: TimeInterval = 15
  static let defaultOpenTimeout: TimeInterval = 20
  static let defaultPhotoTimeout: TimeInterval = 60
  static let maximumPhotoResponseBytes: Int = 32 * 1024 * 1024

  public let configuration: RemoteArchiveEndpointConfiguration
  private let session: URLSession
  private let activeTailscalePeerHostsProvider: @Sendable () -> Set<String>

  public init(
    configuration: RemoteArchiveEndpointConfiguration,
    session: URLSession? = nil
  ) {
    self.init(
      configuration: configuration,
      session: session,
      activeTailscalePeerHostsProvider: { LocalTailscalePeerCache.shared.activePeerHosts() }
    )
  }

  init(
    configuration: RemoteArchiveEndpointConfiguration,
    session: URLSession? = nil,
    activeTailscalePeerHostsProvider: @escaping @Sendable () -> Set<String>
  ) {
    self.configuration = configuration
    self.activeTailscalePeerHostsProvider = activeTailscalePeerHostsProvider
    if let session {
      self.session = session
    } else {
      let urlSessionConfiguration = URLSessionConfiguration.ephemeral
      urlSessionConfiguration.requestCachePolicy = .reloadIgnoringLocalAndRemoteCacheData
      urlSessionConfiguration.urlCache = nil
      urlSessionConfiguration.httpCookieStorage = nil
      urlSessionConfiguration.httpShouldSetCookies = false
      urlSessionConfiguration.timeoutIntervalForRequest = Self.defaultOpenTimeout
      urlSessionConfiguration.timeoutIntervalForResource = Self.defaultPhotoTimeout
      self.session = URLSession(configuration: urlSessionConfiguration)
    }
  }

  public func status() async throws -> FederatedTrawlerStatusOperation {
    let message: Trawl_Federation_FederatedTrawlerStatusOperation = try await fetchProtobuf(
      path: "/app/status",
      queryItems: [],
      timeout: Self.defaultStatusTimeout
    )
    do {
      return try message.decodedFederatedTrawlerStatusOperation()
    } catch {
      throw TrawlClientError.invalidProtobuf
    }
  }

  public func update(
    registeredTrawlers: [RegisteredTrawlerIdentity],
    progress: @escaping @Sendable (TrawlerArchiveUpdateProgress) -> Void
  ) async throws -> FederatedTrawlerArchiveUpdateOperation {
    throw TrawlClientError.remoteArchiveReadOnly
  }

  public func search(
    _ request: TrawlArchiveSearchRequest
  ) async throws -> FederatedTrawlerSearchOperation {
    var queryItems: [URLQueryItem] = []
    if !request.searchQueryText.isEmpty {
      queryItems.append(URLQueryItem(name: "q", value: request.searchQueryText))
    }
    if let registeredTrawler = request.onlySearchThisRegisteredTrawler,
      !registeredTrawler.registeredTrawlerIdentity.isEmpty
    {
      queryItems.append(
        URLQueryItem(name: "trawler", value: registeredTrawler.registeredTrawlerIdentity))
    }
    let fractionalISO = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
    if let earliest = request.earliestMatchingArchiveRecordTimeInclusive {
      queryItems.append(URLQueryItem(name: "after", value: earliest.formatted(fractionalISO)))
    }
    if let latest = request.latestMatchingArchiveRecordTimeInclusive {
      queryItems.append(URLQueryItem(name: "before", value: latest.formatted(fractionalISO)))
    }
    queryItems.append(
      URLQueryItem(name: "limit", value: String(request.maximumReturnedSearchMatchCount)))

    let message: Trawl_Federation_FederatedTrawlerSearchOperation = try await fetchProtobuf(
      path: "/app/search",
      queryItems: queryItems,
      timeout: Self.defaultSearchTimeout
    )
    do {
      return try message.decodedFederatedTrawlerSearchOperation()
    } catch {
      throw TrawlClientError.invalidProtobuf
    }
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
    var queryItems = [URLQueryItem(name: "link", value: link.globallyRoutableTrawlLink)]
    if !anchor.recordAnchorIdentifier.isEmpty {
      queryItems.append(URLQueryItem(name: "anchor", value: anchor.recordAnchorIdentifier))
    }
    let message: Trawl_Open_OpenResponse = try await fetchProtobuf(
      path: "/app/open",
      queryItems: queryItems,
      timeout: Self.defaultOpenTimeout
    )
    let result: OpenResponse
    do {
      result = try message.decodedOpenResponse()
    } catch {
      throw TrawlClientError.invalidProtobuf
    }
    guard result.requestedTrawlLink == link,
      result.requestedRecordAnchor == anchor
    else {
      throw TrawlClientError.invalidProtobuf
    }
    return result
  }

  public func photoImageData(for link: GloballyRoutableTrawlLink) async throws -> Data? {
    guard
      let route = parseGloballyRoutableTrawlLink(link),
      route.registeredTrawler.registeredTrawlerIdentity == "photos"
    else {
      throw TrawlClientError.invalidProtobuf
    }
    let data = try await fetchAuthenticatedGET(
      path: "/app/photo",
      queryItems: [URLQueryItem(name: "link", value: link.globallyRoutableTrawlLink)],
      accept: "image/jpeg, image/*",
      timeout: Self.defaultPhotoTimeout,
      maximumResponseBytes: Self.maximumPhotoResponseBytes,
      unavailableErrorOnMissingOrServiceUnavailable: true
    )
    guard !data.isEmpty else {
      throw TrawlClientError.remotePhotoUnavailable("The remote archive returned an empty photo.")
    }
    return data
  }

  private func fetchProtobuf<Message: SwiftProtobuf.Message>(
    path: String,
    queryItems: [URLQueryItem],
    timeout: TimeInterval
  ) async throws -> Message {
    let data = try await fetchAuthenticatedGET(
      path: path,
      queryItems: queryItems,
      accept: "application/x-protobuf",
      timeout: timeout,
      maximumResponseBytes: DelimitedFrames.maximumFrameBytes,
      unavailableErrorOnMissingOrServiceUnavailable: false
    )
    do {
      return try Message(serializedBytes: data)
    } catch {
      throw TrawlClientError.invalidProtobuf
    }
  }

  private func isVerifiedTransport(for serverBaseURL: URL) -> Bool {
    guard RemoteArchiveEndpointConfiguration.usesEncryptedOrLoopbackTransport(for: serverBaseURL) else {
      return false
    }
    if serverBaseURL.scheme?.lowercased() == "https" {
      return true
    }
    let host = RemoteArchiveEndpointConfiguration.normalizedHost(for: serverBaseURL)
    if RemoteArchiveEndpointConfiguration.isLoopbackHost(host) {
      return true
    }
    // Plain HTTP to a Tailscale MagicDNS name or CGNAT IP is only permitted
    // when the local Tailscale daemon confirms the host is an active peer on
    // this device's WireGuard tailnet.
    let activePeers = activeTailscalePeerHostsProvider()
    return activePeers.contains(host)
  }

  private func fetchAuthenticatedGET(
    path: String,
    queryItems: [URLQueryItem],
    accept: String,
    timeout: TimeInterval,
    maximumResponseBytes: Int,
    unavailableErrorOnMissingOrServiceUnavailable: Bool
  ) async throws -> Data {
    guard !configuration.bearerToken.isEmpty else {
      throw TrawlClientError.remoteAuthenticationRequired
    }
    guard isVerifiedTransport(for: configuration.serverBaseURL) else {
      throw TrawlClientError.remoteInsecureTransportRejected
    }
    guard var components = URLComponents(url: configuration.serverBaseURL, resolvingAgainstBaseURL: false) else {
      throw TrawlClientError.remoteServerURLInvalid
    }
    components.path = components.path + path
    if !queryItems.isEmpty {
      components.queryItems = queryItems
    }
    guard let requestURL = components.url else {
      throw TrawlClientError.remoteServerURLInvalid
    }

    var request = URLRequest(url: requestURL)
    request.httpMethod = "GET"
    request.timeoutInterval = timeout
    request.cachePolicy = .reloadIgnoringLocalAndRemoteCacheData
    request.setValue("Bearer \(configuration.bearerToken)", forHTTPHeaderField: "Authorization")
    request.setValue(accept, forHTTPHeaderField: "Accept")

    let asyncBytes: URLSession.AsyncBytes
    let urlResponse: URLResponse
    do {
      (asyncBytes, urlResponse) = try await session.bytes(for: request)
    } catch is CancellationError {
      throw TrawlClientError.cancelled
    } catch let error as URLError {
      switch error.code {
      case .cancelled:
        throw TrawlClientError.cancelled
      case .timedOut:
        throw TrawlClientError.timedOut
      default:
        throw TrawlClientError.remoteServerUnreachable(error.localizedDescription)
      }
    } catch {
      throw TrawlClientError.remoteServerUnreachable(error.localizedDescription)
    }

    guard let httpResponse = urlResponse as? HTTPURLResponse else {
      throw TrawlClientError.invalidFrame
    }

    let readLimit = httpResponse.statusCode == 200 ? maximumResponseBytes : 64 * 1024
    if httpResponse.expectedContentLength > Int64(readLimit) {
      throw TrawlClientError.oversizedFrame
    }

    var data = Data()
    if httpResponse.expectedContentLength > 0 {
      data.reserveCapacity(min(Int(httpResponse.expectedContentLength), readLimit))
    }
    do {
      for try await byte in asyncBytes {
        if data.count >= readLimit {
          throw TrawlClientError.oversizedFrame
        }
        data.append(byte)
      }
    } catch let error as TrawlClientError {
      throw error
    } catch is CancellationError {
      throw TrawlClientError.cancelled
    } catch let error as URLError {
      switch error.code {
      case .cancelled:
        throw TrawlClientError.cancelled
      case .timedOut:
        throw TrawlClientError.timedOut
      default:
        throw TrawlClientError.remoteServerUnreachable(error.localizedDescription)
      }
    } catch {
      throw TrawlClientError.remoteServerUnreachable(error.localizedDescription)
    }

    switch httpResponse.statusCode {
    case 200:
      break
    case 401:
      throw TrawlClientError.remoteAuthenticationRequired
    case 403:
      throw TrawlClientError.remoteAccessDenied(Self.decodedServerErrorMessage(from: data))
    case 404 where unavailableErrorOnMissingOrServiceUnavailable,
      422 where unavailableErrorOnMissingOrServiceUnavailable,
      503 where unavailableErrorOnMissingOrServiceUnavailable:
      throw TrawlClientError.remotePhotoUnavailable(Self.decodedServerErrorMessage(from: data))
    case 400, 404, 405:
      throw TrawlClientError.remoteRequestRejected(Self.decodedServerErrorMessage(from: data))
    default:
      let detail = Self.decodedServerErrorMessage(from: data)
      let message = detail.isEmpty ? "HTTP \(httpResponse.statusCode)" : detail
      throw TrawlClientError.remoteServerUnreachable(message)
    }

    return data
  }

  private static func decodedServerErrorMessage(from data: Data) -> String {
    struct ServerErrorPayload: Decodable {
      let error: String?
    }
    if let parsed = try? JSONDecoder().decode(ServerErrorPayload.self, from: data),
      let message = parsed.error?.trimmingCharacters(in: .whitespacesAndNewlines),
      !message.isEmpty
    {
      return message
    }
    return ""
  }
}
