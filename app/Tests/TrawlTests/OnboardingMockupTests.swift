import AppKit
import PermissionGuide
import SwiftUI
import Testing

@testable import Trawl
@testable import TrawlClient
@testable import TrawlCore

@Suite(.serialized)
struct OnboardingMockupTests {
  @MainActor
  @Test func renderReviewMockupsWhenRequested() async throws {
    guard let outputPath = ProcessInfo.processInfo.environment["OPENTRAWL_MOCKUP_OUTPUT_DIR"] else {
      return
    }

    let outputDirectory = URL(fileURLWithPath: outputPath, isDirectory: true)
    try FileManager.default.createDirectory(
      at: outputDirectory,
      withIntermediateDirectories: true
    )

    let identity = BuildIdentity(
      version: "0.1.0",
      gitCommit: String(repeating: "a", count: 40)
    )
    let response = try mockupStatus().model()
    let client = MockupStatusClient(response: response)
    let model = AppModel(
      client: client,
      permissionProbe: FullDiskAccessProbe(canaries: [], probePath: { _ in .readable })
    )
    await model.refresh()

    let installations = MacAppInstallations(
      environment: [:],
      applicationIsInstalled: { _ in true }
    )
    installations.refresh(catalog: model.catalog)
    let icons = SourceIconStore()
    icons.update(catalog: model.catalog)
    for entry in model.catalog {
      guard let bundleID = entry.manifest.branding?.bundleIdentifier,
        !bundleID.isEmpty,
        let appURL = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleID)
      else { continue }
      icons.setImageForTesting(
        rasterizedIcon(NSWorkspace.shared.icon(forFile: appURL.path)),
        sourceID: entry.id
      )
    }
    for entry in model.catalog {
      await icons.load(sourceID: entry.id)
      icons.setImageForTesting(
        rasterizedIcon(icons.image(for: entry.id)),
        sourceID: entry.id
      )
    }

    let appIconURL = URL(fileURLWithPath: #filePath)
      .deletingLastPathComponent()
      .deletingLastPathComponent()
      .deletingLastPathComponent()
      .deletingLastPathComponent()
      .appendingPathComponent("assets/brand/exports/icns/OpenTrawl.icns")
    let appIcon = try #require(NSImage(contentsOf: appIconURL))

    try render(
      WelcomeStep(icon: appIcon, onContinue: {}),
      named: "01-welcome.png",
      in: outputDirectory,
      icons: icons
    )
    try render(
      PermissionStep(
        icon: appIcon,
        permissionCheck: .idle,
        buildIdentity: identity,
        onBack: {},
        onOpenSettings: {},
        onCheckAgain: {},
        onContinue: {}
      ),
      named: "02-full-disk-access.png",
      in: outputDirectory,
      icons: icons
    )
    try render(
      PermissionStep(
        icon: appIcon,
        permissionCheck: .notConfirmed,
        buildIdentity: identity,
        onBack: {},
        onOpenSettings: {},
        onCheckAgain: {},
        onContinue: {}
      ),
      named: "02b-full-disk-access-recovery.png",
      in: outputDirectory,
      icons: icons
    )

    let freshResponse = try mockupStatus(hasArchive: false).model()
    let freshClient = MockupBuildingClient(response: freshResponse)
    let freshModel = AppModel(
      client: freshClient,
      permissionProbe: FullDiskAccessProbe(canaries: [], probePath: { _ in .readable })
    )
    await freshModel.refresh()
    let freshBuild = Task {
      await freshModel.syncNow(appIDs: freshModel.syncCandidateAppIDs)
    }
    for _ in 0..<100
    where freshModel.syncProgress.values.filter({ $0 == .building }).count
      < freshModel.syncCandidateAppIDs.count
    {
      await Task.yield()
    }
    try render(
      BuildStep(
        appModel: freshModel,
        appInstallations: installations,
        aiInstruction: "Connect this coding AI to OpenTrawl.",
        hasCopiedAIInstructions: false,
        onCopyAIInstructions: {},
        onBack: {},
        onRetryApp: { _ in },
        onRetryInitialLoad: {},
        onPermissionRecovery: {},
        onStop: {},
        onFinish: {}
      ),
      named: "03a-new-archive-building.png",
      in: outputDirectory,
      icons: icons
    )
    freshBuild.cancel()
    await freshBuild.value

    try render(
      BuildStep(
        appModel: model,
        appInstallations: installations,
        aiInstruction: "Connect this coding AI to OpenTrawl.",
        hasCopiedAIInstructions: false,
        onCopyAIInstructions: {},
        onBack: {},
        onRetryApp: { _ in },
        onRetryInitialLoad: {},
        onPermissionRecovery: {},
        onStop: {},
        onFinish: {}
      ),
      named: "03b-existing-archive-ready.png",
      in: outputDirectory,
      icons: icons
    )
  }

