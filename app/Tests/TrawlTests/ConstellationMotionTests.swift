import Foundation
import QuartzCore
import Testing

@testable import Trawl
@testable import TrawlClient
@testable import TrawlCore

@Test func sourceAndAttachedEndpointUseTheSameUneditedSample() {
  let sourceID = "telegram"
  let sourceAnchor = ConstellationPoint(x: 244, y: 318)
  let endpointAnchor = ConstellationPoint(x: 244, y: 318)
  let phases: [Double] = [0, 0.125, 0.25, 0.5, 0.75, 1]
  let motion = ConstellationMotion(sourceID: sourceID)

  print(
    "CONSTELLATION_INPUT sourceID=\(sourceID) sourceAnchor=\(sourceAnchor) endpointAnchor=\(endpointAnchor) phases=\(phases)"
  )

  let samples = phases.map { phase in
    let translation = motion.translation(at: phase)
    return (
      sourceID: sourceID,
      phase: phase,
      source: sourceAnchor.translated(by: translation),
      endpoint: endpointAnchor.translated(by: translation),
      translation: translation
    )
  }

  print("CONSTELLATION_OUTPUT samples=\(samples)")

  #expect(samples.count == phases.count)
  for sample in samples {
    #expect(sample.source == sample.endpoint)
    #expect(sample.translation.dx >= -20 && sample.translation.dx <= 20)
    #expect(sample.translation.dy >= -14 && sample.translation.dy <= 14)
  }
}

@Test func motionIsDeterministicAndUsesThePromisedBounds() {
  let sourceIDs = [
    "calendar", "contacts", "gmail", "imessage", "notes", "photos", "telegram", "twitter",
    "whatsapp",
  ]
  let phases: [Double] = [0, 0.25, 0.5, 0.75, 1]

  print("CONSTELLATION_INPUT sourceIDs=\(sourceIDs) phases=\(phases)")

  for sourceID in sourceIDs {
    let first = ConstellationMotion(sourceID: sourceID)
    let second = ConstellationMotion(sourceID: sourceID)
    #expect(first == second)
    #expect(first.horizontalAmplitude >= 12 && first.horizontalAmplitude <= 20)
    #expect(first.verticalAmplitude >= 8 && first.verticalAmplitude <= 14)
    #expect(first.duration >= 10 && first.duration <= 14)

    for phase in phases {
      let phaseTranslation = first.translation(at: phase)
      let elapsedTranslation = first.translation(elapsed: first.duration * phase)
      #expect(abs(phaseTranslation.dx - elapsedTranslation.dx) < 0.000_000_000_001)
      #expect(abs(phaseTranslation.dy - elapsedTranslation.dy) < 0.000_000_000_001)
    }
    print(
      "CONSTELLATION_OUTPUT motion=\(first) samples=\(phases.map { first.translation(at: $0) })")
  }
}

