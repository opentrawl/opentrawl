import AppKit
import QuartzCore
import TrawlCore

private enum ArchiveUpdateTrafficPalette {
  static let electricYellowCore = NSColor(red: 1.0, green: 0.84, blue: 0.22, alpha: 1)
  static let electricAmberGlow = NSColor(red: 1.0, green: 0.72, blue: 0.08, alpha: 1)
}

/// Deterministic pseudo-random stream so every photon's path is stable from
/// rebuild to rebuild but different from every other photon's.
private struct SplitMix64 {
  private var state: UInt64

  init(seed: UInt64) {
    state = seed
  }

  mutating func unitValue() -> Double {
    state &+= 0x9e37_79b9_7f4a_7c15
    var value = state
    value = (value ^ (value >> 30)) &* 0xbf58_476d_1ce4_e5b9
    value = (value ^ (value >> 27)) &* 0x94d0_49bb_1331_11eb
    value ^= value >> 31
    return Double(value) / Double(UInt64.max)
  }
}

/// One photon journey from an updating app to the centre along its own
/// sampled walk.
private struct PhotonTraversal {
  let travelledSegments: [ConstellationDirectedNetworkSegment]
  let travelledPathLength: Double
}

/// One photon's crossing of a static mesh edge, in fractions of that
/// photon's cycle, together with the photon's phase so the crossing can be
/// placed on the shared phase-zero timeline.
private struct StaticEdgeCrossingWindow {
  let photonLocalStartFraction: Double
  let photonLocalEndFraction: Double
  let photonPhaseOffsetFraction: Double
}

/// Renders the archive-update traffic: while an app's archive updates, yellow
/// photons haul its data home to the centre, each along its own mostly-direct
/// random walk through the mesh, so the net around the app carries spread-out
/// living traffic. Mesh edges glow exactly while a photon crosses them.
/// ConstellationTrafficRenderer keeps owning search traffic, the red idle
/// ambience and the reduce-motion outline treatment.
@MainActor
struct ConstellationArchiveUpdateTrafficRenderer {
  let centre: CGPoint
  let visualScale: CGFloat
  let segments: [NetworkSegment]
  let scale: CGFloat

  /// How strongly a photon's random walk is pulled towards the centre, and
  /// how far it may wander before the walk is completed by the shortest
  /// route.
  private static let centreProgressBiasSharpness = 4.0
  private static let maximumDetourHopsMultiplier = 1.6

  /// Adds the archive-update traffic for the updating sources. A source's
  /// traffic simply stops when it leaves the updating set; the activity does
  /// not prove success, so no finishing flourish is drawn. Returns true when
  /// it owns the update traffic so the caller keeps the default red update
  /// pulses out of this render pass. Under reduce motion it draws nothing
  /// and the caller's static outline treatment applies unchanged.
  func addArchiveUpdateTrafficLayers(
    activity: ConstellationActivity,
    reduceMotion: Bool,
    to rootLayer: CALayer
  ) -> Bool {
    guard !reduceMotion else { return false }
    guard case .updating(let updatingSourceIDs) = activity, !updatingSourceIDs.isEmpty else {
      return false
    }
    let topology = ConstellationNetworkTopology(segments: segments)
    guard let centreNode = topology.nodeIdentity(atAnchor: centre) else { return false }
    for sourceID in updatingSourceIDs.sorted() {
      addUpdatingSourcePhotonStream(
        sourceID: sourceID,
        topology: topology,
        centreNode: centreNode,
        to: rootLayer
      )
      addSourceEdgePersistentTintLayers(sourceID: sourceID, to: rootLayer)
    }
    return true
  }

  // MARK: - Photon streams

