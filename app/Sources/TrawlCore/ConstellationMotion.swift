import Foundation

public enum ConstellationActivity: Sendable, Equatable {
  case idle
  case searching(sourceID: String?)
  case syncing(sourceIDs: Set<String>)
  case failed(sourceIDs: Set<String>)

  public var activeSourceIDs: Set<String>? {
    switch self {
    case .idle:
      nil
    case .searching(let sourceID):
      sourceID.map { [$0] }
    case .syncing(let sourceIDs), .failed(let sourceIDs):
      sourceIDs
    }
  }

  public var isWorkInProgress: Bool {
    switch self {
    case .searching, .syncing:
      true
    case .idle, .failed:
      false
    }
  }
}

public struct ConstellationVector: Sendable, Equatable {
  public static let zero = Self(dx: 0, dy: 0)

  public let dx: Double
  public let dy: Double

  public init(dx: Double, dy: Double) {
    self.dx = dx
    self.dy = dy
  }
}

public struct ConstellationPoint: Sendable, Hashable {
  public let x: Double
  public let y: Double

  public init(x: Double, y: Double) {
    self.x = x
    self.y = y
  }

  public func translated(by vector: ConstellationVector) -> Self {
    Self(x: x + vector.dx, y: y + vector.dy)
  }

  public func distance(to other: Self) -> Double {
    hypot(x - other.x, y - other.y)
  }
}

public struct ConstellationMotion: Sendable, Equatable {
  public let sourceID: String
  public let phaseOffset: Double
  public let horizontalAmplitude: Double
  public let verticalAmplitude: Double
  public let duration: TimeInterval

  public init(sourceID: String) {
    self.sourceID = sourceID
    let hash = Self.hash(sourceID)
    phaseOffset = Double(hash & 0xffff) / Double(UInt16.max)
    horizontalAmplitude = 12 + Double((hash >> 16) & 0xff) / 255 * 8
    verticalAmplitude = 8 + Double((hash >> 24) & 0xff) / 255 * 6
    duration = 12 + Double((hash >> 32) & 0xff) / 255 * 2
  }

  public func translation(at phase: Double) -> ConstellationVector {
    let angle = (phase + phaseOffset) * 2 * .pi
    return ConstellationVector(
      dx: cos(angle) * horizontalAmplitude,
      dy: sin(angle) * verticalAmplitude
    )
  }

  public func translation(at phase: Double, reduceMotion: Bool) -> ConstellationVector {
    reduceMotion ? .zero : translation(at: phase)
  }

  public func translation(elapsed: TimeInterval) -> ConstellationVector {
    translation(at: elapsed / duration)
  }

  private static func hash(_ value: String) -> UInt64 {
    value.utf8.reduce(0xcbf2_9ce4_8422_2325) { partial, byte in
      (partial ^ UInt64(byte)) &* 0x100_0000_01b3
    }
  }
}

public struct ConstellationOrbitLayout: Sendable {
  public let sourceIDs: [String]
  public let size: ConstellationPoint
  public let centre: ConstellationPoint
  public let horizontalClearance: Double
  public let topClearance: Double
  public let bottomClearance: Double

  public init(
    sourceIDs: [String],
    size: ConstellationPoint,
    centre: ConstellationPoint,
    horizontalClearance: Double,
    topClearance: Double,
    bottomClearance: Double
  ) {
    self.sourceIDs = sourceIDs
    self.size = size
    self.centre = centre
    self.horizontalClearance = horizontalClearance
    self.topClearance = topClearance
    self.bottomClearance = bottomClearance
  }

  public func positions() -> [ConstellationPoint] {
    guard !sourceIDs.isEmpty else { return [] }
    let orderedIDs = sourceIDs.sorted()
    let outerCount = orderedIDs.count > 12 ? Int(ceil(Double(orderedIDs.count) * 0.68)) : orderedIDs.count
    let outer = band(
      Array(orderedIDs.prefix(outerCount)),
      radius: 0.94,
      angleOffset: -.pi / 2
    )
    guard outerCount < orderedIDs.count else { return positionsByID(ids: orderedIDs, positions: outer) }
    let inner = band(
      Array(orderedIDs.dropFirst(outerCount)),
      radius: 0.54,
      angleOffset: -.pi / 2 + .pi / Double(orderedIDs.count - outerCount)
    )
    return positionsByID(ids: orderedIDs, positions: outer + inner)
  }

  private func positionsByID(ids: [String], positions: [ConstellationPoint]) -> [ConstellationPoint] {
    let positions = Dictionary(uniqueKeysWithValues: zip(ids, positions))
    return sourceIDs.compactMap { positions[$0] }
  }

  private func band(
    _ ids: [String],
    radius: Double,
    angleOffset: Double
  ) -> [ConstellationPoint] {
    let horizontalRadius = max(
      0,
      min(size.x * 0.40, centre.x - horizontalClearance, size.x - horizontalClearance - centre.x)
    ) * radius
    let verticalRadius = max(
      0,
      min(size.y * 0.42, centre.y - topClearance, size.y - bottomClearance - centre.y)
    ) * radius

    return ids.enumerated().map { index, sourceID in
      let angleJitter = (unit(sourceID, salt: 1) - 0.5) * 0.18
      let radialJitter = 0.86 + unit(sourceID, salt: 2) * 0.12
      let angle = angleOffset + 2 * .pi * Double(index) / Double(ids.count) + angleJitter
      return ConstellationPoint(
        x: centre.x + cos(angle) * horizontalRadius * radialJitter,
        y: centre.y + sin(angle) * verticalRadius * radialJitter
      )
    }
  }

  private func unit(_ value: String, salt: UInt64) -> Double {
    let hash = value.utf8.reduce(0xcbf2_9ce4_8422_2325 ^ salt) { partial, byte in
      (partial ^ UInt64(byte)) &* 0x100_0000_01b3
    }
    return Double(hash) / Double(UInt64.max)
  }
}
