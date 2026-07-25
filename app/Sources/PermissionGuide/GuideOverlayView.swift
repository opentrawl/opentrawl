import AppKit
import SwiftUI

/// A small floating guide beside System Settings. The app icon is the drag
/// source accepted by the Full Disk Access list.
struct GuideOverlayView: View {
  @Bindable var model: GuideModel
  @State private var nudge = false

  var body: some View {
    ZStack(alignment: .topTrailing) {
      content
        .padding(22)
        .frame(width: 264)
        .background(.regularMaterial, in: shape)
        .overlay(shape.stroke(.white.opacity(0.12), lineWidth: 1))
        .clipShape(shape)
        .shadow(color: .black.opacity(0.28), radius: 22, y: 12)

      if model.phase == .guiding {
        closeButton.padding(12)
      }
    }
    .animation(.snappy(duration: 0.35), value: model.phase)
  }

  private var shape: RoundedRectangle {
    RoundedRectangle(cornerRadius: 22, style: .continuous)
  }

  @ViewBuilder
  private var content: some View {
    switch model.phase {
    case .guiding:
      guiding.transition(.opacity.combined(with: .scale(scale: 0.96)))
    case .granted:
      granted.transition(.opacity.combined(with: .scale(scale: 0.96)))
    }
  }

  private var guiding: some View {
    VStack(spacing: 16) {
      tile
      VStack(spacing: 5) {
        Text("Grant Full Disk Access")
          .font(.headline)
        Text("Drag \(model.appName) into the list in System Settings, then switch it on.")
          .font(.subheadline)
          .foregroundStyle(.secondary)
          .multilineTextAlignment(.center)
          .fixedSize(horizontal: false, vertical: true)
      }
      pointer
    }
  }

  private var tile: some View {
    Image(nsImage: model.icon)
      .resizable()
      .interpolation(.high)
      .frame(width: 84, height: 84)
      .padding(10)
      .background(.quaternary, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
      .onDrag { NSItemProvider(object: model.dragURL as NSURL) }
      .help("Drag onto the Full Disk Access list")
      .accessibilityLabel("Drag \(model.appName) to Full Disk Access")
  }

  private var pointer: some View {
    Image(systemName: "arrow.right")
      .font(.system(size: 22, weight: .semibold))
      .foregroundStyle(.tint)
      .rotationEffect(model.pointerAngle ?? .zero)
      .offset(x: nudge ? 6 : -2)
      .opacity(model.pointerAngle == nil ? 0.55 : 1)
      .animation(.easeInOut(duration: 0.8).repeatForever(autoreverses: true), value: nudge)
      .onAppear { nudge = true }
      .accessibilityHidden(true)
  }

  private var granted: some View {
    VStack(spacing: 14) {
      Image(systemName: "checkmark.circle.fill")
        .font(.system(size: 56))
        .foregroundStyle(.white, .green)
        .symbolRenderingMode(.palette)
      Text("Full Disk Access granted")
        .font(.headline)
    }
    .padding(.vertical, 12)
  }

  private var closeButton: some View {
    Button(action: model.onClose) {
      Image(systemName: "xmark")
        .font(.system(size: 11, weight: .bold))
        .foregroundStyle(.secondary)
        .padding(6)
        .background(.quaternary, in: Circle())
    }
    .buttonStyle(.plain)
    .help("Dismiss")
  }
}