  /// Six photons per updating app, each cycling through sampled random walks
  /// home at constant speed. A photon's repeat cycle spans exactly one orbit
  /// period of its app node, and the moving-edge geometry is sampled in
  /// orbit time shifted by the photon's own phase offset, so the launch
  /// segment tracks the orbiting mesh on every repeat:
  /// ((t + phaseOffset) mod orbitPeriod) - phaseOffset is congruent to t.
  /// Static mesh edges get one glow layer per unique travelled edge for the
  /// whole source, carrying the combined opacity timeline of every photon
  /// crossing.
  private func addUpdatingSourcePhotonStream(
    sourceID: String,
    topology: ConstellationNetworkTopology,
    centreNode: ConstellationNetworkNodeIdentity,
    to rootLayer: CALayer
  ) {
    let sourceNode = ConstellationNetworkNodeIdentity.source(sourceID)
    guard let directRoute = topology.shortestRoute(from: sourceNode, to: centreNode) else {
      return
    }
    let directRouteLength = routeLength(of: directRoute)
    guard directRouteLength > 1 else { return }

    let photonCount = 6
    let orbitPeriod = ConstellationMotion(sourceID: sourceID).duration
    var crossingsByStaticEdge: [(segment: NetworkSegment, crossings: [StaticEdgeCrossingWindow])] =
      []
    for photonIndex in 0..<photonCount {
      let photonSeed = "\(sourceID):update-photon:\(photonIndex)"
      var random = SplitMix64(seed: deterministicHash(photonSeed))
      let baseTravelDuration = 1.1 + unitRandom(photonSeed + ":speed") * 0.8
      let pointsPerSecond = directRouteLength / baseTravelDuration
      var traversals: [PhotonTraversal] = []
      var plannedPathLength = 0.0
      while plannedPathLength / pointsPerSecond < orbitPeriod {
        traversals.append(
          sampleRandomWalkTraversal(
            from: sourceNode,
            topology: topology,
            centreNode: centreNode,
            directHopCount: directRoute.count,
            firstStepSpreadIndex: photonIndex + traversals.count,
            random: &random
          )
        )
        plannedPathLength = traversals.reduce(0) { $0 + $1.travelledPathLength }
      }
      let cycleDuration = orbitPeriod
      let phaseOffset =
        (Double(photonIndex) + unitRandom(photonSeed + ":phase") * 0.8)
        / Double(photonCount) * cycleDuration
      let headDiameter = 3.2 + unitRandom(photonSeed + ":size") * 2.8

      let composite = compositePhotonCycle(
        traversals: traversals,
        cycleDuration: cycleDuration,
        startElapsed: -phaseOffset
      )
      let trailAlphas = [1.0, 0.55, 0.34, 0.18]
      for trailIndex in 0...3 {
        rootLayer.addSublayer(
          makeRepeatingPhotonLayer(
            positions: composite.positions,
            positionKeyTimes: composite.positionKeyTimes,
            visibilityPeakAlpha: trailAlphas[trailIndex],
            visibilityKeyPoints: composite.traversalTimeWindows,
            diameter: headDiameter * (1 - Double(trailIndex) * 0.18),
            colour: ArchiveUpdateTrafficPalette.electricYellowCore,
            glowRadius: trailIndex == 0 ? 8 : 0,
            cycleDuration: cycleDuration,
            phaseOffset: positiveTimeOffset(
              phaseOffset - Double(trailIndex) * 0.05,
              period: cycleDuration
            )
          )
        )
      }
      collectStaticEdgeCrossings(
        traversals: traversals,
        photonPhaseOffsetFraction: phaseOffset / cycleDuration,
        into: &crossingsByStaticEdge
      )
    }
    addTraversedEdgeGlowLayers(
      crossingsByStaticEdge: crossingsByStaticEdge,
      orbitPeriod: orbitPeriod,
      to: rootLayer
    )
  }

