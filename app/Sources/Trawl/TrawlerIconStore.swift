import AppKit
import Observation
import SwiftUI
import TrawlClient
import TrawlCore

@MainActor
@Observable
final class TrawlerIconStore {
  private let artwork = AppStoreArtwork()
  private var images: [RegisteredTrawlerIdentity: NSImage] = [:]
  private var loading: Set<RegisteredTrawlerIdentity> = []
  private var branding: [RegisteredTrawlerIdentity: TrawlerBranding] = [:]

  func image(for registeredTrawler: RegisteredTrawlerIdentity) -> NSImage {
    images[registeredTrawler] ?? placeholder(for: registeredTrawler)
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
      registeredTrawler: RegisteredTrawlerIdentity
    ) {
      images[registeredTrawler] = image
    }
  #endif

  func load(registeredTrawler: RegisteredTrawlerIdentity) async {
    guard images[registeredTrawler] == nil,
      loading.insert(registeredTrawler).inserted
    else { return }
    defer { loading.remove(registeredTrawler) }

    if let bundleID = branding[registeredTrawler]?.bundleIdentifier,
      !bundleID.isEmpty,
      let appURL = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleID)
    {
      images[registeredTrawler] = NSWorkspace.shared.icon(forFile: appURL.path)
      return
    }

    if let bundleID = branding[registeredTrawler]?.artworkBundleIdentifier,
      !bundleID.isEmpty,
      let data = await artwork.data(
        bundleIdentifier: bundleID,
        cacheKey: registeredTrawler.registeredTrawlerIdentity),
      let image = NSImage(data: data)
    {
      images[registeredTrawler] = image
    }
  }

  private func placeholder(for registeredTrawler: RegisteredTrawlerIdentity) -> NSImage {
    let symbol =
      branding[registeredTrawler].flatMap {
        $0.symbolName.isEmpty ? nil : $0.symbolName
      } ?? "shippingbox.fill"
    return NSImage(
      systemSymbolName: symbol,
      accessibilityDescription: registeredTrawler.registeredTrawlerIdentity)
      ?? NSImage(size: NSSize(width: 32, height: 32))
  }
}

struct TrawlerIconView: View {
  @Environment(TrawlerIconStore.self) private var icons

  let registeredTrawler: RegisteredTrawlerIdentity
  let size: CGFloat

  var body: some View {
    Image(nsImage: icons.image(for: registeredTrawler))
      .resizable()
      .scaledToFit()
      .frame(width: size, height: size)
      .clipShape(.rect(cornerRadius: size * 0.22))
      .task(id: registeredTrawler) {
        await icons.load(registeredTrawler: registeredTrawler)
      }
  }
}
