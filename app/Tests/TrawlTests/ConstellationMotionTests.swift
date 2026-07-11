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
  let counts = [6, 9, 12, 16]
  let sizes = [
    ConstellationPoint(x: 800, y: 700),
    ConstellationPoint(x: 900, y: 720),
  ]
  let horizontalClearance = 117.0
  let topClearance = 67.0
  let bottomClearance = 125.0

  for size in sizes {
    let centre = ConstellationPoint(x: size.x / 2, y: size.y / 2 - min(27, size.y * 0.035))
    for count in counts {
      let sourceIDs = (1...count).map { String(format: "synthetic-%02d", $0) }
      print("CONSTELLATION_INPUT size=\(size) centre=\(centre) sourceIDs=\(sourceIDs) clearances=[\(horizontalClearance), \(topClearance), \(bottomClearance)]")
      let layout = ConstellationOrbitLayout(
        sourceIDs: sourceIDs,
        size: size,
        centre: centre,
        horizontalClearance: horizontalClearance,
        topClearance: topClearance,
        bottomClearance: bottomClearance
      )
      let positions = layout.positions()
      print("CONSTELLATION_OUTPUT size=\(size) count=\(count) positions=\(positions)")

      #expect(positions.count == count)
      #expect(Set(positions).count == count)
      for position in positions {
        #expect(position.x >= horizontalClearance)
        #expect(position.x <= size.x - horizontalClearance)
        #expect(position.y >= topClearance)
        #expect(position.y <= size.y - bottomClearance)
      }
      for left in positions.indices {
        for right in positions.indices.dropFirst(left + 1) {
          #expect(positions[left].distance(to: positions[right]) >= 88)
        }
      }
    }
  }
}

@Test func activityPreservesTheCompleteUntouchedInputMeaning() {
  let allSources: Set<String> = ["calendar", "gmail", "photos"]
  let inputs: [ConstellationActivity] = [
    .idle,
    .searching(sourceID: nil),
    .searching(sourceID: "gmail"),
    .syncing(sourceIDs: allSources),
    .failed(sourceIDs: ["photos"]),
  ]
  let outputs = inputs.map { ($0.activeSourceIDs, $0.isWorkInProgress) }

  print("CONSTELLATION_INPUT activities=\(inputs)")
  print("CONSTELLATION_OUTPUT activitySemantics=\(outputs)")

  #expect(inputs[0].activeSourceIDs == nil)
  #expect(inputs[1].activeSourceIDs == nil)
  #expect(inputs[2].activeSourceIDs == Set(["gmail"]))
  #expect(inputs[3].activeSourceIDs == allSources)
  #expect(inputs[4].activeSourceIDs == Set(["photos"]))
  #expect(!inputs[0].isWorkInProgress)
  #expect(inputs[1].isWorkInProgress)
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
