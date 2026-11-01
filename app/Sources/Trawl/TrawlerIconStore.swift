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
  private var applicationStyleTiles: [RegisteredTrawlerIdentity: NSImage] = [:]

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

  /// A source with no app icon gets a tile in its own colour, cached per
  /// source because it is drawn on every render.
  private func placeholder(for registeredTrawler: RegisteredTrawlerIdentity) -> NSImage {
    if let tile = applicationStyleTiles[registeredTrawler] {
      return tile
    }
    let sourceBranding = branding[registeredTrawler]
    let symbol = sourceBranding.flatMap { $0.symbolName.isEmpty ? nil : $0.symbolName }
      ?? "shippingbox.fill"
    let accent = sourceBranding.flatMap { NSColor(hexadecimalRGB: $0.accentColor) }
      ?? .systemGray
    let tile = Self.applicationStyleTile(symbolName: symbol, accent: accent)
    if sourceBranding != nil {
      applicationStyleTiles[registeredTrawler] = tile
    }
    return tile
  }

  /// Drawn on the macOS app icon grid (an 824-point body with 185-point
  /// corners on a 1024-point canvas) so it sits level with the app icons
  /// beside it: a gentle gradient of the source's colour, symbol in white.
  static func applicationStyleTile(symbolName: String, accent: NSColor) -> NSImage {
    NSImage(size: NSSize(width: 256, height: 256), flipped: false) { canvas in
      let inset = canvas.width * 100 / 1024
      let body = canvas.insetBy(dx: inset, dy: inset)
      let corner = body.width * 185 / 824
      let shape = NSBezierPath(roundedRect: body, xRadius: corner, yRadius: corner)
      let top = accent.blended(withFraction: 0.16, of: .white) ?? accent
      let bottom = accent.blended(withFraction: 0.14, of: .black) ?? accent
      NSGradient(starting: top, ending: bottom)?.draw(in: shape, angle: -90)
      let glyphConfiguration = NSImage.SymbolConfiguration(
        pointSize: body.height * 0.48, weight: .semibold
      ).applying(NSImage.SymbolConfiguration(paletteColors: [.white]))
      if let glyph = NSImage(systemSymbolName: symbolName, accessibilityDescription: nil)?
        .withSymbolConfiguration(glyphConfiguration)
      {
        let size = glyph.size
        glyph.draw(
          in: NSRect(
            x: body.midX - size.width / 2, y: body.midY - size.height / 2,
            width: size.width, height: size.height))
      }
      return true
    }
  }
}

extension NSColor {
  /// "#RRGGBB", as trawler branding states its colour.
  convenience init?(hexadecimalRGB: String) {
    let digits = hexadecimalRGB.hasPrefix("#") ? hexadecimalRGB.dropFirst() : Substring(hexadecimalRGB)
    guard digits.count == 6, let value = UInt32(digits, radix: 16) else { return nil }
    self.init(
      srgbRed: CGFloat((value >> 16) & 0xFF) / 255,
      green: CGFloat((value >> 8) & 0xFF) / 255,
      blue: CGFloat(value & 0xFF) / 255,
      alpha: 1)
  }
}

struct TrawlerIconView: View {
  @Environment(TrawlerIconStore.self) private var icons

  let registeredTrawler: RegisteredTrawlerIdentity
  let size: CGFloat

  var body: some View {
    Image(nsImage: icons.image(for: registeredTrawler))
      .resizable()
      .interpolation(.high)
      .scaledToFit()
      .frame(width: size, height: size)
      .clipShape(.rect(cornerRadius: size * 0.22))
      .task(id: registeredTrawler) {
        await icons.load(registeredTrawler: registeredTrawler)
      }
  }
}
