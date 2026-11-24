import Foundation

/// Routes TrawlClient calls to whichever archive backend is currently active
/// (this Mac's bundled `trawl` helper or a read-only remote archive server).
public final class SwitchableTrawlClient: TrawlClient, @unchecked Sendable {
  private let lock = NSLock()
  private var activeClient: any TrawlClient

  public init(initialClient: any TrawlClient) {
    self.activeClient = initialClient
  }

  public func replaceActiveClient(_ client: any TrawlClient) {
    lock.withLock {
      activeClient = client
    }
  }

  private var currentClient: any TrawlClient {
    lock.withLock { activeClient }
  }

  public func status() async throws -> FederatedTrawlerStatusOperation {
    try await currentClient.status()
  }

  public func update(
    registeredTrawlers: [RegisteredTrawlerIdentity],
    progress: @escaping @Sendable (TrawlerArchiveUpdateProgress) -> Void
  ) async throws -> FederatedTrawlerArchiveUpdateOperation {
    try await currentClient.update(
      registeredTrawlers: registeredTrawlers,
      progress: progress
    )
  }

  public func search(
    _ request: TrawlArchiveSearchRequest
  ) async throws -> FederatedTrawlerSearchOperation {
    try await currentClient.search(request)
  }

  public func open(
    link: GloballyRoutableTrawlLink,
    anchor: RecordAnchorIdentifier
  ) async throws -> OpenResponse {
    try await currentClient.open(link: link, anchor: anchor)
  }

  public func photoImageData(for link: GloballyRoutableTrawlLink) async throws -> Data? {
    try await currentClient.photoImageData(for: link)
  }
}