@Test func layoutsStayBalancedAndInsideSafeBoundsForEverySupportedCount() {
  let counts = [6, 9]
  let sizes = [
    ConstellationPoint(x: 704, y: 504),
    ConstellationPoint(x: 744, y: 531),
    ConstellationPoint(x: 784, y: 558),
    ConstellationPoint(x: 824, y: 585),
    ConstellationPoint(x: 864, y: 612),
    ConstellationPoint(x: 904, y: 639),
    ConstellationPoint(x: 944, y: 666),
    ConstellationPoint(x: 984, y: 693),
    ConstellationPoint(x: 1_024, y: 720),
    ConstellationPoint(x: 2_200, y: 900),
  ]

  for count in counts {
    let sourceIDs = (1...count).map { String(format: "synthetic-%02d", $0) }
    var expectedOrbitOrder: [String]?
    var expectedPolarIdentity: [String: (angle: Double, radius: Double)] = [:]
    var previousMetrics: ConstellationLayoutMetrics?
    var previousExtents: [String: (x: Double, y: Double)] = [:]

    for size in sizes {
      let centre = ConstellationPoint(x: size.x / 2, y: size.y / 2 - min(27, size.y * 0.035))
      let metrics = ConstellationLayoutMetrics.forSourceCount(count, fitting: size)
      let layout = ConstellationOrbitLayout(
        sourceIDs: sourceIDs,
        size: size,
        centre: centre,
        metrics: metrics
      )
      let result = layout.placementResult()
      let placements = result.placements

      let canvas = ConstellationRect(x: 0, y: 0, width: size.x, height: size.y)
      let diamond = ConstellationRect(
        x: centre.x - metrics.diamondClearanceRadius,
        y: centre.y - metrics.diamondClearanceRadius,
        width: metrics.diamondClearanceRadius * 2,
        height: metrics.diamondClearanceRadius * 2
      )
      #expect(placements.count == count)
      #expect(Set(placements.map(\.anchor)).count == count)
      let orbitOrder = placements.sorted {
        atan2($0.anchor.y - centre.y, $0.anchor.x - centre.x)
          < atan2($1.anchor.y - centre.y, $1.anchor.x - centre.x)
      }.map(\.id)
      if let expectedOrbitOrder {
        #expect(orbitOrder == expectedOrbitOrder)
      } else {
        expectedOrbitOrder = orbitOrder
      }
      for placement in placements {
        #expect(canvas.contains(placement.hostRect))
        #expect(canvas.contains(placement.labelRect))
        #expect(!placement.hostRect.expanded(by: metrics.spacing).intersects(diamond))
        let polar = normalisedOrbitIdentity(
          for: placement,
          centre: centre,
          size: size,
          metrics: metrics
        )
        if let expected = expectedPolarIdentity[placement.id] {
          #expect(abs(polar.angle - expected.angle) < 0.000_000_001)
          #expect(abs(polar.radius - expected.radius) < 0.000_000_001)
        } else {
          expectedPolarIdentity[placement.id] = polar
        }
        let extent = (x: abs(placement.anchor.x - centre.x), y: abs(placement.anchor.y - centre.y))
        if let previous = previousExtents[placement.id] {
          #expect(extent.x >= previous.x)
          #expect(extent.y >= previous.y)
        }
        previousExtents[placement.id] = extent
      }
      for left in placements.indices {
        for right in placements.indices.dropFirst(left + 1) {
          #expect(!placements[left].labelRect.intersects(placements[right].labelRect))
        }
      }

      let angles = placements.map { atan2($0.anchor.y - centre.y, $0.anchor.x - centre.x) }
        .sorted()
      let wrappedAngles = Array(angles.dropFirst()) + [angles[0] + 2 * .pi]
      let angleGaps = zip(angles, wrappedAngles).map { $1 - $0 }
      #expect((angleGaps.max() ?? 0) - (angleGaps.min() ?? 0) >= 0.001)
      if size.x / size.y > 1.45 {
        let horizontalExtent = placements.map { abs($0.anchor.x - centre.x) }.max() ?? 0
        let verticalExtent = placements.map { abs($0.anchor.y - centre.y) }.max() ?? 1
        #expect(horizontalExtent / verticalExtent <= 1.7)
      }
      if let previousMetrics {
        #expect(metrics.labelWidth >= previousMetrics.labelWidth)
        #expect(metrics.labelHeight >= previousMetrics.labelHeight)
        #expect(metrics.minimumIconDiameter >= previousMetrics.minimumIconDiameter)
      }
      previousMetrics = metrics
    }
  }
}

@Test func graphTopologyStaysConnectedAndLoopedDuringResize() throws {
  let sources = try restingSources(count: 9)
  let sizes = [
    CGSize(width: 704, height: 504),
    CGSize(width: 824, height: 585),
    CGSize(width: 1_024, height: 720),
    CGSize(width: 2_200, height: 900),
  ]
  var expectedEdges: Set<[Int]>?

  for size in sizes {
    let snapshot = ConstellationLayout(size: size, sources: sources).snapshot()
    let points = snapshot.sources.map(\.anchor) + [snapshot.centre] + snapshot.contextNodes
    let indices = Dictionary(uniqueKeysWithValues: zip(points, points.indices))
    let edges = Set(
      snapshot.segments.map { segment in
        [indices[segment.startEndpoint.anchor]!, indices[segment.endEndpoint.anchor]!].sorted()
      })
    let visited = connectedIndices(startingAt: 0, edges: edges)

    #expect(visited.count == points.count)
    #expect(edges.count >= points.count)
    #expect(snapshot.segments.filter { $0.kind == .source }.count == sources.count)
    #expect(snapshot.segments.filter { $0.kind == .centre }.count == 4)
    if let expectedEdges {
      #expect(edges == expectedEdges)
    } else {
      expectedEdges = edges
    }
  }
}

