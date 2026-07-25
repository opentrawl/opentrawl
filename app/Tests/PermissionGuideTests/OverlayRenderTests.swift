import AppKit
import SwiftUI
import Testing

@testable import PermissionGuide

@Suite struct OverlayRenderTests {
  @Test @MainActor func rendersBothPhases() throws {
    for (phase, name) in [(GuideModel.Phase.guiding, "guiding"), (.granted, "granted")] {
      let model = GuideModel(
        icon: sampleIcon(),
        appName: "OpenTrawl",
        dragURL: URL(fileURLWithPath: "/Applications/OpenTrawl.app"),
        pointerAngle: .degrees(20)
      )
      model.phase = phase

      let renderer = ImageRenderer(content: GuideOverlayView(model: model))
      renderer.scale = 2
      let image = try #require(renderer.nsImage)
      #expect(image.size.width > 0 && image.size.height > 0)

      let output = URL(fileURLWithPath: "/tmp/permissionguide_\(name).png")
      let data = try #require(pngData(from: image))
      try data.write(to: output)
    }
  }

  @Test @MainActor func floatingPanelIsNonactivatingButCanReceiveEscape() {
    let model = GuideModel(
      icon: sampleIcon(),
      appName: "OpenTrawl",
      dragURL: URL(fileURLWithPath: "/Applications/OpenTrawl.app"),
      pointerAngle: nil
    )
    let panel = FloatingPanel(model: model)
    var cancelled = false
    panel.cancelHandler = { cancelled = true }

    #expect(panel.styleMask.contains(.nonactivatingPanel))
    #expect(panel.canBecomeKey)
    #expect(!panel.canBecomeMain)
    #expect(panel.level == .floating)
    panel.cancelOperation(nil)
    #expect(cancelled)
  }

  private func sampleIcon() -> NSImage {
    let size = NSSize(width: 84, height: 84)
    let image = NSImage(size: size)
    image.lockFocus()
    NSColor.systemBlue.setFill()
    NSBezierPath(
      roundedRect: NSRect(origin: .zero, size: size),
      xRadius: 18,
      yRadius: 18
    ).fill()
    image.unlockFocus()
    return image
  }

  private func pngData(from image: NSImage) -> Data? {
    guard let tiff = image.tiffRepresentation,
      let representation = NSBitmapImageRep(data: tiff)
    else {
      return nil
    }
    return representation.representation(using: .png, properties: [:])
  }
}
