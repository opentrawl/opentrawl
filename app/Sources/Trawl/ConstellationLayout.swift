import CoreGraphics
import Foundation
import TrawlClient

enum ConstellationGeometry {
  static let sourceContentWidth: CGFloat = 154
  static let sourceLabelAllowance: CGFloat = 58
  static let sourceHostSize = CGSize(width: 194, height: 164)
  static let horizontalMotion = CGFloat(12)...CGFloat(20)
  static let verticalMotion = CGFloat(8)...CGFloat(14)

  static var horizontalClearance: CGFloat {
    sourceHostSize.width / 2 + horizontalMotion.upperBound
  }

  static var topClearance: CGFloat {
    sourceHostSize.height / 2 - TrawlDesign.sourceGraphAnchorOffset
      + verticalMotion.upperBound
  }

  static var bottomClearance: CGFloat {
    sourceHostSize.height / 2 + TrawlDesign.sourceGraphAnchorOffset
      + verticalMotion.upperBound
  }
}

struct MovingSource: Identifiable {
  let source: SourceStatus
  let anchor: CGPoint
  let diameter: CGFloat

  var id: String { source.id }
}

struct ConstellationSnapshot {
  let centre: CGPoint
  let sources: [MovingSource]
  let contextNodes: [CGPoint]
  let segments: [NetworkSegment]
}

struct NetworkEndpoint: Equatable {
  let anchor: CGPoint
  let trimRadius: CGFloat
  let sourceID: String?

  func point(offset: CGVector = .zero) -> CGPoint {
    CGPoint(x: anchor.x + offset.dx, y: anchor.y + offset.dy)
  }
}

struct NetworkSegment: Equatable {
  enum Kind: Equatable {
    case context
    case source
    case centre
  }

  let startEndpoint: NetworkEndpoint
  let endEndpoint: NetworkEndpoint
  let kind: Kind

  var movingSourceID: String? {
    switch (startEndpoint.sourceID, endEndpoint.sourceID) {
    case (.some(let sourceID), nil), (nil, .some(let sourceID)):
      sourceID
    default:
      nil
    }
  }

  func points(sourceOffset: CGVector = .zero) -> (start: CGPoint, end: CGPoint) {
    let startOffset = startEndpoint.sourceID == movingSourceID ? sourceOffset : .zero
    let endOffset = endEndpoint.sourceID == movingSourceID ? sourceOffset : .zero
    let startAnchor = startEndpoint.point(offset: startOffset)
    let endAnchor = endEndpoint.point(offset: endOffset)
    let length = max(hypot(endAnchor.x - startAnchor.x, endAnchor.y - startAnchor.y), 1)
    let unit = CGVector(
      dx: (endAnchor.x - startAnchor.x) / length,
      dy: (endAnchor.y - startAnchor.y) / length
    )
    return (
      start: CGPoint(
        x: startAnchor.x + unit.dx * startEndpoint.trimRadius,
        y: startAnchor.y + unit.dy * startEndpoint.trimRadius
      ),
      end: CGPoint(
        x: endAnchor.x - unit.dx * endEndpoint.trimRadius,
        y: endAnchor.y - unit.dy * endEndpoint.trimRadius
      )
    )
  }
}

private struct GraphEdge: Hashable, Comparable {
  let start: Int
  let end: Int

  init(_ lhs: Int, _ rhs: Int) {
    start = min(lhs, rhs)
    end = max(lhs, rhs)
  }

  static func < (lhs: GraphEdge, rhs: GraphEdge) -> Bool {
    (lhs.start, lhs.end) < (rhs.start, rhs.end)
  }
}

private struct Triangle {
  let a: Int
  let b: Int
  let c: Int

  var edges: [GraphEdge] {
    [GraphEdge(a, b), GraphEdge(b, c), GraphEdge(c, a)]
  }

  func contains(vertex: Int) -> Bool {
    a == vertex || b == vertex || c == vertex
  }
}

