import Foundation
import QuartzCore
import Testing

@testable import Trawl
@testable import TrawlCore

@Test func sourceAndAttachedEndpointUseTheSameUneditedSample() {
  let sourceID = "telegram"
  let sourceAnchor = ConstellationPoint(x: 244, y: 318)
  let endpointAnchor = ConstellationPoint(x: 244, y: 318)
  let phases: [Double] = [0, 0.125, 0.25, 0.5, 0.75, 1]
  let motion = ConstellationMotion(sourceID: sourceID)

  print("CONSTELLATION_INPUT sourceID=\(sourceID) sourceAnchor=\(sourceAnchor) endpointAnchor=\(endpointAnchor) phases=\(phases)")

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
  let sourceIDs = ["calendar", "contacts", "gmail", "imessage", "notes", "photos", "telegram", "twitter", "whatsapp"]
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
    print("CONSTELLATION_OUTPUT motion=\(first) samples=\(phases.map { first.translation(at: $0) })")
  }
}

@Test func layoutsStayBalancedAndInsideSafeBoundsForEverySupportedCount() {
  let counts = [6, 9, 12, 16, 20]
  let sizes = [
    ConstellationPoint(x: 744, y: 644),
  ]

  for size in sizes {
    let centre = ConstellationPoint(x: size.x / 2, y: size.y / 2 - min(27, size.y * 0.035))
    for count in counts {
      let sourceIDs = (1...count).map { String(format: "synthetic-%02d", $0) }
      let metrics = ConstellationLayoutMetrics.forSourceCount(count)
      print("CONSTELLATION_INPUT size=\(size) centre=\(centre) sourceIDs=\(sourceIDs) metrics=\(metrics)")
      let layout = ConstellationOrbitLayout(
        sourceIDs: sourceIDs,
        size: size,
        centre: centre,
        metrics: metrics
      )
      let result = layout.placementResult()
      let placements = result.placements
      print("CONSTELLATION_OUTPUT size=\(size) count=\(count) result=\(result)")

      let canvas = ConstellationRect(x: 0, y: 0, width: size.x, height: size.y)
      let diamond = ConstellationRect(
        x: centre.x - metrics.diamondClearanceRadius,
        y: centre.y - metrics.diamondClearanceRadius,
        width: metrics.diamondClearanceRadius * 2,
        height: metrics.diamondClearanceRadius * 2
      )
      #expect(placements.count == count)
      #expect(Set(placements.map(\.anchor)).count == count)
      for placement in placements {
        #expect(canvas.contains(placement.hostRect))
        #expect(canvas.contains(placement.labelRect))
        #expect(!placement.hostRect.expanded(by: metrics.spacing).intersects(diamond))
        #expect(placement.hostRect.contains(placement.labelRect))
      }
      for left in placements.indices {
        for right in placements.indices.dropFirst(left + 1) {
          #expect(
            !placements[left].hostRect.expanded(by: metrics.spacing / 2)
              .intersects(placements[right].hostRect.expanded(by: metrics.spacing / 2))
          )
          #expect(!placements[left].labelRect.intersects(placements[right].labelRect))
        }
      }

      let radii = placements.map { $0.anchor.distance(to: centre) }
      let angles = placements.map { atan2($0.anchor.y - centre.y, $0.anchor.x - centre.x) }
        .sorted()
      let wrappedAngles = Array(angles.dropFirst()) + [angles[0] + 2 * .pi]
      let angleGaps = zip(angles, wrappedAngles).map { $1 - $0 }
      #expect(Set(radii.map { Int($0 / 20) }).count >= 3)
      #expect((radii.max() ?? 0) - (radii.min() ?? 0) >= 80)
      #expect((angleGaps.max() ?? 0) - (angleGaps.min() ?? 0) >= 0.08)
    }
  }
}

@Test func actionLabelsNeverOverlapAcrossFullMotionAtMinimumSize() {
  let sourceIDs = [
    "calendar", "contacts", "gmail", "imessage", "notes", "photos", "telegram", "twitter",
    "whatsapp",
  ]
  let size = ConstellationPoint(x: 744, y: 644)
  let centre = ConstellationPoint(x: size.x / 2, y: size.y / 2 - min(27, size.y * 0.035))
  let metrics = ConstellationLayoutMetrics.forSourceCount(sourceIDs.count)
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
      placement.labelRect.translated(by: ConstellationMotion(sourceID: placement.id).translation(at: $0))
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
    let animation = photon.animation(forKey: "opentrawl.ambient-photon")
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

private extension ConstellationRect {
  func translated(by vector: ConstellationVector) -> Self {
    Self(x: x + vector.dx, y: y + vector.dy, width: width, height: height)
  }
}

private extension [ConstellationRect] {
  var envelope: ConstellationRect {
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
