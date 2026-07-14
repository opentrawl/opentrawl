import Foundation

public struct ConstellationTrafficEvent: Sendable, Equatable {
  public let requestedSourceIDs: Set<String>
  public let usefulSourceIDs: Set<String>
  public let failedSourceIDs: Set<String>

  public init(
    requestedSourceIDs: Set<String>,
    usefulSourceIDs: Set<String>,
    failedSourceIDs: Set<String>
  ) {
    self.requestedSourceIDs = requestedSourceIDs
    self.usefulSourceIDs = usefulSourceIDs
    self.failedSourceIDs = failedSourceIDs
  }
}

public enum ConstellationActivity: Sendable, Equatable {
  case idle
  case searching(sourceID: String?)
  case syncing(sourceIDs: Set<String>)
  case failed(sourceIDs: Set<String>)

  public func requestedSourceIDs(allSourceIDs: Set<String>) -> Set<String> {
    switch self {
    case .idle:
      []
    case .searching(let sourceID):
      sourceID.map { [$0] } ?? allSourceIDs
    case .syncing(let sourceIDs), .failed(let sourceIDs):
      sourceIDs
    }
  }

  public var isWorkInProgress: Bool {
    switch self {
    case .idle, .failed:
      false
    case .searching, .syncing:
      true
    }
  }
}

public struct ConstellationTrafficPlan: Sendable, Equatable {
  public let outboundSourceIDs: Set<String>
  public let returningSourceIDs: Set<String>
  public let failedSourceIDs: Set<String>

  public init(activity: ConstellationActivity, allSourceIDs: Set<String>) {
    outboundSourceIDs =
      activity.isWorkInProgress
      ? activity.requestedSourceIDs(allSourceIDs: allSourceIDs)
      : []
    returningSourceIDs = []
    if case .failed(let sourceIDs) = activity {
      failedSourceIDs = sourceIDs.intersection(allSourceIDs)
    } else {
      failedSourceIDs = []
    }
  }

  public init(event: ConstellationTrafficEvent, allSourceIDs: Set<String>) {
    outboundSourceIDs = []
    let requested = event.requestedSourceIDs.intersection(allSourceIDs)
    let failed = event.failedSourceIDs.intersection(requested)
    failedSourceIDs = failed
    returningSourceIDs = event.usefulSourceIDs.intersection(requested).subtracting(failed)
  }

  public var affectedSourceIDs: Set<String> {
    outboundSourceIDs.union(returningSourceIDs).union(failedSourceIDs)
  }
}

public struct ConstellationPulseTiming: Sendable, Equatable {
  public let delay: TimeInterval

  public init(delay: TimeInterval) {
    self.delay = delay
  }

  public func isVisible(elapsed: TimeInterval) -> Bool {
    elapsed >= delay
  }

