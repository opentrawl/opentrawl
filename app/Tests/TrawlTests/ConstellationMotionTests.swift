import Foundation
import Testing

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
    ConstellationPoint(x: 800, y: 700),
    ConstellationPoint(x: 900, y: 720),
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
      let placements = layout.placements()
      print("CONSTELLATION_OUTPUT size=\(size) count=\(count) placements=\(placements)")

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

@Test func activityPreservesTheCompleteUntouchedInputMeaning() {
  let allSources: Set<String> = ["calendar", "gmail", "photos"]
  let usefulGmail = ConstellationResponseEvent(
    usefulSourceIDs: ["gmail"],
    failedSourceIDs: []
  )
  let mixedSync = ConstellationResponseEvent(
    usefulSourceIDs: ["calendar", "gmail", "photos"],
    failedSourceIDs: ["photos"]
  )
  let inputs: [ConstellationActivity] = [
    .idle,
    .searching(sourceID: nil, response: nil),
    .searching(sourceID: "gmail", response: usefulGmail),
    .syncing(sourceIDs: allSources, response: nil),
    .syncing(sourceIDs: allSources, response: mixedSync),
  ]
  let outputs = inputs.map {
    (
      plan: ConstellationTrafficPlan(activity: $0, allSourceIDs: allSources),
      isWorkInProgress: $0.isWorkInProgress,
      response: $0.response
    )
  }

  print("CONSTELLATION_INPUT activities=\(inputs)")
  print("CONSTELLATION_OUTPUT activitySemantics=\(outputs)")

  #expect(outputs[0].plan == ConstellationTrafficPlan(activity: .idle, allSourceIDs: allSources))
  #expect(outputs[0].plan.outboundSourceIDs.isEmpty)
  #expect(outputs[1].plan.outboundSourceIDs == allSources)
  #expect(outputs[1].plan.returningSourceIDs.isEmpty)
  #expect(outputs[2].plan.outboundSourceIDs == Set(["gmail"]))
  #expect(outputs[2].plan.returningSourceIDs == Set(["gmail"]))
  #expect(outputs[3].plan.outboundSourceIDs == allSources)
  #expect(outputs[3].plan.returningSourceIDs.isEmpty)
  #expect(outputs[4].plan.outboundSourceIDs == allSources)
  #expect(outputs[4].plan.returningSourceIDs == Set(["calendar", "gmail"]))
  #expect(outputs[4].plan.failedSourceIDs == Set(["photos"]))
  #expect(outputs[4].response == mixedSync)
  #expect(!inputs[0].isWorkInProgress)
  #expect(inputs[1].isWorkInProgress)
  #expect(!inputs[2].isWorkInProgress)
  #expect(inputs[3].isWorkInProgress)
  #expect(!inputs[4].isWorkInProgress)
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
