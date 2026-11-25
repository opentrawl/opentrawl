import Foundation
import Testing

@testable import TrawlClient

struct HTTPTrawlClientTests {
  @Test func normalizesRemoteArchiveAddressesAcrossTailscaleLANAndHTTPS() {
    #expect(
      RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(
        from: "archive-mac.tailnet.ts.net:7391"
      )?.absoluteString == "http://archive-mac.tailnet.ts.net:7391"
    )
    #expect(
      RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(
        from: "http://archive-mac.tailnet.ts.net:7391/mcp"
      )?.absoluteString == "http://archive-mac.tailnet.ts.net:7391"
    )
    #expect(
      RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(
        from: "archive-mac.local:7391/"
      )?.absoluteString == "https://archive-mac.local:7391"
    )
    #expect(
      RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(
        from: "http://archive-mac.local:7391/"
      )?.absoluteString == "http://archive-mac.local:7391"
    )
    #expect(
      RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(
        from: "archive.example.com:8443"
      )?.absoluteString == "https://archive.example.com:8443"
    )
    #expect(
      RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(
        from: "https://archive.example.com/opentrawl/mcp"
      )?.absoluteString == "https://archive.example.com/opentrawl"
    )
    #expect(
      RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(
        from: "http://archive-mac.tailnet.ts.net:7391/app/photo"
      )?.absoluteString == "http://archive-mac.tailnet.ts.net:7391"
    )
    #expect(
      RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(from: "ftp://example.com") == nil
    )
    #expect(
      RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(from: "   ") == nil
    )

    let tsURL = URL(string: "http://archive-mac.tailnet.ts.net:7391")!
    let lanHTTPURL = URL(string: "http://archive-mac.local:7391")!
    let spoofedCGNATURL = URL(string: "http://evil.100.64.1.2.example.com:7391")!
    let bareSuffixURL = URL(string: "http://.ts.net:7391")!
    let httpsURL = URL(string: "https://archive.example.com:8443/opentrawl")!
    #expect(RemoteArchiveEndpointConfiguration.usesEncryptedOrLoopbackTransport(for: tsURL))
    #expect(!RemoteArchiveEndpointConfiguration.usesEncryptedOrLoopbackTransport(for: lanHTTPURL))
    #expect(!RemoteArchiveEndpointConfiguration.usesEncryptedOrLoopbackTransport(for: spoofedCGNATURL))
    #expect(!RemoteArchiveEndpointConfiguration.usesEncryptedOrLoopbackTransport(for: bareSuffixURL))
    #expect(RemoteArchiveEndpointConfiguration.usesEncryptedOrLoopbackTransport(for: httpsURL))
    #expect(
      RemoteArchiveEndpointConfiguration.originKey(for: httpsURL)
        == "https://archive.example.com:8443"
    )
  }

  @Test func remoteClientRejectsPlainHTTPToUnverifiedTailscaleHost() async {
    let config = RemoteArchiveEndpointConfiguration(
      serverBaseURL: URL(string: "http://unverified.tailnet.ts.net:7391")!,
      bearerToken: "otk_test"
    )
    let client = HTTPTrawlClient(
      configuration: config,
      activeTailscalePeerHostsProvider: { ["archive-mac.tailnet.ts.net", "100.64.0.1"] }
    )
    do {
      _ = try await client.status()
      Issue.record("expected status() to unverified .ts.net host over plain HTTP to fail")
    } catch let error as TrawlClientError {
      #expect(error == .remoteInsecureTransportRejected)
    } catch {
      Issue.record("unexpected error: \(error)")
    }
  }

  @Test func remoteClientRejectsLocalUpdateAsReadOnly() async {
    let config = RemoteArchiveEndpointConfiguration(
      serverBaseURL: URL(string: "http://127.0.0.1:7391")!,
      bearerToken: "otk_test"
    )
    let client = HTTPTrawlClient(configuration: config)
    do {
      _ = try await client.update(registeredTrawlers: []) { _ in }
      Issue.record("expected update() on HTTPTrawlClient to fail with remoteArchiveReadOnly")
    } catch let error as TrawlClientError {
      #expect(error == .remoteArchiveReadOnly)
    } catch {
      Issue.record("unexpected error: \(error)")
    }
  }

  @Test func remoteClientRejectsPlainHTTPOutsideTailscaleAndLoopback() async {
    let config = RemoteArchiveEndpointConfiguration(
      serverBaseURL: URL(string: "http://archive-mac.local:7391")!,
      bearerToken: "otk_test"
    )
    let client = HTTPTrawlClient(configuration: config)
    do {
      _ = try await client.status()
      Issue.record("expected status() over plain LAN HTTP to fail")
    } catch let error as TrawlClientError {
      #expect(error == .remoteInsecureTransportRejected)
    } catch {
      Issue.record("unexpected error: \(error)")
    }
  }

  @Test func remotePhotoFetchSendsBearerTokenAndSurfacesServerErrors() async throws {
    let config = RemoteArchiveEndpointConfiguration(
      serverBaseURL: URL(string: "http://127.0.0.1:7391")!,
      bearerToken: "otk_photo_test"
    )
    let sessionConfiguration = URLSessionConfiguration.ephemeral
    sessionConfiguration.protocolClasses = [StubPhotoURLProtocol.self]
    let client = HTTPTrawlClient(
      configuration: config,
      session: URLSession(configuration: sessionConfiguration)
    )

    let foundLink = GloballyRoutableTrawlLink(globallyRoutableTrawlLink: "photos:abc234")
    let data = try await client.photoImageData(for: foundLink)
    #expect(data == Data([0xFF, 0xD8, 0xFF, 0xE0]))

    let permissionDeniedLink = GloballyRoutableTrawlLink(globallyRoutableTrawlLink: "photos:perm53")
    do {
      _ = try await client.photoImageData(for: permissionDeniedLink)
      Issue.record("expected 503 photo fetch failure to throw remotePhotoUnavailable")
    } catch let error as TrawlClientError {
      #expect(
        error == .remotePhotoUnavailable("OpenTrawl does not have full access to Apple Photos.")
      )
      #expect(error.localizedDescription == "OpenTrawl does not have full access to Apple Photos.")
    }

    let missingLink = GloballyRoutableTrawlLink(globallyRoutableTrawlLink: "photos:mnprst")
    do {
      _ = try await client.photoImageData(for: missingLink)
      Issue.record("expected 404 photo fetch failure to surface server error message")
    } catch let error as TrawlClientError {
      #expect(error == .remotePhotoUnavailable("Apple Photos has no photo with this UUID"))
    }

    let nonPhotoLink = GloballyRoutableTrawlLink(globallyRoutableTrawlLink: "gmail:abc234")
    do {
      _ = try await client.photoImageData(for: nonPhotoLink)
      Issue.record("expected non-photo link to be rejected")
    } catch let error as TrawlClientError {
      #expect(error == .invalidProtobuf)
    }
  }
}