@Test func graphSegmentsNeverProperlyIntersectDuringResizeOrSourceMotion() throws {
  let orderedSources = try restingSources(count: 9)
  let sources = [
    orderedSources[4], orderedSources[0], orderedSources[7],
    orderedSources[2], orderedSources[8], orderedSources[5],
    orderedSources[1], orderedSources[6], orderedSources[3],
  ]
  let sizes = [
    CGSize(width: 704, height: 504),
    CGSize(width: 824, height: 585),
    CGSize(width: 1_024, height: 720),
    CGSize(width: 2_200, height: 900),
  ]
  let phases: [Double] = [0, 0.125, 0.25, 0.5, 0.75]

  for size in sizes {
    let segments = ConstellationLayout(size: size, sources: sources).snapshot().segments
    for phase in phases {
      let paths = segments.map { segment in
        segment.points(
          sourceOffset: segment.movingSourceID.map {
            let translation = ConstellationMotion(sourceID: $0).translation(at: phase)
            return CGVector(dx: translation.dx, dy: translation.dy)
          } ?? .zero
        )
      }
      for index in paths.indices {
        for otherIndex in paths.indices.dropFirst(index + 1) {
          #expect(!properlyIntersects(paths[index], paths[otherIndex]))
        }
      }
    }
  }
}

private func properlyIntersects(
  _ first: (start: CGPoint, end: CGPoint),
  _ second: (start: CGPoint, end: CGPoint)
) -> Bool {
  let sharedEndpoint = [first.start, first.end].contains { firstPoint in
    [second.start, second.end].contains { secondPoint in
      hypot(firstPoint.x - secondPoint.x, firstPoint.y - secondPoint.y) < 0.01
    }
  }
  guard !sharedEndpoint else { return false }
  func orientation(_ first: CGPoint, _ second: CGPoint, _ third: CGPoint) -> CGFloat {
    (second.x - first.x) * (third.y - first.y) - (second.y - first.y) * (third.x - first.x)
  }
  let firstStart = orientation(first.start, first.end, second.start)
  let firstEnd = orientation(first.start, first.end, second.end)
  let secondStart = orientation(second.start, second.end, first.start)
  let secondEnd = orientation(second.start, second.end, first.end)
  return firstStart * firstEnd < 0 && secondStart * secondEnd < 0
}

private func normalisedOrbitIdentity(
  for placement: ConstellationPlacement,
  centre: ConstellationPoint,
  size: ConstellationPoint,
  metrics: ConstellationLayoutMetrics
) -> (angle: Double, radius: Double) {
  let availableHorizontalRadius = min(centre.x, size.x - centre.x) - metrics.hostSize.x / 2
  let activeHorizontalRadius = size.x / size.y > 1.45 ? size.y * 0.68 : availableHorizontalRadius
  let horizontalRadius = min(availableHorizontalRadius, activeHorizontalRadius)
  let minimumAnchorY = metrics.hostSize.y / 2 - metrics.hostCentreYOffset
  let maximumAnchorY = min(
    size.y - metrics.hostSize.y / 2 - metrics.hostCentreYOffset,
    size.y - metrics.labelTop - metrics.labelHeight
  )
  let verticalRadius = min(centre.y - minimumAnchorY, maximumAnchorY - centre.y)
  let x = (placement.anchor.x - centre.x) / horizontalRadius
  let y = (placement.anchor.y - centre.y) / verticalRadius
  return (angle: atan2(y, x), radius: hypot(x, y))
}

private func connectedIndices(startingAt start: Int, edges: Set<[Int]>) -> Set<Int> {
  var visited: Set<Int> = [start]
  var pending = [start]
  while let current = pending.popLast() {
    for edge in edges where edge.contains(current) {
      let neighbour = edge[0] == current ? edge[1] : edge[0]
      if visited.insert(neighbour).inserted {
        pending.append(neighbour)
      }
    }
  }
  return visited
}

private func restingSources(count: Int) throws -> [RestingSource] {
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .complete
  response.sources = (1...count).map { index in
    var source = Trawl_Federation_V1_SourceStatus()
    source.manifest.sourceID = String(format: "source-%02d", index)
    source.manifest.displayName = "Source \(index)"
    source.state = "ok"
    return source
  }
  return SourceRestingCopy.sources(
    from: try response.model().sources, failures: [], skippedSources: [])
}