struct ConstellationLayout {
  private let sources: [SourceStatus]
  private let sourceBases: [CGPoint]
  private let contextBases: [CGPoint]
  private let centreBase: CGPoint
  private let graphEdges: [GraphEdge]
  private let minimumBytes: Double
  private let maximumBytes: Double

  init(size: CGSize, sources: [SourceStatus], meshSeed: UInt64) {
    self.sources = sources
    let verticalOffset = -min(TrawlDesign.sourceGraphAnchorOffset, size.height * 0.035)
    centreBase = CGPoint(x: size.width / 2, y: size.height / 2 + verticalOffset)
    sourceBases = Self.makeSourceBases(sources: sources, size: size, centre: centreBase)
    contextBases = Self.makeContextBases(
      count: max(10, min(18, sources.count + 3)),
      size: size,
      centre: centreBase,
      seed: meshSeed
    )
    graphEdges = Self.makeGraphEdges(
      points: sourceBases + [centreBase] + contextBases,
      sourceCount: sources.count
    )

    let positive = sources.map(\.archiveBytes).filter { $0 > 0 }.map(Double.init)
    minimumBytes = positive.min() ?? 0
    maximumBytes = positive.max() ?? 0
  }

  func snapshot() -> ConstellationSnapshot {
    let diameters = sources.map(diameter)
    let points = sourceBases + [centreBase] + contextBases
    let endpoints = zip(points.indices, points).map { index, point in
      if index < sources.count {
        return NetworkEndpoint(
          anchor: point,
          trimRadius: diameters[index] / 2,
          sourceID: sources[index].id
        )
      }
      if index == sources.count {
        return NetworkEndpoint(
          anchor: point,
          trimRadius: TrawlDesign.centreSize / 2 + 2,
          sourceID: nil
        )
      }
      return NetworkEndpoint(anchor: point, trimRadius: 2, sourceID: nil)
    }

    let centreIndex = sources.count
    let segments = graphEdges.map { edge in
      let kind: NetworkSegment.Kind
      if edge.start == centreIndex || edge.end == centreIndex {
        kind = .centre
      } else if edge.start < sources.count || edge.end < sources.count {
        kind = .source
      } else {
        kind = .context
      }
      return NetworkSegment(
        startEndpoint: endpoints[edge.start],
        endEndpoint: endpoints[edge.end],
        kind: kind
      )
    }

    return ConstellationSnapshot(
      centre: centreBase,
      sources: zip(sources.indices, sources).map { index, source in
        MovingSource(
          source: source,
          anchor: sourceBases[index],
          diameter: diameters[index]
        )
      },
      contextNodes: contextBases,
      segments: segments
    )
  }

  private func diameter(for source: SourceStatus) -> CGFloat {
    guard source.archiveBytes > 0, maximumBytes > minimumBytes else {
      return TrawlDesign.sourceMinimum
    }
    let value = log1p(Double(source.archiveBytes))
    let lower = log1p(minimumBytes)
    let upper = log1p(maximumBytes)
    let normalised = (value - lower) / (upper - lower)
    return TrawlDesign.sourceMinimum
      + CGFloat(normalised) * (TrawlDesign.sourceMaximum - TrawlDesign.sourceMinimum)
  }

  private static func makeSourceBases(
    sources: [SourceStatus],
    size: CGSize,
    centre: CGPoint
  ) -> [CGPoint] {
    guard !sources.isEmpty else { return [] }
    if sources.count <= 12 {
      return ringPoints(
        sources: sources,
        size: size,
        centre: centre,
        radiusX: 0.38,
        radiusY: 0.39,
        angleOffset: -.pi / 2
      )
    }

    let outerCount = Int(ceil(Double(sources.count) * 0.64))
    let outer = ringPoints(
      sources: Array(sources.prefix(outerCount)),
      size: size,
      centre: centre,
      radiusX: 0.39,
      radiusY: 0.40,
      angleOffset: -.pi / 2
    )
    let innerSources = Array(sources.dropFirst(outerCount))
    let inner = ringPoints(
      sources: innerSources,
      size: size,
      centre: centre,
      radiusX: 0.27,
      radiusY: 0.28,
      angleOffset: -.pi / 2 + .pi / Double(max(innerSources.count, 1))
    )
    return outer + inner
  }

