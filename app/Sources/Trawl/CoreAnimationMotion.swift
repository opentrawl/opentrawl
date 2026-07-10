import AppKit
import QuartzCore
import SwiftUI

private enum CoreAnimationTimeline {
  static let epoch = CACurrentMediaTime()
  static let sampleCount = 720

  static func beginTime(for layer: CALayer) -> CFTimeInterval {
    layer.convertTime(epoch, from: nil)
  }

  static var frameRateRange: CAFrameRateRange {
    CAFrameRateRange(minimum: 60, maximum: 120, preferred: 120)
  }
}

struct CoreAnimationOrbitHost: NSViewRepresentable {
  let rootView: AnyView
  let contentSize: CGSize
  let motion: OrbitMotion
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
final class OrbitLayerView: NSView {
  private let hostingView = NSHostingView(rootView: AnyView(EmptyView()))
  private var contentSize = CGSize.zero
  private var motion = OrbitMotion(sourceID: "opentrawl")
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
    return nil
  }

  func update(
    rootView: AnyView,
    contentSize: CGSize,
    motion: OrbitMotion,
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
    let adjustedPoint = NSPoint(
      x: point.x - transform.m41,
      y: point.y - transform.m42
    )
    guard hostingView.frame.contains(adjustedPoint) else { return nil }
    return hostingView.hitTest(hostingView.convert(adjustedPoint, from: self))
  }

