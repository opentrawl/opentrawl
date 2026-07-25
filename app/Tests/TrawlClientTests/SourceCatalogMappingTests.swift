import Testing

@testable import TrawlClient

@Test func statusMapsAvailableAndComingSoonCatalogEntries() throws {
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .complete
  response.catalog = [
    catalogEntry(
      sourceID: "messages", displayName: "Messages", releaseState: .available, enabled: true,
      bundleIdentifier: "com.apple.MobileSMS"),
    catalogEntry(
      sourceID: "gmail", displayName: "Gmail", releaseState: .comingSoon, enabled: false,
      artworkBundleIdentifier: "com.google.Gmail"),
  ]

  let catalog = try response.model().catalog

  #expect(catalog.count == 2)
  #expect(catalog[0].manifest.sourceID == "messages")
  #expect(catalog[0].releaseState == .available)
  #expect(catalog[0].enabled)
  #expect(catalog[0].manifest.branding?.bundleIdentifier == "com.apple.MobileSMS")
  #expect(catalog[1].manifest.sourceID == "gmail")
  #expect(catalog[1].releaseState == .comingSoon)
  #expect(!catalog[1].enabled)
  #expect(catalog[1].manifest.branding?.artworkBundleIdentifier == "com.google.Gmail")
}

@Test func statusPreservesEnabledComingSoonCatalogEntryForLocalTesting() throws {
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .complete
  response.catalog = [
    catalogEntry(
      sourceID: "gmail", displayName: "Gmail", releaseState: .comingSoon, enabled: true,
      artworkBundleIdentifier: "com.google.Gmail")
  ]

  let entry = try #require(response.model().catalog.first)
  #expect(entry.releaseState == .comingSoon)
  #expect(entry.enabled)
}

@Test func statusRejectsCatalogEntryWithoutManifest() {
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .complete
  response.catalog = [.with { $0.releaseState = .available }]

  #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
}

@Test func statusRejectsUnspecifiedAndUnrecognisedCatalogReleaseState() {
  for releaseState in [
    Trawl_Federation_V1_SourceReleaseState.unspecified,
    Trawl_Federation_V1_SourceReleaseState.UNRECOGNIZED(99),
  ] {
    var response = Trawl_Federation_V1_StatusResponse()
    response.outcome = .complete
    response.catalog = [
      catalogEntry(
        sourceID: "messages", displayName: "Messages", releaseState: releaseState,
        enabled: true, bundleIdentifier: "com.apple.MobileSMS")
    ]

    #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
  }
}

private func catalogEntry(
  sourceID: String, displayName: String,
  releaseState: Trawl_Federation_V1_SourceReleaseState, enabled: Bool,
  bundleIdentifier: String = "", artworkBundleIdentifier: String = ""
) -> Trawl_Federation_V1_SourceCatalogEntry {
  .with {
    $0.manifest = .with {
      $0.sourceID = sourceID
      $0.displayName = displayName
      $0.branding = .with {
        $0.symbolName = "app.fill"
        $0.accentColor = "#112233"
        $0.bundleIdentifier = bundleIdentifier
        $0.artworkBundleIdentifier = artworkBundleIdentifier
      }
    }
    $0.releaseState = releaseState
    $0.enabled = enabled
  }
}
