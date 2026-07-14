import AppKit
import QuartzCore
import TrawlCore

private struct DirectedNetworkSegment {
  let segment: NetworkSegment
  let isForward: Bool
}

@MainActor
struct ConstellationTrafficRenderer {
  let centre: CGPoint
  let centreDiameter: CGFloat
  let segments: [NetworkSegment]
  let reduceMotion: Bool
  let scale: CGFloat

  private var sourceIDs: Set<String> {
    Set(segments.compactMap(\.movingSourceID))
  }

  func addLayers(
    activity: ConstellationActivity,
    event: ConstellationTrafficEvent?,
    to rootLayer: CALayer
  ) {
    let activityPlan = ConstellationTrafficPlan(activity: activity, allSourceIDs: sourceIDs)
    let eventPlan = event.map { ConstellationTrafficPlan(event: $0, allSourceIDs: sourceIDs) }

    if reduceMotion {
      addReducedMotionLayers(activityPlan: activityPlan, eventPlan: eventPlan, to: rootLayer)
      return
    }

    addAmbientLayers(to: rootLayer)
    for sourceID in activityPlan.outboundSourceIDs.sorted() {
      guard let route = route(from: centreKey, to: sourceKey(sourceID)) else { continue }
      rootLayer.addSublayer(makePulseLayer(route: route, duration: 1.2 * Double(route.count)))
    }
    guard let eventPlan else { return }
    for sourceID in eventPlan.returningSourceIDs.sorted() {
      guard let route = route(from: sourceKey(sourceID), to: centreKey) else { continue }
      rootLayer.addSublayer(
        makePulseLayer(route: route, duration: 1.2 * Double(route.count), delay: 0.12)
      )
    }
    for sourceID in eventPlan.failedSourceIDs.sorted() {
      rootLayer.addSublayer(makeFailedEndpoint(for: sourceID, delay: 0.12))
    }
  }

  private func addAmbientLayers(to rootLayer: CALayer) {
    for index in 0..<3 {
      guard let sourceID = ambientSourceID(index: index) else { continue }
      guard let outbound = route(from: centreKey, to: sourceKey(sourceID)) else { continue }
      let route =
        outbound
        + outbound.reversed().map {
          DirectedNetworkSegment(segment: $0.segment, isForward: !$0.isForward)
        }
      rootLayer.addSublayer(
        makePulseLayer(
          route: route,
          diameter: 3,
          opacity: 0.48,
          glow: 4,
          duration: ConstellationMotion(sourceID: sourceID).duration,
          repeats: true
        )
      )
    }
  }

  private func addReducedMotionLayers(
    activityPlan: ConstellationTrafficPlan,
    eventPlan: ConstellationTrafficPlan?,
    to rootLayer: CALayer
  ) {
    let affected = eventPlan?.affectedSourceIDs ?? activityPlan.affectedSourceIDs
    guard !affected.isEmpty else { return }
    rootLayer.addSublayer(makeOutline(at: centre, radius: centreDiameter / 2 + 5))
    for sourceID in affected.sorted() {
      guard let endpoint = sourceEndpoint(for: sourceID) else { continue }
      rootLayer.addSublayer(makeOutline(at: endpoint.anchor, radius: endpoint.trimRadius + 5))
    }
  }

  private func makePulseLayer(
    route: [DirectedNetworkSegment],
    diameter: CGFloat = 5,
    opacity: Float = 0.78,
    glow: CGFloat = 8,
    duration: TimeInterval,
    repeats: Bool = false,
    delay: TimeInterval = 0
  ) -> CALayer {
    let pulse = CALayer()
    pulse.contentsScale = scale
    pulse.bounds = CGRect(x: 0, y: 0, width: diameter, height: diameter)
    pulse.cornerRadius = diameter / 2
    pulse.backgroundColor =
      NSColor(TrawlDesign.brandRed).withAlphaComponent(CGFloat(opacity)).cgColor
    pulse.shadowColor = NSColor(TrawlDesign.brandRed).cgColor
    pulse.shadowOpacity = opacity
    pulse.shadowRadius = glow
    pulse.shadowOffset = .zero

    let now = CoreAnimationTimeline.elapsed
    let timing = ConstellationPulseTiming(delay: delay)
    let sampleStart = timing.routeSampleStartElapsed(
      currentElapsed: now,
      repeatsFromSharedEpoch: repeats
    )
    let points = routePositions(route: route, startElapsed: sampleStart, duration: duration)
    pulse.position = points.last ?? centre
    pulse.opacity = repeats ? opacity : 0

    let position = CAKeyframeAnimation(keyPath: "position")
    position.values = points.map { NSValue(point: $0) }
    position.calculationMode = .linear
    position.timingFunction = CAMediaTimingFunction(name: .linear)
    position.preferredFrameRateRange = CoreAnimationTimeline.frameRateRange
    position.duration = duration
    position.repeatCount = repeats ? .infinity : 0
    position.isRemovedOnCompletion = !repeats
    position.fillMode = .forwards
    position.beginTime =
      repeats
      ? CoreAnimationTimeline.beginTime(for: pulse)
      : pulse.convertTime(CACurrentMediaTime() + timing.delay, from: nil)
    pulse.add(position, forKey: repeats ? "opentrawl.ambient-photon" : "opentrawl.work-photon")

    if !repeats {
      let visibility = CAKeyframeAnimation(keyPath: "opacity")
      visibility.values = [opacity, opacity, 0]
      visibility.keyTimes = [0, 0.96, 1]
      visibility.duration = duration
      visibility.beginTime = position.beginTime
      visibility.fillMode = .forwards
      visibility.isRemovedOnCompletion = true
      pulse.add(visibility, forKey: "opentrawl.work-photon-visibility")
    }
    return pulse
  }

