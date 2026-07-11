import Foundation

public struct ConstellationResponseEvent: Sendable, Equatable {
  public let usefulSourceIDs: Set<String>
  public let failedSourceIDs: Set<String>

  public init(usefulSourceIDs: Set<String>, failedSourceIDs: Set<String>) {
    self.usefulSourceIDs = usefulSourceIDs
    self.failedSourceIDs = failedSourceIDs
  }
}

public enum ConstellationActivity: Sendable, Equatable {
  case idle
  case searching(sourceID: String?, response: ConstellationResponseEvent?)
  case syncing(sourceIDs: Set<String>, response: ConstellationResponseEvent?)

  public func requestedSourceIDs(allSourceIDs: Set<String>) -> Set<String> {
    switch self {
    case .idle:
      []
    case .searching(let sourceID, _):
      sourceID.map { [$0] } ?? allSourceIDs
    case .syncing(let sourceIDs, _):
      sourceIDs
    }
  }

  public var response: ConstellationResponseEvent? {
    switch self {
    case .idle:
      nil
    case .searching(_, let response), .syncing(_, let response):
      response
    }
  }

  public var isWorkInProgress: Bool {
    switch self {
    case .idle:
      false
    case .searching(_, let response), .syncing(_, let response):
      response == nil
    }
  }
}

public struct ConstellationTrafficPlan: Sendable, Equatable {
  public let outboundSourceIDs: Set<String>
  public let returningSourceIDs: Set<String>
  public let failedSourceIDs: Set<String>

