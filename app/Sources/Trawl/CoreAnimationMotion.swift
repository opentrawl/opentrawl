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

private struct DirectedNetworkSegment {
  let segment: NetworkSegment
  let isForward: Bool
}

struct CoreAnimationNetwork: NSViewRepresentable {
  let centre: CGPoint
  let contextNodes: [CGPoint]
  let segments: [NetworkSegment]
  let activity: ConstellationActivity
  let reduceMotion: Bool

  func makeNSView(context: Context) -> NetworkLayerView {
    let view = NetworkLayerView()
    view.update(
      centre: centre,
      contextNodes: contextNodes,
      segments: segments,
      activity: activity,
      reduceMotion: reduceMotion
    )
    return view
  }

  func updateNSView(_ view: NetworkLayerView, context: Context) {
    view.update(
      centre: centre,
      contextNodes: contextNodes,
      segments: segments,
      activity: activity,
      reduceMotion: reduceMotion
    )
  }
}

@MainActor
final class NetworkLayerView: NSView {
  private var centre = CGPoint.zero
  private var contextNodes: [CGPoint] = []
  private var segments: [NetworkSegment] = []
  private var activity = ConstellationActivity.idle
  private var reduceMotion = false
  private var renderedCentre = CGPoint.zero
  private var renderedContextNodes: [CGPoint] = []
  private var renderedSegments: [NetworkSegment] = []
  private var renderedActivity = ConstellationActivity.idle
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
    contextNodes: [CGPoint],
    segments: [NetworkSegment],
    activity: ConstellationActivity,
    reduceMotion: Bool
  ) {
    self.centre = centre
    self.contextNodes = contextNodes
    self.segments = segments
    self.activity = activity
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
        || renderedContextNodes != contextNodes
        || renderedSegments != segments
        || renderedActivity != activity
    else { return }

    renderedSize = bounds.size
    renderedScale = scale
    renderedReduceMotion = reduceMotion
    renderedCentre = centre
    renderedContextNodes = contextNodes
    renderedSegments = segments
    renderedActivity = activity

    rootLayer.sublayers?.forEach { $0.removeFromSuperlayer() }
    for segment in segments {
      rootLayer.addSublayer(makeLineLayer(for: segment, scale: scale))
    }
    for (index, point) in contextNodes.enumerated() {
      rootLayer.addSublayer(makeNodeLayer(at: point, index: index, scale: scale))
    }
    addActivityLayers(to: rootLayer, scale: scale)
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

  private func vector(_ value: ConstellationVector) -> CGVector {
    CGVector(dx: CGFloat(value.dx), dy: CGFloat(value.dy))
  }

  private var sourceIDs: [String] {
    Array(Set(segments.compactMap(\.movingSourceID))).sorted()
  }

  private var trafficPlan: ConstellationTrafficPlan {
    ConstellationTrafficPlan(activity: activity, allSourceIDs: Set(sourceIDs))
  }

  private var requestedSourceIDs: [String] {
    trafficPlan.outboundSourceIDs.sorted()
  }

  private func addActivityLayers(to rootLayer: CALayer, scale: CGFloat) {
    if reduceMotion {
      addReducedMotionWorkState(to: rootLayer, scale: scale)
      return
    }

    for index in 0..<3 {
      guard let route = ambientRoute(index: index) else { continue }
      rootLayer.addSublayer(
        makePulseLayer(
          route: route,
          diameter: 3,
          opacity: 0.28,
          glow: 4,
          duration: ambientDuration(index: index),
          repeats: true,
          delay: 0,
          startElapsed: 0,
          scale: scale
        )
      )
    }

    guard case .idle = activity else {
      addWorkLayers(to: rootLayer, scale: scale)
      return
    }
  }

  private func addWorkLayers(to rootLayer: CALayer, scale: CGFloat) {
    let elapsed = CoreAnimationTimeline.elapsed
    var outboundDurations: [String: TimeInterval] = [:]
    for sourceID in requestedSourceIDs {
      guard let route = route(from: centreKey, to: sourceKey(sourceID)) else { continue }
      let duration = 1.2 * Double(route.count)
      outboundDurations[sourceID] = duration
      rootLayer.addSublayer(
        makePulseLayer(
          route: route,
          diameter: 5,
          opacity: 0.78,
          glow: 8,
          duration: duration,
          repeats: false,
          delay: 0,
          startElapsed: elapsed,
          scale: scale
        )
      )
    }

    for sourceID in trafficPlan.returningSourceIDs.sorted() {
      guard let route = route(from: sourceKey(sourceID), to: centreKey) else { continue }
      let delay = outboundDurations[sourceID] ?? 0
      rootLayer.addSublayer(
        makePulseLayer(
          route: route,
          diameter: 5,
          opacity: 0.78,
          glow: 8,
          duration: 1.2 * Double(route.count),
          repeats: false,
          delay: delay,
          startElapsed: elapsed + delay,
          scale: scale
        )
      )
    }
    for sourceID in trafficPlan.failedSourceIDs.sorted() {
      rootLayer.addSublayer(
        makeFailedEndpoint(
          for: sourceID,
          delay: outboundDurations[sourceID] ?? 0,
          scale: scale
        )
      )
    }
  }

  private func ambientRoute(index: Int) -> [DirectedNetworkSegment]? {
    guard let sourceID = ambientSourceID(index: index) else { return nil }
    guard let outbound = route(from: centreKey, to: sourceKey(sourceID)) else { return nil }
    return outbound + outbound.reversed().map {
      DirectedNetworkSegment(segment: $0.segment, isForward: !$0.isForward)
    }
  }

  private func ambientDuration(index: Int) -> TimeInterval {
    guard let sourceID = ambientSourceID(index: index) else { return 12 }
    return ConstellationMotion(sourceID: sourceID).duration
  }

  private func ambientSourceID(index: Int) -> String? {
    guard !sourceIDs.isEmpty else { return nil }
    let offset = Int((TrawlDesign.meshSeed >> UInt64(index * 8)) & 0xff)
    let stride = max(1, sourceIDs.count / 3)
    return sourceIDs[(offset + index * stride) % sourceIDs.count]
  }

  private var centreKey: String {
    pointKey(centre)
  }

  private func sourceKey(_ sourceID: String) -> String {
    "source:\(sourceID)"
  }

  private func pointKey(_ point: CGPoint) -> String {
    "point:\(Int((point.x * 100).rounded())):\(Int((point.y * 100).rounded()))"
  }

  private func endpointKey(_ endpoint: NetworkEndpoint) -> String {
    endpoint.sourceID.map(sourceKey) ?? pointKey(endpoint.anchor)
  }

  private func route(from start: String, to end: String) -> [DirectedNetworkSegment]? {
    guard start != end else { return [] }
    var connections: [String: [(String, DirectedNetworkSegment)]] = [:]
    for segment in segments {
      let startKey = endpointKey(segment.startEndpoint)
      let endKey = endpointKey(segment.endEndpoint)
      connections[startKey, default: []].append(
        (endKey, DirectedNetworkSegment(segment: segment, isForward: true))
      )
      connections[endKey, default: []].append(
        (startKey, DirectedNetworkSegment(segment: segment, isForward: false))
      )
    }

    var queue: [(key: String, route: [DirectedNetworkSegment])] = [(start, [])]
    var visited = Set([start])
    while !queue.isEmpty {
      let next = queue.removeFirst()
      for (neighbour, segment) in connections[next.key, default: []] where visited.insert(neighbour).inserted {
        let candidate = next.route + [segment]
        if neighbour == end { return candidate }
        queue.append((neighbour, candidate))
      }
    }
    return nil
  }

  private func makePulseLayer(
    route: [DirectedNetworkSegment],
    diameter: CGFloat,
    opacity: Float,
    glow: CGFloat,
    duration: TimeInterval,
    repeats: Bool,
    delay: TimeInterval,
    startElapsed: TimeInterval,
    scale: CGFloat
  ) -> CALayer {
    let pulse = CALayer()
    pulse.contentsScale = scale
    pulse.bounds = CGRect(x: 0, y: 0, width: diameter, height: diameter)
    pulse.cornerRadius = diameter / 2
    pulse.backgroundColor = NSColor(TrawlDesign.brandRed).withAlphaComponent(CGFloat(opacity)).cgColor
    pulse.shadowColor = NSColor(TrawlDesign.brandRed).cgColor
    pulse.shadowOpacity = opacity
    pulse.shadowRadius = glow
    pulse.shadowOffset = .zero

    let points = routePositions(route: route, startElapsed: startElapsed, duration: duration)
    pulse.position = points[0]
    let animation = CAKeyframeAnimation(keyPath: "position")
    animation.values = points.map { NSValue(point: $0) }
    animation.calculationMode = .linear
    animation.timingFunction = CAMediaTimingFunction(name: .linear)
    animation.preferredFrameRateRange = CoreAnimationTimeline.frameRateRange
    animation.duration = duration
    animation.repeatCount = repeats ? .infinity : 0
    animation.isRemovedOnCompletion = !repeats
    animation.fillMode = .both
    animation.beginTime = repeats
      ? CoreAnimationTimeline.beginTime(for: pulse)
      : pulse.convertTime(CACurrentMediaTime() + delay, from: nil)
    pulse.add(animation, forKey: repeats ? "opentrawl.ambient-photon" : "opentrawl.work-photon")
    return pulse
  }

  private func routePositions(
    route: [DirectedNetworkSegment],
    startElapsed: TimeInterval,
    duration: TimeInterval
  ) -> [CGPoint] {
    guard !route.isEmpty else { return [centre] }
    let sampleCount = max(24, route.count * 24)
    return (0...sampleCount).map { sample in
      let progress = Double(sample) / Double(sampleCount)
      let scaled = progress * Double(route.count)
      let index = min(Int(scaled), route.count - 1)
      let edgeProgress = scaled - Double(index)
      let elapsed = startElapsed + duration * progress
      let directed = route[index]
      let points = directedPoints(directed, elapsed: elapsed)
      return CGPoint(
        x: points.start.x + (points.end.x - points.start.x) * edgeProgress,
        y: points.start.y + (points.end.y - points.start.y) * edgeProgress
      )
    }
  }

  private func directedPoints(
    _ directed: DirectedNetworkSegment,
    elapsed: TimeInterval
  ) -> (start: CGPoint, end: CGPoint) {
    let sourceOffset = directed.segment.movingSourceID.map {
      vector(ConstellationMotion(sourceID: $0).translation(elapsed: elapsed))
    } ?? .zero
    let points = directed.segment.points(sourceOffset: sourceOffset)
    return directed.isForward ? points : (points.end, points.start)
  }

  private func addReducedMotionWorkState(to rootLayer: CALayer, scale: CGFloat) {
    guard case .idle = activity else {
      rootLayer.addSublayer(makeWorkOutline(at: centre, radius: TrawlDesign.centreSize / 2 + 5, scale: scale))
      for sourceID in requestedSourceIDs {
        guard let endpoint = sourceEndpoint(for: sourceID) else { continue }
        rootLayer.addSublayer(makeWorkOutline(at: endpoint.anchor, radius: endpoint.trimRadius + 5, scale: scale))
      }
      return
    }
  }

  private func sourceEndpoint(for sourceID: String) -> NetworkEndpoint? {
    segments.lazy.compactMap { segment in
      [segment.startEndpoint, segment.endEndpoint].first { $0.sourceID == sourceID }
    }.first
  }

  private func makeWorkOutline(at point: CGPoint, radius: CGFloat, scale: CGFloat) -> CAShapeLayer {
    let outline = CAShapeLayer()
    outline.contentsScale = scale
    outline.fillColor = nil
    outline.strokeColor = NSColor(TrawlDesign.brandRed).cgColor
    outline.lineWidth = 2
    outline.path = CGPath(
      ellipseIn: CGRect(x: point.x - radius, y: point.y - radius, width: radius * 2, height: radius * 2),
      transform: nil
    )
    return outline
  }

  private func makeFailedEndpoint(
    for sourceID: String,
    delay: TimeInterval,
    scale: CGFloat
  ) -> CALayer {
    let endpoint = sourceEndpoint(for: sourceID) ?? NetworkEndpoint(anchor: centre, trimRadius: 0, sourceID: nil)
    let node = CALayer()
    node.contentsScale = scale
    node.bounds = CGRect(x: 0, y: 0, width: 2, height: 2)
    node.cornerRadius = 1
    node.position = endpoint.anchor
    node.backgroundColor = NSColor(TrawlDesign.brandRed).cgColor
    node.opacity = 0

    let fade = CAKeyframeAnimation(keyPath: "opacity")
    fade.values = [0, 1, 1, 0]
    fade.keyTimes = [0, 0.08, 0.92, 1]
    fade.duration = 2
    fade.isRemovedOnCompletion = true
    fade.beginTime = node.convertTime(CACurrentMediaTime() + delay, from: nil)
    node.add(fade, forKey: "opentrawl.failed-endpoint")

    if endpoint.sourceID != nil {
      let motion = ConstellationMotion(sourceID: sourceID)
      let elapsed = CoreAnimationTimeline.elapsed + delay
      let positions = (0...120).map { sample in
        let progress = Double(sample) / 120
        let offset = vector(motion.translation(elapsed: elapsed + 2 * progress))
        return NSValue(point: endpoint.point(offset: offset))
      }
      let position = CAKeyframeAnimation(keyPath: "position")
      position.values = positions
      position.calculationMode = .linear
      position.timingFunction = CAMediaTimingFunction(name: .linear)
      position.preferredFrameRateRange = CoreAnimationTimeline.frameRateRange
      position.duration = 2
      position.beginTime = node.convertTime(CACurrentMediaTime() + delay, from: nil)
      node.add(position, forKey: "opentrawl.failed-endpoint-position")
    }
    return node
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
