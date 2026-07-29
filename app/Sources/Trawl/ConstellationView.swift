import AppKit
import QuartzCore
import SwiftUI
import TrawlCore

struct ConstellationView: View {
  @Environment(\.accessibilityReduceMotion) private var reduceMotion

  let trawlers: [RestingTrawler]
  let trawlerDetailOverrides: [String: String]
  let disabledTrawlerManifestIdentities: Set<String>
  let activity: ConstellationActivity
  let trafficEvent: ConstellationTrafficEvent?
  let onSelectEverything: @MainActor @Sendable () -> Void
  let onSelectTrawler: @MainActor @Sendable (RestingTrawler) -> Void

  init(
    trawlers: [RestingTrawler],
    trawlerDetailOverrides: [String: String] = [:],
    disabledTrawlerManifestIdentities: Set<String> = [],
    activity: ConstellationActivity = .idle,
    trafficEvent: ConstellationTrafficEvent? = nil,
    onSelectEverything: @escaping @MainActor @Sendable () -> Void,
    onSelectTrawler: @escaping @MainActor @Sendable (RestingTrawler) -> Void
  ) {
    self.trawlers = trawlers
    self.trawlerDetailOverrides = trawlerDetailOverrides
    self.disabledTrawlerManifestIdentities = disabledTrawlerManifestIdentities
    self.activity = activity
    self.trafficEvent = trafficEvent
    self.onSelectEverything = onSelectEverything
    self.onSelectTrawler = onSelectTrawler
  }

  init(
    trawlers: [RestingTrawler],
    isSyncing: Bool,
    onSelectEverything: @escaping @MainActor @Sendable () -> Void,
    onSelectTrawler: @escaping @MainActor @Sendable (RestingTrawler) -> Void
  ) {
    self.init(
      trawlers: trawlers,
      activity: isSyncing
        ? .syncing(sourceIDs: Set(trawlers.map(\.id)))
        : .idle,
      onSelectEverything: onSelectEverything,
      onSelectTrawler: onSelectTrawler
    )
  }

  var body: some View {
    GeometryReader { geometry in
      let size = Self.canvasSize(in: geometry.size)
      let layout = ConstellationLayout(size: size, trawlers: trawlers)
      let snapshot = layout.snapshot()

      ZStack(alignment: .topLeading) {
        CoreAnimationNetwork(
          centre: snapshot.centre,
          centreDiameter: snapshot.centreDiameter,
          visualScale: snapshot.visualScale,
          contextNodes: snapshot.contextNodes,
          segments: snapshot.segments,
          activity: activity,
          trafficEvent: trafficEvent,
          reduceMotion: reduceMotion
        )
        CentreButton(diameter: snapshot.centreDiameter, action: onSelectEverything)
          .position(snapshot.centre)
        ForEach(snapshot.trawlers) { placement in
          OrbitingTrawlerNode(
            placement: placement,
            detail: trawlerDetailOverrides[placement.trawler.id] ?? placement.trawler.detail,
            isEnabled: !disabledTrawlerManifestIdentities.contains(placement.trawler.id),
            action: { onSelectTrawler(placement.trawler) }
          )
        }
      }
      .frame(width: size.width, height: size.height)
      .position(x: geometry.size.width / 2, y: geometry.size.height / 2)
    }
  }

  static func canvasSize(in available: CGSize) -> CGSize {
    let height = min(available.height, TrawlDesign.constellationMaximumHeight)
    let width = min(
      available.width,
      height * TrawlDesign.constellationMaximumAspectRatio,
      TrawlDesign.constellationMaximumWidth
    )
    return CGSize(width: width, height: height)
  }
}

private struct OrbitingTrawlerNode: View {
  @Environment(\.accessibilityReduceMotion) private var reduceMotion
  @Environment(TrawlerIconStore.self) private var iconStore

  let placement: MovingTrawler
  let detail: String?
  let isEnabled: Bool
  let action: @MainActor @Sendable () -> Void

