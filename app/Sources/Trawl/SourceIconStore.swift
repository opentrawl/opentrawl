import AppKit
import Observation
import SwiftUI
import TrawlCore

@MainActor
@Observable
final class SourceIconStore {
  private let artwork = AppStoreArtwork()
  private var images: [String: NSImage] = [:]
  private var loading: Set<String> = []

  func image(for sourceID: String) -> NSImage {
    images[sourceID] ?? placeholder(for: sourceID)
  }

  func load(sourceID: String) async {
    guard images[sourceID] == nil, loading.insert(sourceID).inserted else { return }
    defer { loading.remove(sourceID) }

    if let bundleID = MacAppCatalog.bundleIdentifier(for: sourceID),
      let appURL = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleID)
    {
      images[sourceID] = NSWorkspace.shared.icon(forFile: appURL.path)
      return
    }

    if let data = await artwork.data(for: sourceID), let image = NSImage(data: data) {
      images[sourceID] = image
    }
  }

  private func placeholder(for sourceID: String) -> NSImage {
    let symbol: String
    switch sourceID {
    case "gmail": symbol = "envelope.fill"
    case "twitter": symbol = "bubble.left.and.bubble.right.fill"
    case "code": symbol = "chevron.left.forwardslash.chevron.right"
    default: symbol = "shippingbox.fill"
    }
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