  /// One mostly-direct random walk from the app's node to the centre.
  /// Immediate backtracking is forbidden, visited nodes are avoided, and
  /// when the walk runs out of moves or hops it is completed by the shortest
  /// route. The caller has already proven the source connects to the centre,
  /// so every walk reaches it and every traversal is non-empty.
  private func sampleRandomWalkTraversal(
    from sourceNode: ConstellationNetworkNodeIdentity,
    topology: ConstellationNetworkTopology,
    centreNode: ConstellationNetworkNodeIdentity,
    directHopCount: Int,
    firstStepSpreadIndex: Int,
    random: inout SplitMix64
  ) -> PhotonTraversal {
    let maximumHops = max(
      directHopCount + 1,
      Int(Double(directHopCount) * Self.maximumDetourHopsMultiplier)
    )

    var travelledSegments: [ConstellationDirectedNetworkSegment] = []
    var currentNode = sourceNode
    var currentAnchor = topology.anchor(of: sourceNode)
    var visitedNodes: Set<ConstellationNetworkNodeIdentity> = [sourceNode]
    var lastTraversedSegment: NetworkSegment?

    while currentNode != centreNode {
      if travelledSegments.count >= maximumHops {
        appendShortestRouteHome(
          from: currentNode,
          topology: topology,
          centreNode: centreNode,
          to: &travelledSegments
        )
        break
      }
      let steppableConnections = topology.connections(from: currentNode)
        .filter { connection in
          if let lastTraversedSegment,
            connection.directedSegment.segment == lastTraversedSegment
          {
            return false
          }
          return connection.neighbour == centreNode
            || !visitedNodes.contains(connection.neighbour)
        }
      guard !steppableConnections.isEmpty else {
        appendShortestRouteHome(
          from: currentNode,
          topology: topology,
          centreNode: centreNode,
          to: &travelledSegments
        )
        break
      }

      let chosenConnection: ConstellationNetworkTopology.Connection
      if travelledSegments.isEmpty {
        chosenConnection = steppableConnections[firstStepSpreadIndex % steppableConnections.count]
      } else {
        chosenConnection = centreBiasedWeightedConnection(
          steppableConnections,
          currentAnchor: currentAnchor,
          random: &random
        )
      }
      travelledSegments.append(chosenConnection.directedSegment)
      lastTraversedSegment = chosenConnection.directedSegment.segment
      visitedNodes.insert(chosenConnection.neighbour)
      currentNode = chosenConnection.neighbour
      currentAnchor = chosenConnection.neighbourAnchor
    }
    return PhotonTraversal(
      travelledSegments: travelledSegments,
      travelledPathLength: routeLength(of: travelledSegments)
    )
  }

  private func centreBiasedWeightedConnection(
    _ connections: [ConstellationNetworkTopology.Connection],
    currentAnchor: CGPoint,
    random: inout SplitMix64
  ) -> ConstellationNetworkTopology.Connection {
    let currentDistance = distance(currentAnchor, centre)
    let selectionWeights = connections.map { connection in
      let edgeLength = max(distance(currentAnchor, connection.neighbourAnchor), 1)
      let centreProgressScore =
        (currentDistance - distance(connection.neighbourAnchor, centre)) / edgeLength
      return exp(Self.centreProgressBiasSharpness * Double(centreProgressScore))
    }
    let totalWeight = selectionWeights.reduce(0, +)
    var remainingWeight = random.unitValue() * totalWeight
    for (index, selectionWeight) in selectionWeights.enumerated() {
      remainingWeight -= selectionWeight
      if remainingWeight <= 0 { return connections[index] }
    }
    return connections[connections.count - 1]
  }

  private func appendShortestRouteHome(
    from node: ConstellationNetworkNodeIdentity,
    topology: ConstellationNetworkTopology,
    centreNode: ConstellationNetworkNodeIdentity,
    to travelledSegments: inout [ConstellationDirectedNetworkSegment]
  ) {
    guard node != centreNode else { return }
    guard let remainder = topology.shortestRoute(from: node, to: centreNode) else {
      preconditionFailure(
        "the direct-route guard proved this walk's region connects to the centre"
      )
    }
    travelledSegments.append(contentsOf: remainder)
  }

