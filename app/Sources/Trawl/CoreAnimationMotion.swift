import AppKit
import QuartzCore
import SwiftUI
import TrawlCore

enum CoreAnimationTimeline {
  static let epoch = CACurrentMediaTime()
  static let sampleCount = 720

  static func beginTime(for layer: CALayer) -> CFTimeInterval {
    layer.convertTime(epoch, from: nil)
  }

  static var frameRateRange: CAFrameRateRange {
    CAFrameRateRange(minimum: 60, maximum: 120, preferred: 120)
  }

  static var elapsed: TimeInterval {
    CACurrentMediaTime() - epoch
  }
}

struct CoreAnimationNetwork: NSViewRepresentable {
  let centre: CGPoint
  let centreDiameter: CGFloat
  let visualScale: CGFloat
  let contextNodes: [CGPoint]
  let segments: [NetworkSegment]
  let activity: ConstellationActivity
  let trafficEvent: ConstellationTrafficEvent?
  let reduceMotion: Bool

  func makeNSView(context: Context) -> NetworkLayerView {
    let view = NetworkLayerView()
    view.update(
      centre: centre,
      centreDiameter: centreDiameter,
      visualScale: visualScale,
      contextNodes: contextNodes,
      segments: segments,
      activity: activity,
      trafficEvent: trafficEvent,
      reduceMotion: reduceMotion
    )
    return view
  }

  func updateNSView(_ view: NetworkLayerView, context: Context) {
    view.update(
      centre: centre,
      centreDiameter: centreDiameter,
      visualScale: visualScale,
      contextNodes: contextNodes,
      segments: segments,
      activity: activity,
      trafficEvent: trafficEvent,
      reduceMotion: reduceMotion
    )
  }
}

@MainActor
final class NetworkLayerView: NSView {
  private var centre = CGPoint.zero
  private var centreDiameter: CGFloat = TrawlDesign.centreSize
  private var visualScale: CGFloat = 1
  private var contextNodes: [CGPoint] = []
  private var segments: [NetworkSegment] = []
  private var activity = ConstellationActivity.idle
  private var trafficEvent: ConstellationTrafficEvent?
  private var reduceMotion = false
  private var renderedCentre = CGPoint.zero
  private var renderedCentreDiameter: CGFloat = 0
  private var renderedVisualScale: CGFloat = 0
  private var renderedContextNodes: [CGPoint] = []
  private var renderedSegments: [NetworkSegment] = []
  private var renderedActivity = ConstellationActivity.idle
  private var renderedTrafficEvent: ConstellationTrafficEvent?
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
    centre: CGPoint,
    centreDiameter: CGFloat,
    visualScale: CGFloat,
    contextNodes: [CGPoint],
    segments: [NetworkSegment],
    activity: ConstellationActivity,
    trafficEvent: ConstellationTrafficEvent?,
    reduceMotion: Bool
  ) {
    self.centre = centre
    self.centreDiameter = centreDiameter
    self.visualScale = visualScale
    self.contextNodes = contextNodes
    self.segments = segments
    self.activity = activity
    self.trafficEvent = trafficEvent
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
        || renderedCentre != centre
        || renderedCentreDiameter != centreDiameter
        || renderedVisualScale != visualScale
        || renderedContextNodes != contextNodes
        || renderedSegments != segments
        || renderedActivity != activity
        || renderedTrafficEvent != trafficEvent
    else { return }

    renderedSize = bounds.size
    renderedScale = scale
    renderedReduceMotion = reduceMotion
    renderedCentre = centre
    renderedCentreDiameter = centreDiameter
    renderedVisualScale = visualScale
    renderedContextNodes = contextNodes
    renderedSegments = segments
    renderedActivity = activity
    renderedTrafficEvent = trafficEvent

    rootLayer.sublayers?.forEach { $0.removeFromSuperlayer() }
    for segment in segments {
      rootLayer.addSublayer(makeLineLayer(for: segment, scale: scale))
    }
    for (index, point) in contextNodes.enumerated() {
      rootLayer.addSublayer(makeNodeLayer(at: point, index: index, scale: scale))
    }
    ConstellationTrafficRenderer(
      centre: centre,
      centreDiameter: centreDiameter,
      visualScale: visualScale,
      segments: segments,
      reduceMotion: reduceMotion,
      scale: scale
    ).addLayers(activity: activity, event: trafficEvent, to: rootLayer)
  }

  private func makeLineLayer(for segment: NetworkSegment, scale: CGFloat) -> CAShapeLayer {
    let line = CAShapeLayer()
    line.contentsScale = scale
    line.fillColor = nil
    line.strokeColor = strokeColour(for: segment.kind)
    line.lineWidth = (segment.kind == .context ? 0.85 : 1.15) * visualScale
    line.lineCap = .round
    line.path = makePath(for: segment)

    guard !reduceMotion, let sourceID = segment.movingSourceID else { return line }
    let motion = ConstellationMotion(sourceID: sourceID)
    let values: [CGPath] = (0...CoreAnimationTimeline.sampleCount).map { sample in
      let progress = Double(sample) / Double(CoreAnimationTimeline.sampleCount)
      return makePath(
        for: segment,
        sourceOffset: vector(motion.translation(at: progress))
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
    let diameter: CGFloat = (index.isMultiple(of: 5) ? 5 : 3.5) * visualScale
    let node = CALayer()
    node.contentsScale = scale
    node.bounds = CGRect(x: 0, y: 0, width: diameter, height: diameter)
    node.cornerRadius = diameter / 2
    node.position = point
    node.backgroundColor =
      NSColor.labelColor.withAlphaComponent(
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

  private func vector(_ value: ConstellationVector) -> CGVector {
    CGVector(dx: CGFloat(value.dx), dy: CGFloat(value.dy))
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
