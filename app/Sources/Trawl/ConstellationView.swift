import AppKit
import QuartzCore
import SwiftUI
import TrawlClient
import TrawlCore

struct ConstellationView: View {
  @Environment(\.accessibilityReduceMotion) private var reduceMotion

  let sources: [SourceStatus]
  let activity: ConstellationActivity
  let onSelectEverything: @MainActor @Sendable () -> Void
  let onSelectSource: @MainActor @Sendable (SourceStatus) -> Void

  init(
    sources: [SourceStatus],
    activity: ConstellationActivity = .idle,
    onSelectEverything: @escaping @MainActor @Sendable () -> Void,
    onSelectSource: @escaping @MainActor @Sendable (SourceStatus) -> Void
  ) {
    self.sources = sources
    self.activity = activity
    self.onSelectEverything = onSelectEverything
    self.onSelectSource = onSelectSource
  }

  init(
    sources: [SourceStatus],
    isSyncing: Bool,
    onSelectEverything: @escaping @MainActor @Sendable () -> Void,
    onSelectSource: @escaping @MainActor @Sendable (SourceStatus) -> Void
  ) {
    self.init(
      sources: sources,
      activity: isSyncing
        ? .syncing(sourceIDs: Set(sources.map(\.id)), response: nil)
        : .idle,
      onSelectEverything: onSelectEverything,
      onSelectSource: onSelectSource
    )
  }

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
        CoreAnimationNetwork(
          centre: snapshot.centre,
          contextNodes: snapshot.contextNodes,
          segments: snapshot.segments,
          activity: activity,
          reduceMotion: reduceMotion
        )
        CentreButton(isWorking: activity.isWorkInProgress, action: onSelectEverything)
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

private struct OrbitingSourceNode: View {
  @Environment(\.accessibilityReduceMotion) private var reduceMotion
  @Environment(SourceIconStore.self) private var iconStore

  let placement: MovingSource
  let action: @MainActor @Sendable () -> Void

  var body: some View {
    CoreAnimationOrbitHost(
      rootView: AnyView(
        SourceNode(
          source: placement.source,
          diameter: placement.diameter,
          contentWidth: CGFloat(placement.metrics.labelWidth),
          action: action
        )
        .environment(iconStore)
      ),
      contentSize: CGSize(
        width: CGFloat(placement.metrics.labelWidth),
        height: placement.diameter + ConstellationGeometry.sourceLabelAllowance
      ),
      motion: placement.motion,
      reduceMotion: reduceMotion
    )
    .frame(
      width: CGFloat(placement.metrics.hostSize.x),
      height: CGFloat(placement.metrics.hostSize.y)
    )
    .position(
      x: placement.anchor.x,
      y: placement.anchor.y + CGFloat(placement.metrics.hostCentreYOffset)
    )
  }
}

private struct CoreAnimationOrbitHost: NSViewRepresentable {
  let rootView: AnyView
  let contentSize: CGSize
  let motion: ConstellationMotion
  let reduceMotion: Bool

  func makeNSView(context: Context) -> OrbitLayerView {
    let view = OrbitLayerView()
    view.update(
      rootView: rootView,
      contentSize: contentSize,
      motion: motion,
      reduceMotion: reduceMotion
    )
    return view
  }

  func updateNSView(_ view: OrbitLayerView, context: Context) {
    view.update(
      rootView: rootView,
      contentSize: contentSize,
      motion: motion,
      reduceMotion: reduceMotion
    )
  }
}

@MainActor
private final class OrbitLayerView: NSView {
  private let hostingView = NSHostingView(rootView: AnyView(EmptyView()))
  private var contentSize = CGSize.zero
  private var motion = ConstellationMotion(sourceID: "opentrawl")
  private var reduceMotion = false
  private var animationConfiguration: String?

  override var isFlipped: Bool { true }

  override init(frame frameRect: NSRect) {
    super.init(frame: frameRect)
    wantsLayer = true
    layer?.masksToBounds = false
    layer?.backgroundColor = NSColor.clear.cgColor
    addSubview(hostingView)
    hostingView.wantsLayer = true
    hostingView.layer?.masksToBounds = false
    hostingView.layer?.backgroundColor = NSColor.clear.cgColor
    setAccessibilityElement(false)
  }

  @available(*, unavailable)
  required init?(coder: NSCoder) {
    nil
  }

  func update(
    rootView: AnyView,
    contentSize: CGSize,
    motion: ConstellationMotion,
    reduceMotion: Bool
  ) {
    hostingView.rootView = rootView
    if self.contentSize != contentSize || self.motion != motion || self.reduceMotion != reduceMotion {
      self.contentSize = contentSize
      self.motion = motion
      self.reduceMotion = reduceMotion
      animationConfiguration = nil
      needsLayout = true
    }
    updateRasterisationScale()
  }