  @MainActor
  private func rasterizedIcon(_ image: NSImage) -> NSImage {
    let pixels = 128
    let representation = NSBitmapImageRep(
      bitmapDataPlanes: nil,
      pixelsWide: pixels,
      pixelsHigh: pixels,
      bitsPerSample: 8,
      samplesPerPixel: 4,
      hasAlpha: true,
      isPlanar: false,
      colorSpaceName: .deviceRGB,
      bytesPerRow: 0,
      bitsPerPixel: 0
    )!
    let context = NSGraphicsContext(bitmapImageRep: representation)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = context
    context.cgContext.clear(CGRect(x: 0, y: 0, width: pixels, height: pixels))
    image.draw(in: CGRect(x: 0, y: 0, width: pixels, height: pixels))
    NSGraphicsContext.restoreGraphicsState()

    let rasterized = NSImage(size: NSSize(width: pixels, height: pixels))
    rasterized.addRepresentation(representation)
    return rasterized
  }

  @MainActor
  private func render<Content: View>(
    _ content: Content,
    named name: String,
    in directory: URL,
    icons: SourceIconStore
  ) throws {
    let view =
      content
      .environment(icons)
      .frame(
        width: TrawlDesign.onboardingWindow.width,
        height: TrawlDesign.onboardingWindow.height
      )
      .background(Color(nsColor: .windowBackgroundColor))
    let renderer = ImageRenderer(content: view)
    renderer.proposedSize = ProposedViewSize(TrawlDesign.onboardingWindow)
    renderer.scale = 2
    let image = try #require(renderer.nsImage)
    let representation = try #require(image.tiffRepresentation.flatMap(NSBitmapImageRep.init))
    let png = try #require(representation.representation(using: .png, properties: [:]))
    try png.write(to: directory.appendingPathComponent(name), options: .atomic)
  }
}

private struct MockupStatusClient: TrawlClient {
  let response: StatusResponse

  func status() async throws -> StatusResponse { response }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}

private actor MockupBuildingClient: TrawlClient {
  let response: StatusResponse

  init(response: StatusResponse) {
    self.response = response
  }

  func status() async throws -> StatusResponse { response }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func sync(
    sourceIDs: [String],
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse {
    for sourceID in sourceIDs {
      progress(.building(sourceID: sourceID))
    }
    try await Task.sleep(for: .seconds(60))
    throw CancellationError()
  }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}

private func mockupStatus(hasArchive: Bool = true) -> Trawl_Federation_V1_StatusResponse {
  let available = [
    ("imessage", "Messages", "message.fill", "com.apple.MobileSMS", Int64(24_182)),
    ("whatsapp", "WhatsApp", "message.badge.filled.fill", "net.whatsapp.WhatsApp", Int64(8_441)),
    ("telegram", "Telegram", "paperplane.fill", "ru.keepcoder.Telegram", Int64(3_206)),
    ("notes", "Notes", "note.text", "com.apple.Notes", Int64(687)),
    ("contacts", "Contacts", "person.crop.circle.fill", "com.apple.AddressBook", Int64(1_204)),
    ("calendar", "Calendar", "calendar", "com.apple.iCal", Int64(7_412)),
  ]
  let comingSoon: [(String, String, String, String?, String?)] = [
    ("gmail", "Gmail", "envelope.fill", nil, "com.google.Gmail"),
    ("photos", "Photos", "photo.on.rectangle.angled", "com.apple.Photos", nil),
    ("twitter", "Twitter (X)", "bubble.left.and.bubble.right.fill", nil, "com.atebits.Tweetie2"),
  ]

  return .with { response in
    response.outcome = .complete
    response.catalog =
      available.map { id, name, symbol, bundleID, _ in
        catalogEntry(
          id: id,
          name: name,
          symbol: symbol,
          bundleID: bundleID,
          releaseState: .available,
          enabled: true
        )
      }
      + comingSoon.map { id, name, symbol, bundleID, artworkBundleID in
        catalogEntry(
          id: id,
          name: name,
          symbol: symbol,
          bundleID: bundleID,
          artworkBundleID: artworkBundleID,
          releaseState: .comingSoon,
          enabled: false
        )
      }
    response.sources = available.map { id, name, symbol, bundleID, count in
      .with { source in
        source.manifest = manifest(id: id, name: name, symbol: symbol, bundleID: bundleID)
        source.state = "ok"
        source.counts = [
          .with {
            $0.id = "items"
            $0.label = "Items"
            $0.value = hasArchive ? count : 0
          }
        ]
      }
    }
  }
}

private func catalogEntry(
  id: String,
  name: String,
  symbol: String,
  bundleID: String? = nil,
  artworkBundleID: String? = nil,
  releaseState: Trawl_Federation_V1_SourceReleaseState,
  enabled: Bool
) -> Trawl_Federation_V1_SourceCatalogEntry {
  .with {
    $0.manifest = manifest(
      id: id,
      name: name,
      symbol: symbol,
      bundleID: bundleID,
      artworkBundleID: artworkBundleID
    )
    $0.releaseState = releaseState
    $0.enabled = enabled
  }
}

private func manifest(
  id: String,
  name: String,
  symbol: String,
  bundleID: String? = nil,
  artworkBundleID: String? = nil
) -> Trawl_Federation_V1_SourceManifest {
  .with {
    $0.sourceID = id
    $0.displayName = name
    $0.branding = .with {
      $0.symbolName = symbol
      if let bundleID { $0.bundleIdentifier = bundleID }
      if let artworkBundleID { $0.artworkBundleIdentifier = artworkBundleID }
    }
  }
}