  private static func ringPoints(
    sources: [SourceStatus],
    size: CGSize,
    centre: CGPoint,
    radiusX: CGFloat,
    radiusY: CGFloat,
    angleOffset: Double
  ) -> [CGPoint] {
    let horizontalCapacity = max(
      0,
      min(
        centre.x - ConstellationGeometry.horizontalClearance,
        size.width - ConstellationGeometry.horizontalClearance - centre.x
      )
    )
    let verticalCapacity = max(
      0,
      min(
        centre.y - ConstellationGeometry.topClearance,
        size.height - ConstellationGeometry.bottomClearance - centre.y
      )
    )
    let horizontalRadius = min(size.width * radiusX, horizontalCapacity)
    let verticalRadius = min(size.height * radiusY, verticalCapacity)

    return sources.enumerated().map { index, source in
      let angleJitter = (stableUnit(source.id, salt: 1) - 0.5) * 0.19
      let radiusScale = CGFloat(0.94 + stableUnit(source.id, salt: 2) * 0.06)
      let angle =
        angleOffset
        + 2 * .pi * Double(index) / Double(sources.count)
        + angleJitter
      return CGPoint(
        x: centre.x + cos(angle) * horizontalRadius * radiusScale,
        y: centre.y + sin(angle) * verticalRadius * radiusScale
      )
    }
  }

  private static func makeContextBases(
    count: Int,
    size: CGSize,
    centre: CGPoint,
    seed: UInt64
  ) -> [CGPoint] {
    var random = SplitMix64(seed: seed)
    let rotation = Double(random.unit()) * 2 * .pi
    let goldenAngle = .pi * (3 - sqrt(5.0))
    return (0..<count).map { index in
      let fraction = (Double(index) + 0.75) / Double(count)
      let radius = CGFloat(0.1 + sqrt(fraction) * 0.18)
      let radialJitter = (random.unit() - 0.5) * 0.016
      let angularJitter = Double(random.unit() - 0.5) * 0.28
      let angle = rotation + Double(index) * goldenAngle + angularJitter
      return CGPoint(
        x: centre.x + CGFloat(cos(angle)) * (radius + radialJitter) * size.width,
        y: centre.y
          + CGFloat(sin(angle)) * (radius + radialJitter) * size.height * 0.94
      )
    }
  }

  private static func makeGraphEdges(points: [CGPoint], sourceCount: Int) -> [GraphEdge] {
    let centreIndex = sourceCount
    let contextIndices = Array(points.indices.dropFirst(sourceCount + 1))
    let interiorIndices = [centreIndex] + contextIndices
    var edges = Set(
      triangulatedEdges(points: interiorIndices.map { points[$0] }).map { edge in
        GraphEdge(interiorIndices[edge.start], interiorIndices[edge.end])
      }
    )

    for sourceIndex in 0..<sourceCount {
      let nearestContext = contextIndices.sorted {
        distance(points[sourceIndex], points[$0]) < distance(points[sourceIndex], points[$1])
      }
      for contextIndex in nearestContext.prefix(2) {
        edges.insert(GraphEdge(sourceIndex, contextIndex))
      }
    }
    return edges.sorted()
  }