  override func layout() {
    super.layout()
    let targetFrame = CGRect(
      x: bounds.midX - contentSize.width / 2,
      y: bounds.midY - contentSize.height / 2,
      width: contentSize.width,
      height: contentSize.height
    )
    if hostingView.frame != targetFrame {
      CATransaction.begin()
      CATransaction.setDisableActions(true)
      hostingView.frame = targetFrame
      CATransaction.commit()
      animationConfiguration = nil
    }
    configureAnimation()
  }

  override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    animationConfiguration = nil
    updateRasterisationScale()
    configureAnimation()
  }

  override func hitTest(_ point: NSPoint) -> NSView? {
    let transform = hostingView.layer?.presentation()?.transform ?? CATransform3DIdentity
    let adjustedPoint = NSPoint(x: point.x - transform.m41, y: point.y - transform.m42)
    guard hostingView.frame.contains(adjustedPoint) else { return nil }
    return hostingView.hitTest(hostingView.convert(adjustedPoint, from: self))
  }

  private func updateRasterisationScale() {
    let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 2
    hostingView.layer?.contentsScale = scale
    hostingView.layer?.shouldRasterize = true
    hostingView.layer?.rasterizationScale = scale
    hostingView.layer?.drawsAsynchronously = true
    hostingView.layer?.magnificationFilter = .linear
    hostingView.layer?.minificationFilter = .linear
  }

  private func configureAnimation() {
    guard bounds.width > 0, bounds.height > 0, let target = hostingView.layer else { return }
    let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 2
    let configuration =
      "\(bounds.width):\(bounds.height):\(scale):\(motion.phaseOffset):\(motion.horizontalAmplitude):"
      + "\(motion.verticalAmplitude):\(motion.duration):\(reduceMotion)"
    guard animationConfiguration != configuration else { return }
    animationConfiguration = configuration

    target.removeAnimation(forKey: "opentrawl.orbit")
    CATransaction.begin()
    CATransaction.setDisableActions(true)
    target.transform = CATransform3DIdentity
    CATransaction.commit()
    guard !reduceMotion else { return }

    let values = (0...CoreAnimationTimeline.sampleCount).map { sample in
      let phase = Double(sample) / Double(CoreAnimationTimeline.sampleCount)
      let translation = motion.translation(at: phase)
      return NSValue(
        caTransform3D: CATransform3DMakeTranslation(
          CGFloat(translation.dx),
          CGFloat(translation.dy),
          0
        )
      )
    }
    let animation = CAKeyframeAnimation(keyPath: "transform")
    animation.values = values
    animation.calculationMode = .linear
    animation.timingFunction = CAMediaTimingFunction(name: .linear)
    animation.preferredFrameRateRange = CoreAnimationTimeline.frameRateRange
    animation.duration = motion.duration
    animation.repeatCount = .infinity
    animation.isRemovedOnCompletion = false
    animation.fillMode = .both
    animation.beginTime = CoreAnimationTimeline.beginTime(for: target)
    target.add(animation, forKey: "opentrawl.orbit")
  }
}

private struct CentreButton: View {
  let isWorking: Bool
  let action: @MainActor @Sendable () -> Void

  nonisolated init(isWorking: Bool, action: @MainActor @escaping @Sendable () -> Void) {
    self.isWorking = isWorking
    self.action = action
  }

  var body: some View {
    Button(action: action) {
      ZStack {
        Image(nsImage: NSApplication.shared.applicationIconImage)
          .resizable()
          .scaledToFit()
          .frame(width: TrawlDesign.centreSize, height: TrawlDesign.centreSize)
        if isWorking {
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
  @FocusState private var isFocused: Bool

  let source: SourceStatus
  let diameter: CGFloat
  let contentWidth: CGFloat
  let action: @MainActor @Sendable () -> Void

  nonisolated init(
    source: SourceStatus,
    diameter: CGFloat,
    contentWidth: CGFloat,
    action: @MainActor @escaping @Sendable () -> Void
  ) {
    self.source = source
    self.diameter = diameter
    self.contentWidth = contentWidth
    self.action = action
  }

  var body: some View {
    Button(action: action) {
      ZStack(alignment: .top) {
        VStack(spacing: 7) {
          SourceIconBadge(sourceID: source.id, diameter: diameter, state: source.state)
          SourceLabel(
            primary: source.counts.first?.display ?? source.name,
            lastSynced: source.lastSyncedDisplay
          )
        }
        .frame(
          width: contentWidth,
          height: diameter + ConstellationGeometry.sourceLabelAllowance,
          alignment: .top
        )

        RoundedRectangle(cornerRadius: 16)
          .stroke(isFocused ? TrawlDesign.brandRed : .clear, lineWidth: 2)
          .padding(-6)
          .allowsHitTesting(false)
      }
      .frame(
        width: contentWidth,
        height: diameter + ConstellationGeometry.sourceLabelAllowance,
        alignment: .top
      )
      .contentShape(.rect)
    }
    .buttonStyle(.plain)
    .focusable()
    .focused($isFocused)
    .focusEffectDisabled()
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
