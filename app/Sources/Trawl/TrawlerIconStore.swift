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
  private var derivedLaneAccents: [RegisteredTrawlerIdentity: Color] = [:]

  func image(for registeredTrawler: RegisteredTrawlerIdentity) -> NSImage {
    images[registeredTrawler] ?? placeholder(for: registeredTrawler)
  }

  /// Derives each lane's colour dynamically from its rendered app icon so
  /// built-in apps and third-party plugins stay harmonious without a hardcoded
  /// per-app colour table. Monochrome icons fall back to a neutral slate.
  func laneAccentColor(for registeredTrawler: RegisteredTrawlerIdentity) -> Color {
    if let cached = derivedLaneAccents[registeredTrawler] {
      return cached
    }
    let resolvedImage = image(for: registeredTrawler)
    let color = Self.extractLaneAccentColor(from: resolvedImage)
    if images[registeredTrawler] != nil || branding[registeredTrawler] != nil {
      derivedLaneAccents[registeredTrawler] = color
    }
    return color
  }

  func update(registeredTrawlerCatalog: [RegisteredTrawlerCatalogEntry]) {
    branding = Dictionary(
      uniqueKeysWithValues: registeredTrawlerCatalog.compactMap { entry in
        guard let branding = entry.registeredTrawlerManifest.trawlerBranding else {
          return nil
        }
        return (entry.id, branding)
      })
    for id in derivedLaneAccents.keys where images[id] == nil {
      derivedLaneAccents.removeValue(forKey: id)
    }
  }

  #if DEBUG
    func setImageForTesting(
      _ image: NSImage,
      registeredTrawler: RegisteredTrawlerIdentity
    ) {
      images[registeredTrawler] = image
      derivedLaneAccents[registeredTrawler] = Self.extractLaneAccentColor(from: image)
    }
  #endif

  func load(registeredTrawler: RegisteredTrawlerIdentity) async {
    guard images[registeredTrawler] == nil,
      loading.insert(registeredTrawler).inserted
    else { return }
    defer { loading.remove(registeredTrawler) }

    // A source with no app of its own ships an Icon Composer icon in the app's
    // asset catalog; the system renders its glass like the app icons beside it.
    if let iconName = branding[registeredTrawler]?.iconNameInMacAppAssetCatalog,
      !iconName.isEmpty,
      let bundledIcon = NSImage(named: iconName)
    {
      images[registeredTrawler] = bundledIcon
      derivedLaneAccents[registeredTrawler] = Self.extractLaneAccentColor(from: bundledIcon)
      return
    }

    if let bundleID = branding[registeredTrawler]?.bundleIdentifier,
      !bundleID.isEmpty,
      let appURL = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleID)
    {
      let icon = NSWorkspace.shared.icon(forFile: appURL.path)
      images[registeredTrawler] = icon
      derivedLaneAccents[registeredTrawler] = Self.extractLaneAccentColor(from: icon)
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
      derivedLaneAccents[registeredTrawler] = Self.extractLaneAccentColor(from: image)
    }
  }

  static func extractLaneAccentColor(from image: NSImage) -> Color {
    guard let chromaticHSB = dominantChromaticHSB(from: image) else {
      return TrawlDesign.Colors.dynamicColor(
        light: NSColor(srgbRed: 0.29, green: 0.32, blue: 0.37, alpha: 1.0),
        dark: NSColor(srgbRed: 0.58, green: 0.62, blue: 0.69, alpha: 1.0)
      )
    }
    let hue = chromaticHSB.hue
    // Yellow/gold hues appear lighter to the human eye on white paper, so clamp
    // their light-mode brightness deeper for crisp contrast.
    let isYellowHue = (0.10...0.22).contains(hue)
    let lightMaxBrightness: CGFloat = isYellowHue ? 0.54 : 0.70
    let lightSaturation = min(max(chromaticHSB.saturation, isYellowHue ? 0.64 : 0.56), 0.88)
    let lightBrightness = min(max(chromaticHSB.brightness, 0.42), lightMaxBrightness)

    let darkSaturation = min(max(chromaticHSB.saturation, 0.52), 0.86)
    let darkBrightness = min(max(chromaticHSB.brightness, 0.68), 0.90)

    let lightNS = NSColor(
      calibratedHue: hue,
      saturation: lightSaturation,
      brightness: lightBrightness,
      alpha: 1.0
    ).usingColorSpace(.sRGB) ?? .systemBlue
    let darkNS = NSColor(
      calibratedHue: hue,
      saturation: darkSaturation,
      brightness: darkBrightness,
      alpha: 1.0
    ).usingColorSpace(.sRGB) ?? .systemBlue
    return TrawlDesign.Colors.dynamicColor(light: lightNS, dark: darkNS)
  }

  private struct ChromaticHSB {
    let hue: CGFloat
    let saturation: CGFloat
    let brightness: CGFloat
  }

  private static func dominantChromaticHSB(from image: NSImage) -> ChromaticHSB? {
    let side = 32
    let bytesPerRow = side * 4
    var pixels = [UInt8](repeating: 0, count: side * bytesPerRow)
    guard
      let colorSpace = CGColorSpace(name: CGColorSpace.sRGB),
      let context = CGContext(
        data: &pixels,
        width: side,
        height: side,
        bitsPerComponent: 8,
        bytesPerRow: bytesPerRow,
        space: colorSpace,
        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
      )
    else {
      return nil
    }
    context.interpolationQuality = .medium
    let rect = CGRect(x: 0, y: 0, width: side, height: side)
    if let cgImage = image.cgImage(forProposedRect: nil, context: nil, hints: nil) {
      context.draw(cgImage, in: rect)
    } else {
      let graphicsContext = NSGraphicsContext(cgContext: context, flipped: false)
      NSGraphicsContext.saveGraphicsState()
      NSGraphicsContext.current = graphicsContext
      image.draw(in: rect)
      NSGraphicsContext.restoreGraphicsState()
    }

    let binCount = 36
    var binWeights = [Double](repeating: 0, count: binCount)
    var binHueCos = [Double](repeating: 0, count: binCount)
    var binHueSin = [Double](repeating: 0, count: binCount)
    var binSatSum = [Double](repeating: 0, count: binCount)
    var binBriSum = [Double](repeating: 0, count: binCount)

    var opaqueCount = 0
    var chromaticCount = 0

    for offset in stride(from: 0, to: pixels.count, by: 4) {
      let alpha = Double(pixels[offset + 3]) / 255.0
      guard alpha >= 0.70 else { continue }
      opaqueCount += 1
      let r = min(1.0, (Double(pixels[offset]) / 255.0) / alpha)
      let g = min(1.0, (Double(pixels[offset + 1]) / 255.0) / alpha)
      let b = min(1.0, (Double(pixels[offset + 2]) / 255.0) / alpha)

      let maxC = max(r, max(g, b))
      let minC = min(r, min(g, b))
      let delta = maxC - minC
      let brightness = maxC
      let saturation = maxC > 0 ? (delta / maxC) : 0
      guard saturation >= 0.22, brightness >= 0.14 else { continue }
      if brightness > 0.95 && saturation < 0.28 { continue }

      chromaticCount += 1
      var hue: Double = 0
      if delta > 0 {
        if maxC == r {
          hue = ((g - b) / delta).truncatingRemainder(dividingBy: 6.0)
        } else if maxC == g {
          hue = (b - r) / delta + 2.0
        } else {
          hue = (r - g) / delta + 4.0
        }
        hue /= 6.0
        if hue < 0 { hue += 1.0 }
      }

      let bin = min(binCount - 1, Int(hue * Double(binCount)))
      let weight = saturation * saturation * (0.35 + 0.65 * brightness)
      let angle = hue * 2.0 * .pi
      binWeights[bin] += weight
      binHueCos[bin] += cos(angle) * weight
      binHueSin[bin] += sin(angle) * weight
      binSatSum[bin] += saturation * weight
      binBriSum[bin] += brightness * weight
    }

    guard opaqueCount > 0, Double(chromaticCount) / Double(opaqueCount) >= 0.03 else {
      return nil
    }
    var bestBin = 0
    for idx in 1..<binCount where binWeights[idx] > binWeights[bestBin] {
      bestBin = idx
    }
    let totalWeight = binWeights[bestBin]
    guard totalWeight > 0 else { return nil }

    var avgAngle = atan2(binHueSin[bestBin], binHueCos[bestBin])
    if avgAngle < 0 { avgAngle += 2.0 * .pi }
    let avgHue = CGFloat(avgAngle / (2.0 * .pi))
    let avgSat = CGFloat(binSatSum[bestBin] / totalWeight)
    let avgBri = CGFloat(binBriSum[bestBin] / totalWeight)
    return ChromaticHSB(hue: avgHue, saturation: avgSat, brightness: avgBri)
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
      .clipShape(.rect(cornerRadius: size * TrawlDesign.Radius.appIconFraction))
      .task(id: registeredTrawler) {
        await icons.load(registeredTrawler: registeredTrawler)
      }
  }
}