  private static func triangulatedEdges(points: [CGPoint]) -> [GraphEdge] {
    guard points.count > 2 else {
      return points.count == 2 ? [GraphEdge(0, 1)] : []
    }

    var workingPoints = points
    let bounds = points.reduce(
      (
        minX: CGFloat.greatestFiniteMagnitude,
        maxX: -CGFloat.greatestFiniteMagnitude,
        minY: CGFloat.greatestFiniteMagnitude,
        maxY: -CGFloat.greatestFiniteMagnitude
      )
    ) { bounds, point in
      (
        min(bounds.minX, point.x), max(bounds.maxX, point.x),
        min(bounds.minY, point.y), max(bounds.maxY, point.y)
      )
    }
    let span = max(bounds.maxX - bounds.minX, bounds.maxY - bounds.minY, 1)
    let middle = CGPoint(x: (bounds.minX + bounds.maxX) / 2, y: (bounds.minY + bounds.maxY) / 2)
    let superVertices = [
      CGPoint(x: middle.x - span * 20, y: middle.y - span),
      CGPoint(x: middle.x, y: middle.y + span * 20),
      CGPoint(x: middle.x + span * 20, y: middle.y - span),
    ]
    let firstSuperVertex = workingPoints.count
    workingPoints.append(contentsOf: superVertices)
    var triangles = [
      Triangle(a: firstSuperVertex, b: firstSuperVertex + 1, c: firstSuperVertex + 2)
    ]

    for pointIndex in points.indices {
      let badTriangleIndices = Set(
        triangles.indices.filter {
          circumcircle(of: triangles[$0], in: workingPoints, contains: workingPoints[pointIndex])
        }
      )
      var edgeCounts: [GraphEdge: Int] = [:]
      for index in badTriangleIndices {
        for edge in triangles[index].edges {
          edgeCounts[edge, default: 0] += 1
        }
      }
      triangles = triangles.indices.compactMap { index in
        badTriangleIndices.contains(index) ? nil : triangles[index]
      }
      for (edge, count) in edgeCounts where count == 1 {
        triangles.append(Triangle(a: edge.start, b: edge.end, c: pointIndex))
      }
    }

    let finished = triangles.filter { triangle in
      triangle.a < firstSuperVertex && triangle.b < firstSuperVertex
        && triangle.c < firstSuperVertex
    }
    return Set(finished.flatMap(\.edges)).sorted()
  }

  private static func circumcircle(
    of triangle: Triangle,
    in points: [CGPoint],
    contains point: CGPoint
  ) -> Bool {
    let a = points[triangle.a]
    let b = points[triangle.b]
    let c = points[triangle.c]
    let determinant = 2 * (a.x * (b.y - c.y) + b.x * (c.y - a.y) + c.x * (a.y - b.y))
    guard abs(determinant) > 0.0001 else { return false }

    let aSquared = a.x * a.x + a.y * a.y
    let bSquared = b.x * b.x + b.y * b.y
    let cSquared = c.x * c.x + c.y * c.y
    let centre = CGPoint(
      x: (aSquared * (b.y - c.y) + bSquared * (c.y - a.y) + cSquared * (a.y - b.y))
        / determinant,
      y: (aSquared * (c.x - b.x) + bSquared * (a.x - c.x) + cSquared * (b.x - a.x))
        / determinant
    )
    let radiusSquared = squaredDistance(centre, a)
    return squaredDistance(centre, point) <= radiusSquared + 0.01
  }

  private static func stableUnit(_ value: String, salt: UInt64) -> Double {
    let hash = value.utf8.reduce(0xcbf2_9ce4_8422_2325 ^ salt) { partial, byte in
      (partial ^ UInt64(byte)) &* 0x100_0000_01b3
    }
    return Double(hash) / Double(UInt64.max)
  }

  private static func distance(_ lhs: CGPoint, _ rhs: CGPoint) -> CGFloat {
    hypot(lhs.x - rhs.x, lhs.y - rhs.y)
  }

  private static func squaredDistance(_ lhs: CGPoint, _ rhs: CGPoint) -> CGFloat {
    let dx = lhs.x - rhs.x
    let dy = lhs.y - rhs.y
    return dx * dx + dy * dy
  }
}

private struct SplitMix64 {
  private var state: UInt64

  init(seed: UInt64) {
    state = seed
  }

  mutating func unit() -> CGFloat {
    state &+= 0x9e37_79b9_7f4a_7c15
    var value = state
    value = (value ^ (value >> 30)) &* 0xbf58_476d_1ce4_e5b9
    value = (value ^ (value >> 27)) &* 0x94d0_49bb_1331_11eb
    value ^= value >> 31
    return CGFloat(Double(value) / Double(UInt64.max))
  }
}