  public func routeSampleStartElapsed(
    currentElapsed: TimeInterval,
    repeatsFromSharedEpoch: Bool
  ) -> TimeInterval {
    repeatsFromSharedEpoch ? 0 : currentElapsed + delay
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

public struct ConstellationRect: Sendable, Equatable {
  public let x: Double
  public let y: Double
  public let width: Double
  public let height: Double

  public init(x: Double, y: Double, width: Double, height: Double) {
    self.x = x
    self.y = y
    self.width = width
    self.height = height
  }

  public var midX: Double { x + width / 2 }
  public var midY: Double { y + height / 2 }
  public var maxX: Double { x + width }
  public var maxY: Double { y + height }

  public func contains(_ other: Self) -> Bool {
    other.x >= x && other.y >= y && other.maxX <= maxX && other.maxY <= maxY
  }

  public func intersects(_ other: Self) -> Bool {
    x < other.maxX && maxX > other.x && y < other.maxY && maxY > other.y
  }

  public func expanded(by amount: Double) -> Self {
    Self(x: x - amount, y: y - amount, width: width + amount * 2, height: height + amount * 2)
  }
}

public struct ConstellationLayoutMetrics: Sendable, Equatable {
  public let hostSize: ConstellationPoint
  public let hostCentreYOffset: Double
  public let labelWidth: Double
  public let labelTop: Double
  public let labelHeight: Double
  public let minimumIconDiameter: Double
  public let maximumIconDiameter: Double
  public let diamondClearanceRadius: Double
  public let spacing: Double

  public static func forSourceCount(_ count: Int) -> Self {
    if count <= 9 {
      return Self(
        hostSize: ConstellationPoint(x: 172, y: 160),
        hostCentreYOffset: 35,
        labelWidth: 148,
        labelTop: 30,
        labelHeight: 68,
        minimumIconDiameter: 48,
        maximumIconDiameter: 68,
        diamondClearanceRadius: 66,
        spacing: 6
      )
    }
    if count == 10 {
      return Self(
        hostSize: ConstellationPoint(x: 172, y: 184),
        hostCentreYOffset: 35,
        labelWidth: 156,
        labelTop: 30,
        labelHeight: 92,
        minimumIconDiameter: 48,
        maximumIconDiameter: 68,
        diamondClearanceRadius: 66,
        spacing: 6
      )
    }
    if count <= 12 {
      return Self(
        hostSize: ConstellationPoint(x: 156, y: 148),
        hostCentreYOffset: 29,
        labelWidth: 128,
        labelTop: 30,
        labelHeight: 59,
        minimumIconDiameter: 48,
        maximumIconDiameter: 68,
        diamondClearanceRadius: 66,
        spacing: 6
      )
    }
    if count <= 16 {
      return Self(
        hostSize: ConstellationPoint(x: 144, y: 148),
        hostCentreYOffset: 29,
        labelWidth: 104,
        labelTop: 30,
        labelHeight: 59,
        minimumIconDiameter: 46,
        maximumIconDiameter: 62,
        diamondClearanceRadius: 66,
        spacing: 6
      )
    }
    return Self(
      hostSize: ConstellationPoint(x: 104, y: 112),
      hostCentreYOffset: 25,
      labelWidth: 72,
      labelTop: 24,
      labelHeight: 47,
      minimumIconDiameter: 38,
      maximumIconDiameter: 44,
      diamondClearanceRadius: 66,
      spacing: 4
    )
  }

  public static func forSourceCount(_ count: Int, fitting size: ConstellationPoint) -> Self {
    let shorterCanvasSide = min(size.x, size.y)
    let canvasScale = min(max((shorterCanvasSide - 504) / 216, 0), 1)
    let density = min(1, 9 / Double(max(count, 1)))
    func scaled(_ minimum: Double, _ maximum: Double) -> Double {
      (minimum + (maximum - minimum) * canvasScale) * density
    }
    return Self(
      hostSize: ConstellationPoint(x: max(48, scaled(104, 144)), y: max(54, scaled(118, 156))),
      hostCentreYOffset: max(14, scaled(24, 30)),
      labelWidth: max(44, scaled(96, 132)),
      labelTop: max(14, scaled(24, 28)),
      labelHeight: max(72, scaled(72, 76)),
      minimumIconDiameter: max(28, scaled(36, 44)),
      maximumIconDiameter: max(34, scaled(44, 56)),
      diamondClearanceRadius: max(30, scaled(48, 66)),
      spacing: max(3, scaled(4, 6))
    )
  }

  public func hostRect(at anchor: ConstellationPoint) -> ConstellationRect {
    ConstellationRect(
      x: anchor.x - hostSize.x / 2,
      y: anchor.y + hostCentreYOffset - hostSize.y / 2,
      width: hostSize.x,
      height: hostSize.y
    )
  }

  public func labelRect(at anchor: ConstellationPoint) -> ConstellationRect {
    ConstellationRect(
      x: anchor.x - labelWidth / 2,
      y: anchor.y + labelTop,
      width: labelWidth,
      height: labelHeight
    )
  }
}

public struct ConstellationPlacement: Sendable, Equatable, Identifiable {
  public let id: String
  public let anchor: ConstellationPoint
  public let hostRect: ConstellationRect
  public let labelRect: ConstellationRect
}

public enum ConstellationLayoutResult: Sendable, Equatable {
  case placements([ConstellationPlacement])
  case unsupported(sourceCount: Int, size: ConstellationPoint)

  public var placements: [ConstellationPlacement] {
    guard case .placements(let placements) = self else { return [] }
    return placements
  }
}

public struct ConstellationOrbitLayout: Sendable {
  public let sourceIDs: [String]
  public let size: ConstellationPoint
  public let centre: ConstellationPoint
  public let metrics: ConstellationLayoutMetrics

  public init(
    sourceIDs: [String],
    size: ConstellationPoint,
    centre: ConstellationPoint,
    metrics: ConstellationLayoutMetrics
  ) {
    self.sourceIDs = sourceIDs
    self.size = size
    self.centre = centre
    self.metrics = metrics
  }

  public func placementResult() -> ConstellationLayoutResult {
    guard !sourceIDs.isEmpty else { return .placements([]) }
    let orderedIDs = sourceIDs.sorted()
    let composition = normalisedComposition(for: orderedIDs)
    let anchorsInOrbitOrder = orderedIDs.enumerated().compactMap { index, sourceID in
      anchor(for: sourceID, polar: composition[index])
    }
    guard anchorsInOrbitOrder.count == orderedIDs.count else {
      return .unsupported(sourceCount: sourceIDs.count, size: size)
    }
    let placementsByID = Dictionary(
      uniqueKeysWithValues: zip(orderedIDs, anchorsInOrbitOrder).map {
        sourceID, anchor in
        (
          sourceID,
          ConstellationPlacement(
            id: sourceID,
            anchor: anchor,
            hostRect: metrics.hostRect(at: anchor),
            labelRect: metrics.labelRect(at: anchor)
          )
        )
      })
    let placements = sourceIDs.compactMap { placementsByID[$0] }
    guard placements.count == sourceIDs.count else {
      return .unsupported(sourceCount: sourceIDs.count, size: size)
    }
    return .placements(placements)
  }

  public func placements() -> [ConstellationPlacement] {
    placementResult().placements
  }

  private var canvas: ConstellationRect {
    ConstellationRect(x: 0, y: 0, width: size.x, height: size.y)
  }

  private var diamond: ConstellationRect {
    let diameter = metrics.diamondClearanceRadius * 2
    return ConstellationRect(
      x: centre.x - metrics.diamondClearanceRadius,
      y: centre.y - metrics.diamondClearanceRadius,
      width: diameter,
      height: diameter
    )
  }

  private func normalisedComposition(for orderedIDs: [String]) -> [(angle: Double, radius: Double)]
  {
    let weights = orderedIDs.map { 0.92 + unit($0, salt: 11) * 0.16 }
    let weightTotal = weights.reduce(0, +)
    let gaps = weights.map { 2 * Double.pi * $0 / weightTotal }
    var angle = -gaps[0] / 2
    return orderedIDs.indices.map { index in
      defer { angle += gaps[index] }
      return (angle: angle, radius: 0.96 + unit(orderedIDs[index], salt: 13) * 0.04)
    }
  }

  private func anchor(
    for sourceID: String,
    polar: (angle: Double, radius: Double)
  ) -> ConstellationPoint? {
    let horizontalRadius = min(centre.x, size.x - centre.x) - metrics.hostSize.x / 2
    let minimumAnchorY = metrics.hostSize.y / 2 - metrics.hostCentreYOffset
    let maximumAnchorY = min(
      size.y - metrics.hostSize.y / 2 - metrics.hostCentreYOffset,
      size.y - metrics.labelTop - metrics.labelHeight
    )
    let verticalRadius = min(centre.y - minimumAnchorY, maximumAnchorY - centre.y)
    let anchor = ConstellationPoint(
      x: centre.x + horizontalRadius * polar.radius * cos(polar.angle),
      y: centre.y + verticalRadius * polar.radius * sin(polar.angle)
    )
    guard canvas.contains(metrics.hostRect(at: anchor)),
      !metrics.hostRect(at: anchor).expanded(by: metrics.spacing).intersects(diamond)
    else { return nil }
    return anchor
  }

  private func unit(_ value: String, salt: UInt64) -> Double {
    let hash = value.utf8.reduce(0xcbf2_9ce4_8422_2325 ^ salt) { partial, byte in
      (partial ^ UInt64(byte)) &* 0x100_0000_01b3
    }
    return Double(hash) / Double(UInt64.max)
  }
}