  private func makeFailedEndpoint(for sourceID: String, delay: TimeInterval) -> CALayer {
    guard let endpoint = sourceEndpoint(for: sourceID) else { return CALayer() }
    let node = CALayer()
    node.contentsScale = scale
    node.bounds = CGRect(x: 0, y: 0, width: 2, height: 2)
    node.cornerRadius = 1
    node.position = endpoint.anchor
    node.backgroundColor = NSColor(TrawlDesign.brandRed).cgColor
    node.opacity = 0

    let fade = CAKeyframeAnimation(keyPath: "opacity")
    fade.values = [1, 1, 0]
    fade.keyTimes = [0, 0.92, 1]
    fade.duration = 2
    fade.fillMode = .forwards
    fade.isRemovedOnCompletion = true
    fade.beginTime = node.convertTime(CACurrentMediaTime() + delay, from: nil)
    node.add(fade, forKey: "opentrawl.failed-endpoint")

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
    position.preferredFrameRateRange = CoreAnimationTimeline.frameRateRange
    position.duration = 2
    position.beginTime = fade.beginTime
    position.fillMode = .forwards
    node.add(position, forKey: "opentrawl.failed-endpoint-position")
    return node
  }

  private func makeOutline(at point: CGPoint, radius: CGFloat) -> CAShapeLayer {
    let outline = CAShapeLayer()
    outline.contentsScale = scale
    outline.fillColor = nil
    outline.strokeColor = NSColor(TrawlDesign.brandRed).cgColor
    outline.lineWidth = 2
    outline.path = CGPath(
      ellipseIn: CGRect(
        x: point.x - radius, y: point.y - radius, width: radius * 2, height: radius * 2),
      transform: nil
    )
    return outline
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
      let points = directedPoints(route[index], elapsed: elapsed)
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
    let offset =
      directed.segment.movingSourceID.map {
        vector(ConstellationMotion(sourceID: $0).translation(elapsed: elapsed))
      } ?? .zero
    let points = directed.segment.points(sourceOffset: offset)
    return directed.isForward ? points : (points.end, points.start)
  }

  private var centreKey: String { pointKey(centre) }

  private func sourceKey(_ sourceID: String) -> String { "source:\(sourceID)" }

  private func pointKey(_ point: CGPoint) -> String {
    "point:\(Int((point.x * 100).rounded())):\(Int((point.y * 100).rounded()))"
  }

  private func endpointKey(_ endpoint: NetworkEndpoint) -> String {
    endpoint.sourceID.map(sourceKey) ?? pointKey(endpoint.anchor)
  }

  private func route(from start: String, to end: String) -> [DirectedNetworkSegment]? {
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
    var queue: [(String, [DirectedNetworkSegment])] = [(start, [])]
    var visited = Set([start])
    while !queue.isEmpty {
      let next = queue.removeFirst()
      for (neighbour, segment) in connections[next.0, default: []]
      where visited.insert(neighbour).inserted {
        let candidate = next.1 + [segment]
        if neighbour == end { return candidate }
        queue.append((neighbour, candidate))
      }
    }
    return nil
  }

  private func sourceEndpoint(for sourceID: String) -> NetworkEndpoint? {
    segments.lazy.compactMap { segment in
      [segment.startEndpoint, segment.endEndpoint].first { $0.sourceID == sourceID }
    }.first
  }

  private func ambientSourceID(index: Int) -> String? {
    let ordered = sourceIDs.sorted()
    guard !ordered.isEmpty else { return nil }
    let offset = Int((TrawlDesign.meshSeed >> UInt64(index * 8)) & 0xff)
    let stride = max(1, ordered.count / 3)
    return ordered[(offset + index * stride) % ordered.count]
  }

  private func vector(_ value: ConstellationVector) -> CGVector {
    CGVector(dx: CGFloat(value.dx), dy: CGFloat(value.dy))
  }
}
