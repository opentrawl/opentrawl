import AppKit
import Observation
import SwiftUI
import TrawlClient
import TrawlCore

@MainActor
@Observable
final class TrawlerIconStore {
  private let artwork = AppStoreArtwork()
  private var images: [String: NSImage] = [:]
  private var loading: Set<String> = []
  private var branding: [String: TrawlerBranding] = [:]

  func image(for registeredTrawlerManifestIdentity: String) -> NSImage {
    images[registeredTrawlerManifestIdentity]
      ?? placeholder(for: registeredTrawlerManifestIdentity)
  }

  func update(registeredTrawlerCatalog: [RegisteredTrawlerCatalogEntry]) {
    branding = Dictionary(
      uniqueKeysWithValues: registeredTrawlerCatalog.compactMap { entry in
        guard let branding = entry.registeredTrawlerManifest.trawlerBranding else {
          return nil
        }
        return (entry.id, branding)
      })
  }

  #if DEBUG
    func setImageForTesting(
      _ image: NSImage,
      registeredTrawlerManifestIdentity: String
    ) {
      images[registeredTrawlerManifestIdentity] = image
    }
  #endif

  func load(registeredTrawlerManifestIdentity: String) async {
    guard images[registeredTrawlerManifestIdentity] == nil,
      loading.insert(registeredTrawlerManifestIdentity).inserted
    else { return }
    defer { loading.remove(registeredTrawlerManifestIdentity) }

    if let bundleID = branding[registeredTrawlerManifestIdentity]?.bundleIdentifier,
      !bundleID.isEmpty,
      let appURL = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleID)
    {
      images[registeredTrawlerManifestIdentity] = NSWorkspace.shared.icon(forFile: appURL.path)
      return
    }

    if let bundleID = branding[registeredTrawlerManifestIdentity]?.artworkBundleIdentifier,
      !bundleID.isEmpty,
      let data = await artwork.data(
        bundleIdentifier: bundleID,
        cacheKey: registeredTrawlerManifestIdentity),
      let image = NSImage(data: data)
    {
      images[registeredTrawlerManifestIdentity] = image
    }
  }

  private func placeholder(for registeredTrawlerManifestIdentity: String) -> NSImage {
    let symbol =
      branding[registeredTrawlerManifestIdentity].flatMap {
        $0.symbolName.isEmpty ? nil : $0.symbolName
      } ?? "shippingbox.fill"
    return NSImage(
      systemSymbolName: symbol,
      accessibilityDescription: registeredTrawlerManifestIdentity)
      ?? NSImage(size: NSSize(width: 32, height: 32))
  }
}

struct TrawlerIconView: View {
  @Environment(TrawlerIconStore.self) private var icons

  let registeredTrawlerManifestIdentity: String
  let size: CGFloat

  var body: some View {
    Image(nsImage: icons.image(for: registeredTrawlerManifestIdentity))
      .resizable()
      .scaledToFit()
      .frame(width: size, height: size)
      .clipShape(.rect(cornerRadius: size * 0.22))
      .task(id: registeredTrawlerManifestIdentity) {
        await icons.load(
          registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity)
      }
  }
}