@Test func actionLabelsNeverOverlapAcrossFullMotionAtMinimumSize() {
  let sourceIDs = [
    "calendar", "contacts", "gmail", "imessage", "notes", "photos", "telegram", "twitter",
    "whatsapp",
  ]
  let size = ConstellationPoint(x: 744, y: 644)
  let centre = ConstellationPoint(x: size.x / 2, y: size.y / 2 - min(27, size.y * 0.035))
  let metrics = ConstellationLayoutMetrics.forSourceCount(sourceIDs.count, fitting: size)
  let placements = ConstellationOrbitLayout(
    sourceIDs: sourceIDs,
    size: size,
    centre: centre,
    metrics: metrics
  ).placements()
  let phases = (0...CoreAnimationTimeline.sampleCount).map {
    Double($0) / Double(CoreAnimationTimeline.sampleCount)
  }
  let renderedLabels = placements.map { placement in
    let bounds = phases.map {
      placement.labelRect.translated(
        by: ConstellationMotion(sourceID: placement.id).translation(at: $0))
    }
    return (sourceID: placement.id, envelope: bounds.envelope)
  }

  print("CONSTELLATION_INPUT size=\(size) sourceIDs=\(sourceIDs) phases=\(phases.count)")
  print("CONSTELLATION_OUTPUT placements=\(placements) actionLabelEnvelopes=\(renderedLabels)")

  #expect(placements.count == sourceIDs.count)
  for left in renderedLabels.indices {
    for right in renderedLabels.indices.dropFirst(left + 1) {
      let overlap = renderedLabels[left].envelope.intersects(renderedLabels[right].envelope)
      if overlap {
        print(
          "CONSTELLATION_LABEL_OVERLAP left=\(renderedLabels[left].sourceID) right=\(renderedLabels[right].sourceID)"
        )
      }
      #expect(!overlap)
    }
  }
}

@Test func activityPreservesTheCompleteUntouchedInputMeaning() {
  let allSources: Set<String> = ["calendar", "gmail", "photos"]
  let usefulGmail = ConstellationTrafficEvent(
    requestedSourceIDs: ["gmail"],
    usefulSourceIDs: ["gmail"],
    failedSourceIDs: []
  )
  let mixedSync = ConstellationTrafficEvent(
    requestedSourceIDs: allSources,
    usefulSourceIDs: ["calendar", "gmail", "photos"],
    failedSourceIDs: ["photos"]
  )
  let inputs: [ConstellationActivity] = [
    .idle,
    .searching(sourceID: nil),
    .searching(sourceID: "gmail"),
    .syncing(sourceIDs: allSources),
    .failed(sourceIDs: ["photos"]),
  ]
  let outputs = inputs.map { ConstellationTrafficPlan(activity: $0, allSourceIDs: allSources) }
  let usefulPlan = ConstellationTrafficPlan(event: usefulGmail, allSourceIDs: allSources)
  let mixedPlan = ConstellationTrafficPlan(event: mixedSync, allSourceIDs: allSources)

  print("CONSTELLATION_INPUT activities=\(inputs) events=\([usefulGmail, mixedSync])")
  print("CONSTELLATION_OUTPUT activityPlans=\(outputs) eventPlans=\([usefulPlan, mixedPlan])")

  #expect(outputs[0].outboundSourceIDs.isEmpty)
  #expect(outputs[1].outboundSourceIDs == allSources)
  #expect(outputs[2].outboundSourceIDs == Set(["gmail"]))
  #expect(outputs[3].outboundSourceIDs == allSources)
  #expect(outputs[4].failedSourceIDs == Set(["photos"]))
  #expect(usefulPlan.outboundSourceIDs.isEmpty)
  #expect(usefulPlan.returningSourceIDs == Set(["gmail"]))
  #expect(mixedPlan.outboundSourceIDs.isEmpty)
  #expect(mixedPlan.returningSourceIDs == Set(["calendar", "gmail"]))
  #expect(mixedPlan.failedSourceIDs == Set(["photos"]))
  #expect(!inputs[0].isWorkInProgress)
  #expect(inputs[1].isWorkInProgress)
  #expect(inputs[2].isWorkInProgress)
  #expect(inputs[3].isWorkInProgress)
  #expect(!inputs[4].isWorkInProgress)
}

@Test func responseFailureWinsAndReducedMotionAffectsOnlyEventSources() {
  let allSources: Set<String> = ["calendar", "gmail", "photos"]
  let event = ConstellationTrafficEvent(
    requestedSourceIDs: ["gmail", "photos"],
    usefulSourceIDs: ["calendar", "gmail", "photos"],
    failedSourceIDs: ["photos"]
  )
  let plan = ConstellationTrafficPlan(event: event, allSourceIDs: allSources)

  print("CONSTELLATION_INPUT allSources=\(allSources) event=\(event)")
  print("CONSTELLATION_OUTPUT responsePlan=\(plan) affected=\(plan.affectedSourceIDs)")

  #expect(plan.outboundSourceIDs.isEmpty)
  #expect(plan.returningSourceIDs == Set(["gmail"]))
  #expect(plan.failedSourceIDs == Set(["photos"]))
  #expect(plan.affectedSourceIDs == Set(["gmail", "photos"]))
}