  var body: some View {
    CoreAnimationOrbitHost(
      rootView: AnyView(
        TrawlerNode(
          trawler: placement.trawler,
          detail: detail,
          diameter: placement.diameter,
          contentWidth: CGFloat(placement.metrics.labelWidth),
          labelAllowance: CGFloat(placement.metrics.labelHeight),
          isEnabled: isEnabled,
          action: action
        )
        .environment(iconStore)
      ),
      contentSize: CGSize(
        width: CGFloat(placement.metrics.labelWidth),
        height: placement.diameter + CGFloat(placement.metrics.labelHeight)
          + ConstellationLabelLayout.iconSpacing
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
    if self.contentSize != contentSize || self.motion != motion || self.reduceMotion != reduceMotion
    {
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
  let diameter: CGFloat
  let action: @MainActor @Sendable () -> Void

  nonisolated init(diameter: CGFloat, action: @MainActor @escaping @Sendable () -> Void) {
    self.diameter = diameter
    self.action = action
  }

  var body: some View {
    Button(action: action) {
      ZStack {
        Image(nsImage: NSApplication.shared.applicationIconImage)
          .resizable()
          .scaledToFit()
          .frame(width: diameter, height: diameter)
        Text("Find anything in your archive")
          .font(.callout.weight(.semibold))
          .fixedSize()
          .offset(y: diameter / 2 + 4)
      }
    }
    .buttonStyle(.plain)
    .help("Find anything in your archive")
    .accessibilityLabel("Find anything in your archive")
  }
}

private struct TrawlerNode: View {
  @FocusState private var isFocused: Bool

  let trawler: RestingTrawler
  let detail: String?
  let diameter: CGFloat
  let contentWidth: CGFloat
  let labelAllowance: CGFloat
  let isEnabled: Bool
  let action: @MainActor @Sendable () -> Void

  nonisolated init(
    trawler: RestingTrawler,
    detail: String?,
    diameter: CGFloat,
    contentWidth: CGFloat,
    labelAllowance: CGFloat,
    isEnabled: Bool = true,
    action: @MainActor @escaping @Sendable () -> Void
  ) {
    self.trawler = trawler
    self.detail = detail
    self.diameter = diameter
    self.contentWidth = contentWidth
    self.labelAllowance = labelAllowance
    self.isEnabled = isEnabled
    self.action = action
  }

  var body: some View {
    Button {
      if isEnabled { action() }
    } label: {
      ZStack(alignment: .top) {
        VStack(spacing: ConstellationLabelLayout.iconSpacing) {
          TrawlerIconBadge(
            registeredTrawlerManifestIdentity: trawler.id,
            diameter: diameter
          )
          TrawlerLabel(
            title: TrawlerRestingCopy.title(for: trawler),
            detail: detail,
            width: contentWidth,
            titleLineLimit: ConstellationLabelLayout.titleLineLimit(for: labelAllowance),
            isCompact: ConstellationLabelLayout.isCompact(for: labelAllowance)
          )
        }
        .frame(
          width: contentWidth,
          height: diameter + labelAllowance + ConstellationLabelLayout.iconSpacing,
          alignment: .top
        )

        RoundedRectangle(cornerRadius: 16)
          .stroke(isFocused ? TrawlDesign.brandRed : .clear, lineWidth: 2)
          .padding(-6)
          .allowsHitTesting(false)
      }
      .frame(
        width: contentWidth,
        height: diameter + labelAllowance + ConstellationLabelLayout.iconSpacing,
        alignment: .top
      )
      .contentShape(.rect)
    }
    .buttonStyle(.plain)
    .opacity(isEnabled ? 1 : 0.78)
    .focusable(isEnabled)
    .focused($isFocused)
    .focusEffectDisabled()
    .help(
      isEnabled
        ? "Search \(trawler.registeredTrawlerDisplayName)"
        : "\(trawler.registeredTrawlerDisplayName) · Coming soon")
    .accessibilityLabel(accessibilityLabel)
  }

  private var accessibilityLabel: String {
    [TrawlerRestingCopy.title(for: trawler), detail]
      .compactMap { $0 }
      .joined(separator: ". ")
  }
}

private struct TrawlerIconBadge: View {
  let registeredTrawlerManifestIdentity: String
  let diameter: CGFloat

  var body: some View {
    TrawlerIconView(
      registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity,
      size: diameter)
      .shadow(color: .black.opacity(0.12), radius: 9, y: 4)
  }
}

enum ConstellationLabelLayout {
  static let iconSpacing: CGFloat = 7

  static func isCompact(for labelAllowance: CGFloat) -> Bool { labelAllowance < 92 }

  static func titleLineLimit(for labelAllowance: CGFloat) -> Int {
    isCompact(for: labelAllowance) ? 2 : 1
  }
}

struct TrawlerLabel: View {
  let title: String
  let detail: String?
  let width: CGFloat
  let titleLineLimit: Int
  let isCompact: Bool

  var body: some View {
    VStack(spacing: isCompact ? 1 : 2) {
      Text(title)
        .font(isCompact ? .caption2.weight(.semibold) : .body.weight(.semibold))
        .foregroundStyle(.primary)
        .lineLimit(titleLineLimit)
        .fixedSize(horizontal: false, vertical: true)
        .multilineTextAlignment(.center)
      if let detail {
        Text(detail)
          .font(isCompact ? .caption2 : .caption)
          .foregroundStyle(.secondary)
          .fixedSize(horizontal: false, vertical: true)
          .multilineTextAlignment(.center)
      }
    }
    .padding(.horizontal, isCompact ? 6 : 8)
    .padding(.vertical, isCompact ? 3 : 5)
    .frame(width: width)
    .background(.ultraThinMaterial, in: .rect(cornerRadius: 9))
  }
}
