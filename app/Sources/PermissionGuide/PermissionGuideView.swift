import AppKit
import SwiftUI

public struct PermissionGuideCopy: Sendable {
  public let title: String
  public let instructions: String
  public let waiting: String
  public let continueButton: String
  public let dragAccessibilityLabel: String

  public init(
    title: String,
    instructions: String,
    waiting: String,
    continueButton: String,
    dragAccessibilityLabel: String
  ) {
    self.title = title
    self.instructions = instructions
    self.waiting = waiting
    self.continueButton = continueButton
    self.dragAccessibilityLabel = dragAccessibilityLabel
  }

  static let legacyDefault = PermissionGuideCopy(
    title: "Give OpenTrawl full access",
    instructions: "Drag OpenTrawl into the Full Disk Access list, then turn it on.",
    waiting: "Waiting for access",
    continueButton: "I've turned it on",
    dragAccessibilityLabel: "Drag OpenTrawl to Full Disk Access"
  )
}

public struct PermissionGuideView: View {
  private let bundleURL: URL
  private let icon: NSImage
  private let copy: PermissionGuideCopy
  private let onContinue: () -> Void

  public init(
    bundleURL: URL,
    icon: NSImage,
    copy: PermissionGuideCopy,
    onContinue: @escaping () -> Void
  ) {
    self.bundleURL = bundleURL
    self.icon = icon
    self.copy = copy
    self.onContinue = onContinue
  }

  public var body: some View {
    VStack(spacing: 16) {
      Text(copy.title)
        .font(.headline)

      Text(copy.instructions)
        .multilineTextAlignment(.center)
        .foregroundStyle(.secondary)

      Image(nsImage: icon)
        .resizable()
        .scaledToFit()
        .frame(width: 84, height: 84)
        .onDrag {
          NSItemProvider(object: bundleURL as NSURL)
        } preview: {
          Image(nsImage: icon)
            .resizable()
            .frame(width: 64, height: 64)
        }
        .accessibilityLabel(copy.dragAccessibilityLabel)

      HStack(spacing: 8) {
        ProgressView()
          .controlSize(.small)
        Text(copy.waiting)
          .font(.callout)
          .foregroundStyle(.secondary)
      }

      Button(copy.continueButton, action: onContinue)
    }
    .padding(24)
    .frame(width: 280, height: 280)
  }
}
