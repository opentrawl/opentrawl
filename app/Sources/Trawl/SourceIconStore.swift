import AppKit
import Observation
import SwiftUI
import TrawlClient
import TrawlCore

@MainActor
@Observable
final class SourceIconStore {
  private let artwork = AppStoreArtwork()
  private var images: [String: NSImage] = [:]
  private var loading: Set<String> = []
  private var branding: [String: Branding] = [:]

  func image(for sourceID: String) -> NSImage {
    images[sourceID] ?? placeholder(for: sourceID)
  }

  func update(manifests: [SourceManifest]) {
    branding = Dictionary(
      uniqueKeysWithValues: manifests.compactMap { manifest in
        guard let branding = manifest.branding else { return nil }
        return (manifest.sourceID, branding)
      })
  }

  func update(catalog: [SourceCatalogEntry], legacyManifests: [SourceManifest] = []) {
    update(manifests: catalog.isEmpty ? legacyManifests : catalog.map(\.manifest))
  }

  #if DEBUG
    func setImageForTesting(_ image: NSImage, sourceID: String) {
      images[sourceID] = image
    }
  #endif

  func load(sourceID: String) async {
    guard images[sourceID] == nil, loading.insert(sourceID).inserted else { return }
    defer { loading.remove(sourceID) }

    if let bundleID = branding[sourceID]?.bundleIdentifier, !bundleID.isEmpty,
      let appURL = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleID)
    {
      images[sourceID] = NSWorkspace.shared.icon(forFile: appURL.path)
      return
    }

    if let bundleID = branding[sourceID]?.artworkBundleIdentifier, !bundleID.isEmpty,
      let data = await artwork.data(bundleIdentifier: bundleID, cacheKey: sourceID),
      let image = NSImage(data: data)
    {
      images[sourceID] = image
    }
  }

  private func placeholder(for sourceID: String) -> NSImage {
    let symbol =
      branding[sourceID].flatMap {
        $0.symbolName.isEmpty ? nil : $0.symbolName
      } ?? "shippingbox.fill"
    return NSImage(systemSymbolName: symbol, accessibilityDescription: sourceID)
      ?? NSImage(size: NSSize(width: 32, height: 32))
  }
}

struct SourceIconView: View {
  @Environment(SourceIconStore.self) private var icons

  let sourceID: String
  let size: CGFloat

  var body: some View {
    Image(nsImage: icons.image(for: sourceID))
      .resizable()
      .scaledToFit()
      .frame(width: size, height: size)
      .clipShape(.rect(cornerRadius: size * 0.22))
      .task(id: sourceID) {
        await icons.load(sourceID: sourceID)
      }
  }
}