  private func updateRasterisationScale() {
    let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 2
    hostingView.layer?.contentsScale = scale
    // Freeze the icon, text and material into one retina texture so motion cannot resize or shimmer.
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
      "\(bounds.width):\(bounds.height):\(scale):\(motion.phaseOffset):\(motion.horizontal):"
      + "\(motion.vertical):\(motion.duration):\(reduceMotion)"
    guard animationConfiguration != configuration else { return }
    animationConfiguration = configuration

    target.removeAnimation(forKey: "opentrawl.orbit")
    CATransaction.begin()
    CATransaction.setDisableActions(true)
    target.transform = CATransform3DIdentity
    CATransaction.commit()
    guard !reduceMotion else { return }

    let values = (0...CoreAnimationTimeline.sampleCount).map { sample in
      let progress = Double(sample) / Double(CoreAnimationTimeline.sampleCount)
      let translation = motion.translation(at: progress)
      return NSValue(
        caTransform3D: CATransform3DMakeTranslation(
          translation.dx,
          translation.dy,
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

struct CoreAnimationNetwork: NSViewRepresentable {
  let contextNodes: [CGPoint]
  let segments: [NetworkSegment]
  let reduceMotion: Bool

  func makeNSView(context: Context) -> NetworkLayerView {
    let view = NetworkLayerView()
    view.update(
      contextNodes: contextNodes,
      segments: segments,
      reduceMotion: reduceMotion
    )
    return view
  }

  func updateNSView(_ view: NetworkLayerView, context: Context) {
    view.update(
      contextNodes: contextNodes,
      segments: segments,
      reduceMotion: reduceMotion
    )
  }
}

@MainActor
final class NetworkLayerView: NSView {
  private var contextNodes: [CGPoint] = []
  private var segments: [NetworkSegment] = []
  private var reduceMotion = false
  private var renderedContextNodes: [CGPoint] = []
  private var renderedSegments: [NetworkSegment] = []
  private var renderedReduceMotion: Bool?
  private var renderedSize = CGSize.zero
  private var renderedScale: CGFloat = 0

  override var isFlipped: Bool { true }

  override init(frame frameRect: NSRect) {
    super.init(frame: frameRect)
    wantsLayer = true
    layer?.masksToBounds = false
    layer?.isGeometryFlipped = true
    setAccessibilityElement(false)
  }

  @available(*, unavailable)
  required init?(coder: NSCoder) {
    return nil
  }

  func update(
    contextNodes: [CGPoint],
    segments: [NetworkSegment],
    reduceMotion: Bool
  ) {
    self.contextNodes = contextNodes
    self.segments = segments
    self.reduceMotion = reduceMotion
    needsLayout = true
  }

  override func layout() {
    super.layout()
    configureNetwork()
  }

  override func viewDidMoveToWindow() {
    super.viewDidMoveToWindow()
    renderedScale = 0
    needsLayout = true
  }

  override func hitTest(_ point: NSPoint) -> NSView? {
    nil
  }

  private func configureNetwork() {
    guard bounds.width > 0, bounds.height > 0, let rootLayer = layer else { return }
    let scale = window?.backingScaleFactor ?? NSScreen.main?.backingScaleFactor ?? 2
    guard
      renderedSize != bounds.size
        || renderedScale != scale
        || renderedReduceMotion != reduceMotion
        || renderedContextNodes != contextNodes
        || renderedSegments != segments
    else { return }

    renderedSize = bounds.size
    renderedScale = scale
    renderedReduceMotion = reduceMotion
    renderedContextNodes = contextNodes
    renderedSegments = segments

    rootLayer.sublayers?.forEach { $0.removeFromSuperlayer() }
    for segment in segments {
      rootLayer.addSublayer(makeLineLayer(for: segment, scale: scale))
    }
    for (index, point) in contextNodes.enumerated() {
      rootLayer.addSublayer(makeNodeLayer(at: point, index: index, scale: scale))
    }
  }

  private func makeLineLayer(for segment: NetworkSegment, scale: CGFloat) -> CAShapeLayer {
    let line = CAShapeLayer()
    line.contentsScale = scale
    line.fillColor = nil
    line.strokeColor = strokeColour(for: segment.kind)
    line.lineWidth = segment.kind == .context ? 0.85 : 1.15
    line.lineCap = .round
    line.path = makePath(for: segment)

    guard !reduceMotion, let sourceID = segment.movingSourceID else { return line }
    let motion = OrbitMotion(sourceID: sourceID)
    let values: [CGPath] = (0...CoreAnimationTimeline.sampleCount).map { sample in
      let progress = Double(sample) / Double(CoreAnimationTimeline.sampleCount)
      return makePath(
        for: segment,
        sourceOffset: motion.translation(at: progress)
      )
    }
    line.path = values[0]

    let animation = CAKeyframeAnimation(keyPath: "path")
    animation.values = values
    animation.calculationMode = .linear
    animation.timingFunction = CAMediaTimingFunction(name: .linear)
    animation.preferredFrameRateRange = CoreAnimationTimeline.frameRateRange
    animation.duration = motion.duration
    animation.repeatCount = .infinity
    animation.isRemovedOnCompletion = false
    animation.fillMode = .both
    animation.beginTime = CoreAnimationTimeline.beginTime(for: line)
    line.add(animation, forKey: "opentrawl.attached-edge")
    return line
  }

  private func makeNodeLayer(at point: CGPoint, index: Int, scale: CGFloat) -> CALayer {
    let diameter: CGFloat = index.isMultiple(of: 5) ? 5 : 3.5
    let node = CALayer()
    node.contentsScale = scale
    node.bounds = CGRect(x: 0, y: 0, width: diameter, height: diameter)
    node.cornerRadius = diameter / 2
    node.position = point
    node.backgroundColor = NSColor.labelColor.withAlphaComponent(
      index.isMultiple(of: 5) ? 0.18 : 0.11
    ).cgColor
    return node
  }

  private func makePath(
    for segment: NetworkSegment,
    sourceOffset: CGVector = .zero
  ) -> CGPath {
    let points = segment.points(sourceOffset: sourceOffset)
    let path = CGMutablePath()
    path.move(to: points.start)
    path.addLine(to: points.end)
    return path
  }

  private func strokeColour(for kind: NetworkSegment.Kind) -> CGColor {
    switch kind {
    case .context:
      NSColor.labelColor.withAlphaComponent(0.10).cgColor
    case .source:
      NSColor.labelColor.withAlphaComponent(0.18).cgColor
    case .centre:
      NSColor(
        red: 0.902,
        green: 0.2,
        blue: 0.137,
        alpha: 0.24
      ).cgColor
    }
  }
}