private final class StubPhotoURLProtocol: URLProtocol, @unchecked Sendable {
  override class func canInit(with request: URLRequest) -> Bool { true }
  override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

  override func startLoading() {
    guard
      let url = request.url,
      url.path == "/app/photo",
      request.value(forHTTPHeaderField: "Authorization") == "Bearer otk_photo_test"
    else {
      let response = HTTPURLResponse(
        url: request.url ?? URL(string: "http://127.0.0.1:7391")!,
        statusCode: 401,
        httpVersion: nil,
        headerFields: nil
      )!
      client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
      client?.urlProtocolDidFinishLoading(self)
      return
    }
    let components = URLComponents(url: url, resolvingAgainstBaseURL: false)
    let link = components?.queryItems?.first(where: { $0.name == "link" })?.value
    let statusCode: Int
    let contentType: String
    let body: Data
    switch link {
    case "photos:abc234":
      statusCode = 200
      contentType = "image/jpeg"
      body = Data([0xFF, 0xD8, 0xFF, 0xE0])
    case "photos:perm53":
      statusCode = 503
      contentType = "application/json"
      body = Data(#"{"error":"OpenTrawl does not have full access to Apple Photos."}"#.utf8)
    default:
      statusCode = 404
      contentType = "application/json"
      body = Data(#"{"error":"Apple Photos has no photo with this UUID"}"#.utf8)
    }
    let response = HTTPURLResponse(
      url: url,
      statusCode: statusCode,
      httpVersion: nil,
      headerFields: ["Content-Type": contentType]
    )!
    client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
    client?.urlProtocol(self, didLoad: body)
    client?.urlProtocolDidFinishLoading(self)
  }

  override func stopLoading() {}
}
