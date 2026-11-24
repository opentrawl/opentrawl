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
      RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(from: "ftp://example.com") == nil
    )
    #expect(
      RemoteArchiveEndpointConfiguration.normalizedServerBaseURL(from: "   ") == nil
    )

    let tsURL = URL(string: "http://archive-mac.tailnet.ts.net:7391")!
    let lanHTTPURL = URL(string: "http://archive-mac.local:7391")!
    let httpsURL = URL(string: "https://archive.example.com:8443/opentrawl")!
    #expect(RemoteArchiveEndpointConfiguration.usesEncryptedOrLoopbackTransport(for: tsURL))
    #expect(!RemoteArchiveEndpointConfiguration.usesEncryptedOrLoopbackTransport(for: lanHTTPURL))
    #expect(RemoteArchiveEndpointConfiguration.usesEncryptedOrLoopbackTransport(for: httpsURL))
    #expect(
      RemoteArchiveEndpointConfiguration.originKey(for: httpsURL)
        == "https://archive.example.com:8443"
    )
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
}
