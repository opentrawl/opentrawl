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

  /// True when transport is encrypted (HTTPS or Tailscale WireGuard) or stays
  /// on loopback. False for plain HTTP on a local LAN or external network.
  public static func usesEncryptedOrLoopbackTransport(for serverBaseURL: URL) -> Bool {
    if serverBaseURL.scheme?.lowercased() == "https" {
      return true
    }
    let host = (serverBaseURL.host ?? "").lowercased()
    return host == "localhost"
      || host == "127.0.0.1"
      || host == "::1"
      || host.hasSuffix(".ts.net")
      || Self.isTailscaleCGNATIPv4(host)
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
    for suffix in ["/mcp", "/app/status", "/app/search", "/app/open", "/healthz"] {
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
    let octets = host.split(separator: ".").compactMap { Int($0) }
    guard octets.count == 4, octets.allSatisfy({ (0...255).contains($0) }) else {
      return false
    }
    return octets[0] == 100 && (64...127).contains(octets[1])
  }
}

public struct HTTPTrawlClient: TrawlClient {
  static let defaultStatusTimeout: TimeInterval = 15
  static let defaultSearchTimeout: TimeInterval = 15
  static let defaultOpenTimeout: TimeInterval = 20

  public let configuration: RemoteArchiveEndpointConfiguration
  private let session: URLSession

  public init(
    configuration: RemoteArchiveEndpointConfiguration,
    session: URLSession? = nil
  ) {
    self.configuration = configuration
    if let session {
      self.session = session
    } else {
      let urlSessionConfiguration = URLSessionConfiguration.ephemeral
      urlSessionConfiguration.requestCachePolicy = .reloadIgnoringLocalAndRemoteCacheData
      urlSessionConfiguration.urlCache = nil
      urlSessionConfiguration.httpCookieStorage = nil
      urlSessionConfiguration.httpShouldSetCookies = false
      urlSessionConfiguration.timeoutIntervalForRequest = Self.defaultOpenTimeout
      urlSessionConfiguration.timeoutIntervalForResource = Self.defaultOpenTimeout
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

  private func fetchProtobuf<Message: SwiftProtobuf.Message>(
    path: String,
    queryItems: [URLQueryItem],
    timeout: TimeInterval
  ) async throws -> Message {
    guard !configuration.bearerToken.isEmpty else {
      throw TrawlClientError.remoteAuthenticationRequired
    }
    guard RemoteArchiveEndpointConfiguration.usesEncryptedOrLoopbackTransport(
      for: configuration.serverBaseURL)
    else {
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
    request.setValue("application/x-protobuf", forHTTPHeaderField: "Accept")

    let data: Data
    let urlResponse: URLResponse
    do {
      (data, urlResponse) = try await session.data(for: request)
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
    switch httpResponse.statusCode {
    case 200:
      break
    case 401:
      throw TrawlClientError.remoteAuthenticationRequired
    case 403:
      throw TrawlClientError.remoteAccessDenied(Self.decodedServerErrorMessage(from: data))
    case 400, 404, 405:
      throw TrawlClientError.remoteRequestRejected(Self.decodedServerErrorMessage(from: data))
    default:
      let detail = Self.decodedServerErrorMessage(from: data)
      let message = detail.isEmpty ? "HTTP \(httpResponse.statusCode)" : detail
      throw TrawlClientError.remoteServerUnreachable(message)
    }

    guard data.count <= DelimitedFrames.maximumFrameBytes else {
      throw TrawlClientError.oversizedFrame
    }
    do {
      return try Message(serializedBytes: data)
    } catch {
      throw TrawlClientError.invalidProtobuf
    }
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
