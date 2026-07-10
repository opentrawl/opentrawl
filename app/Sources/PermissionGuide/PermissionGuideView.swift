import AppKit
import SwiftUI

public struct PermissionGuideView: View {
  private let bundleURL: URL
  private let icon: NSImage

  public init(bundleURL: URL, icon: NSImage) {
    self.bundleURL = bundleURL
    self.icon = icon
  }

  public var body: some View {
    VStack(spacing: 16) {
      Text("Give OpenTrawl full access")
        .font(.headline)

      Text("Drag OpenTrawl into the Full Disk Access list, then turn it on.")
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
        .accessibilityLabel("Drag OpenTrawl to Full Disk Access")

      HStack(spacing: 8) {
        ProgressView()
          .controlSize(.small)
        Text("Waiting for access")
          .font(.callout)
          .foregroundStyle(.secondary)
      }
    }
    .padding(24)
    .frame(width: 280, height: 240)
  }
}