  /// Concatenated constant-speed samples across a photon's traversals, with
  /// key times proportional to distance and per-traversal visibility windows.
  /// The caller's connectivity guard guarantees non-empty traversals.
  private func compositePhotonCycle(
    traversals: [PhotonTraversal],
    cycleDuration: TimeInterval,
    startElapsed: TimeInterval
  ) -> (
    positions: [CGPoint],
    positionKeyTimes: [NSNumber],
    traversalTimeWindows: [(start: Double, end: Double)]
  ) {
    precondition(
      !traversals.isEmpty && traversals.allSatisfy { !$0.travelledSegments.isEmpty },
      "photon traversals must be non-empty connected walks"
    )
    let totalLength = traversals.reduce(0) { $0 + $1.travelledPathLength }
    var positions: [CGPoint] = []
    var cumulativeFractions: [Double] = []
    var traversalTimeWindows: [(start: Double, end: Double)] = []
    var lengthBefore = 0.0
    for traversal in traversals {
      let traversalStartFraction = lengthBefore / totalLength
      var lengthWithin = 0.0
      for directedSegment in traversal.travelledSegments {
        let edgeLength = edgeBaseLength(directedSegment)
        let samplesPerEdge = 12
        for sample in 0...samplesPerEdge {
          let edgeProgress = Double(sample) / Double(samplesPerEdge)
          let fraction =
            (lengthBefore + lengthWithin + edgeLength * edgeProgress) / totalLength
          let elapsed = startElapsed + cycleDuration * fraction
          let travelPoints = directedSegment.travelPoints(elapsed: elapsed)
          positions.append(
            CGPoint(
              x: travelPoints.departure.x
                + (travelPoints.arrival.x - travelPoints.departure.x) * CGFloat(edgeProgress),
              y: travelPoints.departure.y
                + (travelPoints.arrival.y - travelPoints.departure.y) * CGFloat(edgeProgress)
            )
          )
          cumulativeFractions.append(fraction)
        }
        lengthWithin += edgeLength
      }
      lengthBefore += traversal.travelledPathLength
      traversalTimeWindows.append((start: traversalStartFraction, end: lengthBefore / totalLength))
    }
    cumulativeFractions = cumulativeFractions.map { min(max($0, 0), 1) }
    cumulativeFractions[0] = 0
    cumulativeFractions[cumulativeFractions.count - 1] = 1
    var lastWindow = traversalTimeWindows[traversalTimeWindows.count - 1]
    lastWindow.end = 1
    traversalTimeWindows[traversalTimeWindows.count - 1] = lastWindow
    return (
      positions: positions,
      positionKeyTimes: cumulativeFractions.map { NSNumber(value: $0) },
      traversalTimeWindows: traversalTimeWindows
    )
  }

  /// Records when this photon crosses each static mesh edge, in fractions of
  /// its cycle. Edges attached to the moving app node are excluded; the
  /// persistent source tint lights those.
  private func collectStaticEdgeCrossings(
    traversals: [PhotonTraversal],
    photonPhaseOffsetFraction: Double,
    into crossingsByStaticEdge: inout [(
      segment: NetworkSegment, crossings: [StaticEdgeCrossingWindow]
    )]
  ) {
    let totalLength = traversals.reduce(0) { $0 + $1.travelledPathLength }
    var lengthBefore = 0.0
    for traversal in traversals {
      var lengthWithin = 0.0
      for directedSegment in traversal.travelledSegments {
        let edgeLength = edgeBaseLength(directedSegment)
        let crossingStart = (lengthBefore + lengthWithin) / totalLength
        let crossingEnd = (lengthBefore + lengthWithin + edgeLength) / totalLength
        lengthWithin += edgeLength
        guard directedSegment.segment.movingSourceID == nil else { continue }
        let crossing = StaticEdgeCrossingWindow(
          photonLocalStartFraction: crossingStart,
          photonLocalEndFraction: crossingEnd,
          photonPhaseOffsetFraction: photonPhaseOffsetFraction
        )
        if let existingIndex = crossingsByStaticEdge.firstIndex(where: {
          $0.segment == directedSegment.segment
        }) {
          crossingsByStaticEdge[existingIndex].crossings.append(crossing)
        } else {
          crossingsByStaticEdge.append((directedSegment.segment, [crossing]))
        }
      }
      lengthBefore += traversal.travelledPathLength
    }
  }

