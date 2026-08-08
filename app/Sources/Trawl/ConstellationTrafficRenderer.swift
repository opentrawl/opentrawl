import AppKit
import QuartzCore
import TrawlCore

@MainActor
struct ConstellationTrafficRenderer {
  let centre: CGPoint
  let centreDiameter: CGFloat
  let visualScale: CGFloat
  let segments: [NetworkSegment]
  let reduceMotion: Bool
  let scale: CGFloat

  private var sourceIDs: Set<String> {
    Set(segments.compactMap(\.movingSourceID))
  }

  func addSearchAndAmbientTrafficLayers(
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

    let topology = ConstellationNetworkTopology(segments: segments)
    guard let centreNode = topology.nodeIdentity(atAnchor: centre) else { return }
    addAmbientLayers(topology: topology, centreNode: centreNode, to: rootLayer)
    for sourceID in activityPlan.outboundSourceIDs.sorted() {
      guard let route = topology.shortestRoute(from: centreNode, to: .source(sourceID))
      else { continue }
      rootLayer.addSublayer(makePulseLayer(route: route, duration: 1.2 * Double(route.count)))
    }
    guard let eventPlan else { return }
    for sourceID in eventPlan.returningSourceIDs.sorted() {
      guard let route = topology.shortestRoute(from: .source(sourceID), to: centreNode)
      else { continue }
      rootLayer.addSublayer(
        makePulseLayer(route: route, duration: 1.2 * Double(route.count), delay: 0.12)
      )
    }
    for sourceID in eventPlan.failedSourceIDs.sorted() {
      rootLayer.addSublayer(makeFailedEndpoint(for: sourceID, delay: 0.12))
    }
  }

  private func addAmbientLayers(
    topology: ConstellationNetworkTopology,
    centreNode: ConstellationNetworkNodeIdentity,
    to rootLayer: CALayer
  ) {
    for index in 0..<3 {
      guard let sourceID = ambientSourceID(index: index) else { continue }
      guard let outbound = topology.shortestRoute(from: centreNode, to: .source(sourceID))
      else { continue }
      let route = outbound + outbound.reversed().map(\.reversedTravelDirection)
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
    rootLayer.addSublayer(makeOutline(at: centre, radius: centreDiameter / 2 + scaled(5)))
    for sourceID in affected.sorted() {
      guard let endpoint = sourceEndpoint(for: sourceID) else { continue }
      rootLayer.addSublayer(makeOutline(at: endpoint.anchor, radius: endpoint.trimRadius + scaled(5)))
    }
  }

  private func makePulseLayer(
    route: [ConstellationDirectedNetworkSegment],
    diameter: CGFloat = 5,
    opacity: Float = 0.78,
    glow: CGFloat = 8,
    duration: TimeInterval,
    repeats: Bool = false,
    delay: TimeInterval = 0
  ) -> CALayer {
    let pulse = CALayer()
    pulse.contentsScale = scale
    let scaledDiameter = scaled(diameter)
    pulse.bounds = CGRect(x: 0, y: 0, width: scaledDiameter, height: scaledDiameter)
    pulse.cornerRadius = scaledDiameter / 2
    pulse.backgroundColor =
      NSColor(TrawlDesign.brandRed).withAlphaComponent(CGFloat(opacity)).cgColor
    pulse.shadowColor = NSColor(TrawlDesign.brandRed).cgColor
    pulse.shadowOpacity = opacity
    pulse.shadowRadius = scaled(glow)
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
    let diameter = scaled(2)
    node.bounds = CGRect(x: 0, y: 0, width: diameter, height: diameter)
    node.cornerRadius = diameter / 2
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
    outline.lineWidth = scaled(2)
    outline.path = CGPath(
      ellipseIn: CGRect(
        x: point.x - radius, y: point.y - radius, width: radius * 2, height: radius * 2),
      transform: nil
    )
    return outline
  }

  private func routePositions(
    route: [ConstellationDirectedNetworkSegment],
    startElapsed: TimeInterval,
    duration: TimeInterval
  ) -> [CGPoint] {
    guard !route.isEmpty else { return [centre] }
    let sampleCount = max(24, route.count * 24)
    return (0...sampleCount).map { sample in
      let progress = Double(sample) / Double(sampleCount)
      let scaledProgress = progress * Double(route.count)
      let index = min(Int(scaledProgress), route.count - 1)
      let edgeProgress = scaledProgress - Double(index)
      let elapsed = startElapsed + duration * progress
      let travelPoints = route[index].travelPoints(elapsed: elapsed)
      return CGPoint(
        x: travelPoints.departure.x
          + (travelPoints.arrival.x - travelPoints.departure.x) * edgeProgress,
        y: travelPoints.departure.y
          + (travelPoints.arrival.y - travelPoints.departure.y) * edgeProgress
      )
    }
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

  private func scaled(_ value: CGFloat) -> CGFloat {
    value * visualScale
  }
}
