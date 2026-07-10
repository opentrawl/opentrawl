@preconcurrency import Foundation

public actor AppStoreArtwork {
  public typealias FetchData = @Sendable (URL, Int) async throws -> Data

  public static let bundleIDs = [
    "gmail": "com.google.Gmail",
    "twitter": "com.atebits.Tweetie2",
  ]

  private static let maximumLookupBytes = 1024 * 1024
  private static let maximumArtworkBytes = 5 * 1024 * 1024

  private let cacheDirectory: URL
  private let fetchData: FetchData

  public init(
    cacheDirectory: URL? = nil,
    fetchData: @escaping FetchData = AppStoreArtwork.download
  ) {
    self.cacheDirectory = cacheDirectory ?? Self.defaultCacheDirectory
    self.fetchData = fetchData
  }

  public static func lookupURL(for sourceID: String) -> URL? {
    guard let bundleID = bundleIDs[sourceID] else { return nil }
    var components = URLComponents(string: "https://itunes.apple.com/lookup")
    components?.queryItems = [
      URLQueryItem(name: "bundleId", value: bundleID),
      URLQueryItem(name: "entity", value: "software"),
    ]
    return components?.url
  }

  public func data(for sourceID: String) async -> Data? {
    guard let lookupURL = Self.lookupURL(for: sourceID) else { return nil }
    let cachedURL = cacheDirectory.appendingPathComponent("\(sourceID).artwork")
    if let data = try? Data(contentsOf: cachedURL),
      !data.isEmpty,
      data.count <= Self.maximumArtworkBytes
    {
      return data
    }

    do {
      let lookupData = try await fetchData(lookupURL, Self.maximumLookupBytes)
      guard lookupData.count <= Self.maximumLookupBytes else { return nil }
      let response = try JSONDecoder().decode(LookupResponse.self, from: lookupData)
      guard let artworkURL = response.results.first?.artworkURL,
        let url = URL(string: artworkURL),
        url.scheme == "https",
        let host = url.host?.lowercased(),
        host == "mzstatic.com" || host.hasSuffix(".mzstatic.com")
      else {
        return nil
      }
      let artwork = try await fetchData(url, Self.maximumArtworkBytes)
      guard !artwork.isEmpty, artwork.count <= Self.maximumArtworkBytes else {
        return nil
      }
      try FileManager.default.createDirectory(
        at: cacheDirectory,
        withIntermediateDirectories: true
      )
      try artwork.write(to: cachedURL, options: .atomic)
      return artwork
    } catch {
      return nil
    }
  }

  public static func download(_ url: URL, maximumBytes: Int) async throws -> Data {
    let (bytes, response) = try await URLSession.shared.bytes(from: url)
    guard let response = response as? HTTPURLResponse,
      (200..<300).contains(response.statusCode)
    else {
      throw URLError(.badServerResponse)
    }

    var data = Data()
    data.reserveCapacity(min(maximumBytes, 64 * 1024))
    for try await byte in bytes {
      guard data.count < maximumBytes else {
        throw URLError(.dataLengthExceedsMaximum)
      }
      data.append(byte)
    }
    return data
  }

  private static var defaultCacheDirectory: URL {
    FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
      .appendingPathComponent("org.opentrawl.trawl/AppStoreArtwork", isDirectory: true)
  }
}

private struct LookupResponse: Decodable {
  let results: [LookupResult]
}

private struct LookupResult: Decodable {
  let artworkURL: String?

  private enum CodingKeys: String, CodingKey {
    case artworkURL = "artworkUrl512"
  }
}