  /// One glow layer per unique travelled static edge for the whole source.
  /// Its opacity timeline is the combined envelope of every photon crossing,
  /// evaluated on the shared phase-zero orbit-period timeline.
  private func addTraversedEdgeGlowLayers(
    crossingsByStaticEdge: [(segment: NetworkSegment, crossings: [StaticEdgeCrossingWindow])],
    orbitPeriod: TimeInterval,
    to rootLayer: CALayer
  ) {
    let envelopeSampleCount = 144
    let peakOpacity = 0.5
    let rampInFraction = 0.015
    let fadeTailFraction = min(0.3 / orbitPeriod, 0.2)
    for (segment, crossings) in crossingsByStaticEdge {
      let glow = CAShapeLayer()
      glow.contentsScale = scale
      glow.fillColor = nil
      glow.strokeColor =
        ArchiveUpdateTrafficPalette.electricYellowCore.withAlphaComponent(0.9).cgColor
      glow.lineWidth = ((segment.kind == .context ? 0.85 : 1.15) + 0.9) * visualScale
      glow.lineCap = .round
      glow.path = makeSegmentPath(segment: segment)
      glow.opacity = 0

      let combinedEnvelope = (0..<envelopeSampleCount).map { sampleIndex -> Double in
        let sharedTimelineFraction = Double(sampleIndex) / Double(envelopeSampleCount)
        return crossings.reduce(0) { strongest, crossing in
          let photonLocalFraction =
            (sharedTimelineFraction + crossing.photonPhaseOffsetFraction)
            .truncatingRemainder(dividingBy: 1)
          let rampedValue = crossingOpacity(
            atPhotonLocalFraction: photonLocalFraction,
            crossing: crossing,
            peakOpacity: peakOpacity,
            rampInFraction: rampInFraction,
            fadeTailFraction: fadeTailFraction
          )
          return max(strongest, rampedValue)
        }
      }
      let usage = CAKeyframeAnimation(keyPath: "opacity")
      usage.values = combinedEnvelope + [combinedEnvelope[0]]
      usage.duration = orbitPeriod
      lockToSharedEpoch(usage, on: glow, phaseOffset: 0)
      glow.add(usage, forKey: "opentrawl.update-edge-usage-glow")
      rootLayer.addSublayer(glow)
    }
  }

  /// Trapezoid opacity for one crossing: quick ramp in as the photon enters
  /// the edge, hold while it crosses, fade after it leaves. Evaluated with
  /// wrap-around so windows spanning the cycle boundary stay continuous.
  private func crossingOpacity(
    atPhotonLocalFraction photonLocalFraction: Double,
    crossing: StaticEdgeCrossingWindow,
    peakOpacity: Double,
    rampInFraction: Double,
    fadeTailFraction: Double
  ) -> Double {
    let rampStart = crossing.photonLocalStartFraction - 0.01
    let holdStart = crossing.photonLocalStartFraction + rampInFraction
    let holdEnd = crossing.photonLocalEndFraction
    let fadeEnd = holdEnd + fadeTailFraction
    for wrapShift in [-1.0, 0, 1.0] {
      let evaluated = photonLocalFraction + wrapShift
      if evaluated >= rampStart && evaluated < holdStart {
        return peakOpacity * (evaluated - rampStart) / max(holdStart - rampStart, 0.0001)
      }
      if evaluated >= holdStart && evaluated <= holdEnd {
        return peakOpacity
      }
      if evaluated > holdEnd && evaluated <= fadeEnd {
        return peakOpacity * (1 - (evaluated - holdEnd) / max(fadeEnd - holdEnd, 0.0001))
      }
    }
    return 0
  }

