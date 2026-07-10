import AppKit
import SwiftUI
import TrawlClient

struct ConstellationView: View {
  @Environment(\.accessibilityReduceMotion) private var reduceMotion

  let sources: [SourceStatus]
  let isSyncing: Bool
  let onSelectEverything: @MainActor @Sendable () -> Void
  let onSelectSource: @MainActor @Sendable (SourceStatus) -> Void

  var body: some View {
    GeometryReader { geometry in
      let size = geometry.size
      let layout = ConstellationLayout(
        size: size,
        sources: sources,
        meshSeed: TrawlDesign.meshSeed
      )
      let snapshot = layout.snapshot()

      ZStack(alignment: .topLeading) {
        DynamicNetwork(
          contextNodes: snapshot.contextNodes,
          segments: snapshot.segments
        )
        CoreAnimationNetworkSignals(
          segments: snapshot.segments,
          reduceMotion: reduceMotion
        )
        CentreButton(isSyncing: isSyncing, action: onSelectEverything)
          .position(snapshot.centre)
        ForEach(snapshot.sources) { placement in
          OrbitingSourceNode(
            placement: placement,
            action: { onSelectSource(placement.source) }
          )
        }
      }
      .frame(width: size.width, height: size.height)
    }
  }
}

private struct DynamicNetwork: View {
  let contextNodes: [CGPoint]
  let segments: [NetworkSegment]

  var body: some View {
    Canvas { context, _ in
      for segment in segments {
        var path = Path()
        path.move(to: segment.start)
        path.addLine(to: segment.end)
        context.stroke(
          path,
          with: .color(segment.colour),
          style: StrokeStyle(
            lineWidth: segment.kind == .context ? 0.85 : 1.15,
            lineCap: .round
          )
        )
      }

      for (index, point) in contextNodes.enumerated() {
        let diameter: CGFloat = index.isMultiple(of: 5) ? 5 : 3.5
        context.fill(
          Path(
            ellipseIn: CGRect(
              x: point.x - diameter / 2,
              y: point.y - diameter / 2,
              width: diameter,
              height: diameter
            )
          ),
          with: .color(Color.primary.opacity(index.isMultiple(of: 5) ? 0.18 : 0.11))
        )
      }

    }
    .allowsHitTesting(false)
    .accessibilityHidden(true)
  }
}

private struct OrbitingSourceNode: View {
  @Environment(\.accessibilityReduceMotion) private var reduceMotion
  @Environment(SourceIconStore.self) private var iconStore

  let placement: MovingSource
  let action: @MainActor @Sendable () -> Void

  var body: some View {
    let motion = OrbitMotion(sourceID: placement.source.id)
    CoreAnimationOrbitHost(
      rootView: AnyView(
        SourceNode(
          source: placement.source,
          diameter: placement.diameter,
          action: action
        )
        .environment(iconStore)
      ),
      contentSize: CGSize(width: 154, height: placement.diameter + 58),
      motion: motion,
      reduceMotion: reduceMotion
    )
    .frame(width: 194, height: 164)
    .position(
      x: placement.anchor.x,
      y: placement.anchor.y + TrawlDesign.sourceGraphAnchorOffset
    )
  }
}

struct OrbitMotion: Sendable, Equatable {
  let phaseOffset: Double
  let horizontal: CGFloat
  let vertical: CGFloat
  let duration: TimeInterval

  init(sourceID: String) {
    let hash = sourceID.utf8.reduce(0xcbf2_9ce4_8422_2325) { partial, byte in
      (partial ^ UInt64(byte)) &* 0x100_0000_01b3
    }
    phaseOffset = Double(hash & 0xffff) / Double(UInt16.max)
    horizontal = 12 + CGFloat((hash >> 16) & 0xff) / 255 * 8
    vertical = 8 + CGFloat((hash >> 24) & 0xff) / 255 * 6
    duration = 10 + Double((hash >> 32) & 0xff) / 255 * 4
  }
}

extension NetworkSegment {
  fileprivate var colour: Color {
    switch kind {
    case .context: TrawlDesign.net
    case .source: TrawlDesign.spoke
    case .centre: TrawlDesign.brandRed.opacity(0.24)
    }
  }
}

private struct CentreButton: View {
  let isSyncing: Bool
  let action: @MainActor @Sendable () -> Void

  nonisolated init(isSyncing: Bool, action: @MainActor @escaping @Sendable () -> Void) {
    self.isSyncing = isSyncing
    self.action = action
  }

  var body: some View {
    Button(action: action) {
      ZStack {
        Image(nsImage: NSApplication.shared.applicationIconImage)
          .resizable()
          .scaledToFit()
          .frame(width: TrawlDesign.centreSize, height: TrawlDesign.centreSize)
        if isSyncing {
          ProgressView()
            .controlSize(.small)
            .padding(7)
            .background(.ultraThinMaterial, in: Circle())
            .offset(x: 38, y: 38)
        }
      }
    }
    .buttonStyle(.plain)
    .help("Search everything")
    .accessibilityLabel("Search everything")
  }
}

private struct SourceNode: View {
  let source: SourceStatus
  let diameter: CGFloat
  let action: @MainActor @Sendable () -> Void

  nonisolated init(
    source: SourceStatus,
    diameter: CGFloat,
    action: @MainActor @escaping @Sendable () -> Void
  ) {
    self.source = source
    self.diameter = diameter
    self.action = action
  }

  var body: some View {
    Button(action: action) {
      VStack(spacing: 7) {
        SourceIconBadge(sourceID: source.id, diameter: diameter, state: source.state)
        SourceLabel(
          primary: source.counts.first?.display ?? source.name,
          lastSynced: source.lastSyncedDisplay
        )
      }
      .frame(width: 154)
      .contentShape(.rect)
    }
    .buttonStyle(.plain)
    .help("Search \(source.name)")
    .accessibilityLabel("Search \(source.name), \(source.summary)")
  }
}

private struct SourceIconBadge: View {
  let sourceID: String
  let diameter: CGFloat
  let state: String

  var body: some View {
    ZStack(alignment: .bottomTrailing) {
      SourceIconView(sourceID: sourceID, size: diameter)
        .shadow(color: .black.opacity(0.12), radius: 9, y: 4)
      Circle()
        .fill(statusColour)
        .frame(width: 12, height: 12)
        .overlay(Circle().stroke(.white, lineWidth: 2))
    }
  }

  private var statusColour: Color {
    switch state {
    case "ok": .green
    case "stale": .orange
    default: TrawlDesign.brandRed
    }
  }
}

private struct SourceLabel: View {
  let primary: String
  let lastSynced: String

  var body: some View {
    VStack(spacing: 2) {
      Text(primary)
        .font(.body.weight(.semibold))
        .foregroundStyle(.primary)
        .lineLimit(1)
        .minimumScaleFactor(0.88)
      Text(syncText)
        .font(.callout)
        .foregroundStyle(.primary.opacity(0.78))
        .lineLimit(1)
        .minimumScaleFactor(0.88)
    }
    .padding(.horizontal, 8)
    .padding(.vertical, 5)
    .background(.thinMaterial, in: .rect(cornerRadius: 9))
    .shadow(color: .black.opacity(0.05), radius: 3, y: 1)
  }

  private var syncText: LocalizedStringKey {
    if lastSynced == "not synced yet" || lastSynced.isEmpty {
      return "Not synced yet"
    }
    return "Last synced \(lastSynced)"
  }
}