@Test func delayedResponsePulseIsHiddenUntilItsBeginTime() {
  let timing = ConstellationPulseTiming(delay: 0.12)
  let samples: [TimeInterval] = [0, 0.119, 0.12, 0.5]
  let output = samples.map { timing.isVisible(elapsed: $0) }

  print("CONSTELLATION_INPUT timing=\(timing) elapsed=\(samples)")
  print("CONSTELLATION_OUTPUT visible=\(output)")

  #expect(output == [false, false, true, true])
}

@Test func ambientPulseAndMovingRouteShareTheEpochElapsedSample() {
  let timing = ConstellationPulseTiming(delay: 0)
  let currentElapsed: TimeInterval = 37.25
  let ambientStart = timing.routeSampleStartElapsed(
    currentElapsed: currentElapsed,
    repeatsFromSharedEpoch: true
  )
  let workStart = timing.routeSampleStartElapsed(
    currentElapsed: currentElapsed,
    repeatsFromSharedEpoch: false
  )

  print("CONSTELLATION_INPUT timing=\(timing) currentElapsed=\(currentElapsed)")
  print("CONSTELLATION_OUTPUT ambientStart=\(ambientStart) workStart=\(workStart)")

  #expect(ambientStart == 0)
  #expect(workStart == currentElapsed)
}

@MainActor
@Test func ambientTrafficKeepsThreeRestrainedPhotonsAtSourceMotionSpeed() {
  let centre = CGPoint(x: 200, y: 200)
  let sourceIDs = ["calendar", "gmail", "photos"]
  let segments = sourceIDs.enumerated().map { index, sourceID in
    NetworkSegment(
      startEndpoint: NetworkEndpoint(anchor: centre, trimRadius: 20, sourceID: nil),
      endEndpoint: NetworkEndpoint(
        anchor: CGPoint(x: 80 + index * 120, y: 80),
        trimRadius: 20,
        sourceID: sourceID
      ),
      kind: .centre
    )
  }
  let rootLayer = CALayer()

  ConstellationTrafficRenderer(
    centre: centre,
    centreDiameter: TrawlDesign.centreSize,
    visualScale: 1,
    segments: segments,
    reduceMotion: false,
    scale: 2
  ).addLayers(activity: .idle, event: nil, to: rootLayer)

  let photons = rootLayer.sublayers ?? []
  let sourceDurations = Set(sourceIDs.map { ConstellationMotion(sourceID: $0).duration })
  print("CONSTELLATION_INPUT sourceIDs=\(sourceIDs) activity=idle")
  print("CONSTELLATION_OUTPUT photons=\(photons)")

  #expect(photons.count == 3)
  for photon in photons {
    let animation =
      photon.animation(forKey: "opentrawl.ambient-photon")
      as? CAKeyframeAnimation
    #expect(photon.bounds.size == CGSize(width: 3, height: 3))
    #expect(photon.opacity == 0.48)
    #expect(photon.shadowOpacity == 0.48)
    #expect(photon.shadowRadius == 4)
    #expect(animation?.repeatCount == .infinity)
    #expect(animation.map { sourceDurations.contains($0.duration) } == true)
  }
}

@Test func reduceMotionKeepsTheCompleteStaticPosition() {
  let sourceID = "photos"
  let phases: [Double] = [0, 0.25, 0.5, 0.75, 1]
  let motion = ConstellationMotion(sourceID: sourceID)
  let outputs = phases.map { motion.translation(at: $0, reduceMotion: true) }

  print("CONSTELLATION_INPUT sourceID=\(sourceID) reduceMotion=true phases=\(phases)")
  print("CONSTELLATION_OUTPUT translations=\(outputs)")
  #expect(outputs.allSatisfy { $0 == .zero })
}

extension ConstellationRect {
  fileprivate func translated(by vector: ConstellationVector) -> Self {
    Self(x: x + vector.dx, y: y + vector.dy, width: width, height: height)
  }
}

extension [ConstellationRect] {
  fileprivate var envelope: ConstellationRect {
    let minimumX = map(\.x).min() ?? 0
    let minimumY = map(\.y).min() ?? 0
    let maximumX = map(\.maxX).max() ?? 0
    let maximumY = map(\.maxY).max() ?? 0
    return ConstellationRect(
      x: minimumX,
      y: minimumY,
      width: maximumX - minimumX,
      height: maximumY - minimumY
    )
  }
}