  /// The 1-2 edges attached to an updating app's own moving node carry every
  /// one of its photons, so they get one persistent faint tint that follows
  /// the node's orbit exactly.
  private func addSourceEdgePersistentTintLayers(sourceID: String, to rootLayer: CALayer) {
    for segment in segments where segment.movingSourceID == sourceID {
      let tint = CAShapeLayer()
      tint.contentsScale = scale
      tint.fillColor = nil
      tint.strokeColor =
        ArchiveUpdateTrafficPalette.electricYellowCore.withAlphaComponent(0.55).cgColor
      tint.lineWidth = 1.8 * visualScale
      tint.lineCap = .round
      tint.shadowColor = ArchiveUpdateTrafficPalette.electricAmberGlow.cgColor
      tint.shadowOpacity = 0.45
      tint.shadowRadius = scaled(3)
      tint.shadowOffset = .zero
      configureSegmentPath(on: tint, segment: segment)
      tint.opacity = 0
      let shimmerPeriod = 1.5
      let shimmer = CAKeyframeAnimation(keyPath: "opacity")
      shimmer.values = [0.45, 1, 0.45]
      shimmer.keyTimes = [0, 0.5, 1]
      shimmer.duration = shimmerPeriod
      lockToSharedEpoch(
        shimmer,
        on: tint,
        phaseOffset: unitRandom("\(sourceID):source-tint") * shimmerPeriod
      )
      tint.add(shimmer, forKey: "opentrawl.update-source-tint")
      rootLayer.addSublayer(tint)
    }
  }

  // MARK: - Photon layer builders

  private func makeRepeatingPhotonLayer(
    positions: [CGPoint],
    positionKeyTimes: [NSNumber],
    visibilityPeakAlpha: Double,
    visibilityKeyPoints: [(start: Double, end: Double)],
    diameter: Double,
    colour: NSColor,
    glowRadius: CGFloat,
    cycleDuration: TimeInterval,
    phaseOffset: TimeInterval
  ) -> CALayer {
    let photon = CALayer()
    photon.contentsScale = scale
    let scaledDiameter = scaled(CGFloat(diameter))
    photon.bounds = CGRect(x: 0, y: 0, width: scaledDiameter, height: scaledDiameter)
    photon.cornerRadius = scaledDiameter / 2
    photon.backgroundColor = colour.withAlphaComponent(CGFloat(visibilityPeakAlpha)).cgColor
    if glowRadius > 0 {
      photon.shadowColor = ArchiveUpdateTrafficPalette.electricAmberGlow.cgColor
      photon.shadowOpacity = Float(visibilityPeakAlpha)
      photon.shadowRadius = scaled(glowRadius)
      photon.shadowOffset = .zero
    }
    photon.position = positions[0]
    photon.opacity = 0

    let position = CAKeyframeAnimation(keyPath: "position")
    position.values = positions.map { NSValue(point: $0) }
    position.keyTimes = positionKeyTimes
    position.calculationMode = .linear
    position.preferredFrameRateRange = CoreAnimationTimeline.frameRateRange
    position.duration = cycleDuration
    lockToSharedEpoch(position, on: photon, phaseOffset: phaseOffset)
    photon.add(position, forKey: "opentrawl.update-photon")

    var visibilityValues: [Double] = []
    var visibilityTimes: [Double] = []
    for window in visibilityKeyPoints {
      let windowSpan = max(window.end - window.start, 0.01)
      visibilityValues.append(contentsOf: [0, visibilityPeakAlpha, visibilityPeakAlpha, 0])
      visibilityTimes.append(contentsOf: [
        window.start,
        min(window.start + windowSpan * 0.1, 1),
        max(window.end - windowSpan * 0.08, 0),
        window.end,
      ])
    }
    visibilityTimes = visibilityTimes.map { min(max($0, 0), 1) }
    visibilityTimes[0] = 0
    visibilityTimes[visibilityTimes.count - 1] = 1
    let visibility = CAKeyframeAnimation(keyPath: "opacity")
    visibility.values = visibilityValues
    visibility.keyTimes = visibilityTimes.map { NSNumber(value: $0) }
    visibility.duration = cycleDuration
    lockToSharedEpoch(visibility, on: photon, phaseOffset: phaseOffset)
    photon.add(visibility, forKey: "opentrawl.update-photon-visibility")
    return photon
  }

