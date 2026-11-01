import AppKit
import SwiftUI

/// The guide docked at the bottom of System Settings: one instruction and a
/// tile drawn like a row of the Full Disk Access list, which is the drag
/// source the list accepts.
struct GuideOverlayView: View {
  static let cardWidth: CGFloat = 480

  @Bindable var model: GuideModel
  let applicationTileDragImage: NSImage

  var body: some View {
    VStack(alignment: .leading, spacing: 12) {
      HStack(spacing: 8) {
        Text(
          model.phase == .granted
            ? OperationalCopy.FullDiskAccessOverlay.granted
            : OperationalCopy.FullDiskAccessOverlay.instruction(appName: model.appName)
        )
        .font(.system(size: 13, weight: .medium))
        .contentTransition(.opacity)
        Spacer(minLength: 0)
        if model.phase == .guiding {
          closeButton
        }
      }
      ApplicationRowTile(
        icon: model.icon,
        appName: model.appName,
        granted: model.phase == .granted
      )
      .overlay {
        if model.phase == .guiding {
          ApplicationTileDragSource(
            dragURL: model.dragURL,
            dragImage: applicationTileDragImage,
            onCreate: { model.dockedTileView = $0 }
          )
          .help(OperationalCopy.FullDiskAccessOverlay.dragHelp)
          .accessibilityLabel(
            OperationalCopy.FullDiskAccessOverlay.dragAccessibilityLabel(appName: model.appName)
          )
        }
      }
      .opacity(model.dockedTileIsWaitingForFlight ? 0 : 1)
    }
    .padding(16)
    .frame(width: Self.cardWidth)
    .background(Color(nsColor: .windowBackgroundColor), in: shape)
    .overlay(shape.strokeBorder(.primary.opacity(0.1), lineWidth: 1))
    .shadow(color: .black.opacity(0.22), radius: 18, y: 8)
    .animation(.snappy(duration: 0.3), value: model.phase)
  }

  private var shape: RoundedRectangle {
    RoundedRectangle(cornerRadius: 20, style: .continuous)
  }

  private var closeButton: some View {
    Button(action: model.onClose) {
      Image(systemName: "xmark")
        .font(.system(size: 10, weight: .bold))
        .foregroundStyle(.secondary)
        .padding(5)
        .background(.quaternary, in: Circle())
    }
    .buttonStyle(.plain)
    .help(OperationalCopy.FullDiskAccessOverlay.dismiss)
  }
}

/// The app as the Full Disk Access list will show it: icon and name. The
/// onboarding page shows the same tile the docked guide offers for dragging.
public struct ApplicationRowTile: View {
  static let width = GuideOverlayView.cardWidth - 32

  let icon: NSImage
  let appName: String
  var granted = false
  var width = ApplicationRowTile.width
  var height: CGFloat = 44
  var filled = true

  public init(icon: NSImage, appName: String, width: CGFloat, height: CGFloat, filled: Bool = true) {
    self.icon = icon
    self.appName = appName
    self.width = width
    self.height = height
    self.filled = filled
  }

  init(icon: NSImage, appName: String, granted: Bool = false) {
    self.icon = icon
    self.appName = appName
    self.granted = granted
  }

  public var body: some View {
    HStack(spacing: 10) {
      Image(nsImage: icon)
        .resizable()
        .interpolation(.high)
        .frame(width: height - 14, height: height - 14)
      Text(appName)
        .font(.system(size: 13, weight: .semibold))
      Spacer(minLength: 0)
      if granted {
        Image(systemName: "checkmark.circle.fill")
          .font(.system(size: 18))
          .symbolRenderingMode(.palette)
          .foregroundStyle(.white, .green)
          .transition(.scale(scale: 0.6).combined(with: .opacity))
      }
    }
    .padding(.horizontal, 10)
    .frame(width: width, height: height)
    .background(
      Color(nsColor: .quaternarySystemFill).opacity(filled ? 1 : 0),
      in: RoundedRectangle(cornerRadius: 8, style: .continuous)
    )
  }

  @MainActor
  static func renderedImage(icon: NSImage, appName: String) -> NSImage {
    let renderer = ImageRenderer(content: ApplicationRowTile(icon: icon, appName: appName))
    renderer.scale = NSScreen.main?.backingScaleFactor ?? 2
    return renderer.nsImage ?? icon
  }
}

/// AppKit drag source over the tile. It takes the first click while System
/// Settings is in front, so the drag starts without first focusing the guide.
private struct ApplicationTileDragSource: NSViewRepresentable {
  let dragURL: URL
  let dragImage: NSImage
  let onCreate: (NSView) -> Void

  func makeNSView(context: Context) -> ApplicationTileDragView {
    let view = ApplicationTileDragView(dragURL: dragURL, dragImage: dragImage)
    onCreate(view)
    return view
  }

  func updateNSView(_ view: ApplicationTileDragView, context: Context) {}
}

final class ApplicationTileDragView: NSView, NSDraggingSource {
  private let dragURL: URL
  private let dragImage: NSImage
  private var mouseDownEvent: NSEvent?

  init(dragURL: URL, dragImage: NSImage) {
    self.dragURL = dragURL
    self.dragImage = dragImage
    super.init(frame: .zero)
  }

  @available(*, unavailable)
  required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }

  override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

  override func resetCursorRects() {
    addCursorRect(bounds, cursor: .openHand)
  }

  override func mouseDown(with event: NSEvent) {
    mouseDownEvent = event
  }

  override func mouseDragged(with event: NSEvent) {
    guard let mouseDownEvent else { return }
    let start = mouseDownEvent.locationInWindow
    let now = event.locationInWindow
    guard hypot(now.x - start.x, now.y - start.y) >= 3 else { return }
    self.mouseDownEvent = nil

    let item = NSDraggingItem(pasteboardWriter: dragURL as NSURL)
    item.setDraggingFrame(bounds, contents: dragImage)
    let session = beginDraggingSession(with: [item], event: mouseDownEvent, source: self)
    session.animatesToStartingPositionsOnCancelOrFail = true
    session.draggingFormation = .none
  }

  func draggingSession(
    _ session: NSDraggingSession,
    sourceOperationMaskFor context: NSDraggingContext
  ) -> NSDragOperation {
    .copy
  }
}