  public init(activity: ConstellationActivity, allSourceIDs: Set<String>) {
    outboundSourceIDs = activity.requestedSourceIDs(allSourceIDs: allSourceIDs)
    if let response = activity.response {
      let failed = response.failedSourceIDs.intersection(outboundSourceIDs)
      failedSourceIDs = failed
      returningSourceIDs = response.usefulSourceIDs
        .intersection(outboundSourceIDs)
        .subtracting(failed)
    } else {
      failedSourceIDs = []
      returningSourceIDs = []
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
    if count <= 12 {
      return Self(
        hostSize: ConstellationPoint(x: 180, y: 164),
        hostCentreYOffset: 29,
        labelWidth: 140,
        labelTop: 32,
        labelHeight: 63,
        minimumIconDiameter: 50,
        maximumIconDiameter: 74,
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
      hostSize: ConstellationPoint(x: 120, y: 132),
      hostCentreYOffset: 29,
      labelWidth: 80,
      labelTop: 27,
      labelHeight: 54,
      minimumIconDiameter: 40,
      maximumIconDiameter: 46,
      diamondClearanceRadius: 66,
      spacing: 6
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

  public func placements() -> [ConstellationPlacement] {
    guard !sourceIDs.isEmpty else { return [] }
    let orderedIDs = sourceIDs.sorted()
    var available = candidates()
    guard available.count >= orderedIDs.count else {
      let placements = fallbackPlacements(for: orderedIDs)
      let placementsByID = Dictionary(uniqueKeysWithValues: placements.map { ($0.id, $0) })
      return sourceIDs.compactMap { placementsByID[$0] }
    }

    var selected: [(id: String, anchor: ConstellationPoint)] = []
    for sourceID in orderedIDs {
      let anchor = available.max { lhs, rhs in
        score(lhs, sourceID: sourceID, selected: selected) < score(rhs, sourceID: sourceID, selected: selected)
      }!
      selected.append((sourceID, anchor))
      available.removeAll { metrics.hostRect(at: $0).expanded(by: metrics.spacing).intersects(metrics.hostRect(at: anchor)) }
    }

    let placementsByID = Dictionary(uniqueKeysWithValues: selected.map { item in
      (
        item.id,
        ConstellationPlacement(
          id: item.id,
          anchor: item.anchor,
          hostRect: metrics.hostRect(at: item.anchor),
          labelRect: metrics.labelRect(at: item.anchor)
        )
      )
    })
    return sourceIDs.compactMap { placementsByID[$0] }
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

  private func candidates() -> [ConstellationPoint] {
    let minimumX = metrics.hostSize.x / 2
    let maximumX = size.x - metrics.hostSize.x / 2
    let minimumY = metrics.hostSize.y / 2 - metrics.hostCentreYOffset
    let maximumY = size.y - metrics.hostSize.y / 2 - metrics.hostCentreYOffset
    guard maximumX > minimumX, maximumY > minimumY else { return [] }

    let maximumColumns = max(
      1,
      Int((maximumX - minimumX) / (metrics.hostSize.x + metrics.spacing)) + 1
    )
    let maximumRows = max(
      1,
      Int((maximumY - minimumY) / (metrics.hostSize.y + metrics.spacing)) + 1
    )
    var best: [ConstellationPoint] = []
    var bestExcess = Int.max
    var bestAspectError = Double.infinity

    for columns in 3...max(3, maximumColumns) {
      for rows in 3...max(3, maximumRows) {
        let generated = relaxedGrid(
          columns: columns,
          rows: rows,
          minimumX: minimumX,
          maximumX: maximumX,
          minimumY: minimumY,
          maximumY: maximumY
        ).filter(isValidCandidate)
        let excess = generated.count - sourceIDs.count
        guard excess >= 0 else { continue }
        let aspectError = abs(Double(columns) / Double(rows) - size.x / size.y)
        if excess < bestExcess || (excess == bestExcess && aspectError < bestAspectError) {
          best = generated
          bestExcess = excess
          bestAspectError = aspectError
        }
      }
    }
    return best
  }

  private func relaxedGrid(
    columns: Int,
    rows: Int,
    minimumX: Double,
    maximumX: Double,
    minimumY: Double,
    maximumY: Double
  ) -> [ConstellationPoint] {
    let stepX = columns == 1 ? 0 : (maximumX - minimumX) / Double(columns - 1)
    let stepY = rows == 1 ? 0 : (maximumY - minimumY) / Double(rows - 1)
    guard
      columns == 1 || stepX >= metrics.hostSize.x + metrics.spacing,
      rows == 1 || stepY >= metrics.hostSize.y + metrics.spacing
    else { return [] }

    let slackX = max(0, stepX - metrics.hostSize.x - metrics.spacing)
    let slackY = max(0, stepY - metrics.hostSize.y - metrics.spacing)
    return (0..<rows).flatMap { row in
      (0..<columns).map { column in
        let index = row * columns + column
        let horizontalJitter = column == 0 || column == columns - 1
          ? 0
          : (unit("candidate-\(index)", salt: 11) - 0.5) * slackX
        let verticalJitter = row == 0 || row == rows - 1
          ? 0
          : (unit("candidate-\(index)", salt: 13) - 0.5) * slackY
        return ConstellationPoint(
          x: minimumX + Double(column) * stepX + horizontalJitter,
          y: minimumY + Double(row) * stepY + verticalJitter
        )
      }
    }
  }

  private func isValidCandidate(_ anchor: ConstellationPoint) -> Bool {
    let host = metrics.hostRect(at: anchor)
    return canvas.contains(host) && !host.expanded(by: metrics.spacing).intersects(diamond)
  }

  private func score(
    _ candidate: ConstellationPoint,
    sourceID: String,
    selected: [(id: String, anchor: ConstellationPoint)]
  ) -> Double {
    let minimumDistance = selected.map { candidate.distance(to: $0.anchor) }.min() ?? hypot(size.x, size.y)
    let horizontalRadius = max(size.x / 2, 1)
    let verticalRadius = max(size.y / 2, 1)
    let radius = hypot(
      (candidate.x - centre.x) / horizontalRadius,
      (candidate.y - centre.y) / verticalRadius
    )
    let radiusBucket = Int(candidate.distance(to: centre) / 40)
    let usedRadiusBuckets = Set(selected.map { item in
      Int(item.anchor.distance(to: centre) / 40)
    })
    let radialNovelty = usedRadiusBuckets.contains(radiusBucket) ? 0 : 120.0
    let preferredRadius = 0.58 + unit(sourceID, salt: 17) * 0.34
    let orbitScore = 1 - abs(radius - preferredRadius)
    let tieBreak = unit("\(sourceID):\(candidate.x):\(candidate.y)", salt: 19)
    return minimumDistance + radialNovelty + orbitScore * 80 + tieBreak * 8
  }

  private func fallbackPlacements(for ids: [String]) -> [ConstellationPlacement] {
    let minimumX = metrics.hostSize.x / 2
    let maximumX = max(minimumX, size.x - metrics.hostSize.x / 2)
    let minimumY = metrics.hostSize.y / 2 - metrics.hostCentreYOffset
    let maximumY = max(minimumY, size.y - metrics.hostSize.y / 2 - metrics.hostCentreYOffset)
    return ids.map { id in
      let candidate = ConstellationPoint(
        x: minimumX + unit(id, salt: 23) * (maximumX - minimumX),
        y: minimumY + unit(id, salt: 29) * (maximumY - minimumY)
      )
      let anchor: ConstellationPoint
      if metrics.hostRect(at: candidate).intersects(diamond) {
        let x = candidate.x < centre.x
          ? max(minimumX, diamond.x - metrics.hostSize.x / 2 - metrics.spacing)
          : min(maximumX, diamond.maxX + metrics.hostSize.x / 2 + metrics.spacing)
        anchor = ConstellationPoint(x: x, y: candidate.y)
      } else {
        anchor = candidate
      }
      return ConstellationPlacement(
        id: id,
        anchor: anchor,
        hostRect: metrics.hostRect(at: anchor),
        labelRect: metrics.labelRect(at: anchor)
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