  // MARK: - Shared geometry and timing

  private func routeLength(of route: [ConstellationDirectedNetworkSegment]) -> Double {
    route.reduce(0) { $0 + edgeBaseLength($1) }
  }

  /// Trimmed endpoints can collapse a very short edge; the floor keeps the
  /// constant-speed division meaningful.
  private func edgeBaseLength(_ directedSegment: ConstellationDirectedNetworkSegment) -> Double {
    let points = directedSegment.segment.points()
    return Double(max(distance(points.start, points.end), 1))
  }

  private func distance(_ lhs: CGPoint, _ rhs: CGPoint) -> CGFloat {
    hypot(lhs.x - rhs.x, lhs.y - rhs.y)
  }

  private func lockToSharedEpoch(
    _ animation: CAAnimation,
    on layer: CALayer,
    phaseOffset: TimeInterval
  ) {
    animation.repeatCount = .infinity
    animation.isRemovedOnCompletion = false
    animation.fillMode = .both
    animation.beginTime = CoreAnimationTimeline.beginTime(for: layer)
    animation.timeOffset = phaseOffset
  }

  private func positiveTimeOffset(_ value: TimeInterval, period: TimeInterval) -> TimeInterval {
    let remainder = value.truncatingRemainder(dividingBy: period)
    return remainder < 0 ? remainder + period : remainder
  }

  private func configureSegmentPath(on line: CAShapeLayer, segment: NetworkSegment) {
    guard let movingSourceID = segment.movingSourceID else {
      line.path = makeSegmentPath(segment: segment)
      return
    }
    let motion = ConstellationMotion(sourceID: movingSourceID)
    let pathValues: [CGPath] = (0...CoreAnimationTimeline.sampleCount).map { sample in
      let phase = Double(sample) / Double(CoreAnimationTimeline.sampleCount)
      return makeSegmentPath(segment: segment, sourceOffset: vector(motion.translation(at: phase)))
    }
    line.path = pathValues[0]
    let pathAnimation = CAKeyframeAnimation(keyPath: "path")
    pathAnimation.values = pathValues
    pathAnimation.calculationMode = .linear
    pathAnimation.timingFunction = CAMediaTimingFunction(name: .linear)
    pathAnimation.preferredFrameRateRange = CoreAnimationTimeline.frameRateRange
    pathAnimation.duration = motion.duration
    lockToSharedEpoch(pathAnimation, on: line, phaseOffset: 0)
    line.add(pathAnimation, forKey: "opentrawl.update-attached-edge")
  }

  private func makeSegmentPath(
    segment: NetworkSegment,
    sourceOffset: CGVector = .zero
  ) -> CGPath {
    let points = segment.points(sourceOffset: sourceOffset)
    let path = CGMutablePath()
    path.move(to: points.start)
    path.addLine(to: points.end)
    return path
  }

  private func deterministicHash(_ seedText: String) -> UInt64 {
    seedText.utf8.reduce(0xcbf2_9ce4_8422_2325) { partial, byte in
      (partial ^ UInt64(byte)) &* 0x100_0000_01b3
    }
  }

  private func unitRandom(_ seedText: String) -> Double {
    Double(deterministicHash(seedText) & 0xffff) / Double(UInt16.max)
  }

  private func vector(_ value: ConstellationVector) -> CGVector {
    CGVector(dx: CGFloat(value.dx), dy: CGFloat(value.dy))
  }

  private func scaled(_ value: CGFloat) -> CGFloat {
    value * visualScale
  }
}
