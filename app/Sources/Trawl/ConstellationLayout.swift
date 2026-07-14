import CoreGraphics
import Foundation
import TrawlCore

struct MovingSource: Identifiable {
  let source: RestingSource
  let anchor: CGPoint
  let diameter: CGFloat
  let metrics: ConstellationLayoutMetrics

  var id: String { source.id }

  var motion: ConstellationMotion { ConstellationMotion(sourceID: source.id) }
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
}

struct ConstellationLayout {
  private let sources: [RestingSource]
  private let sourceBases: [CGPoint]
  private let metrics: ConstellationLayoutMetrics
  private let contextBases: [CGPoint]
  private let centreBase: CGPoint
  private let graphEdges: [GraphEdge]

  init(size: CGSize, sources: [RestingSource], meshSeed _: UInt64) {
    self.sources = sources
    let layoutMetrics = ConstellationLayoutMetrics.forSourceCount(
      sources.count,
      fitting: ConstellationPoint(x: size.width, y: size.height)
    )
    metrics = layoutMetrics
    let verticalOffset = -min(TrawlDesign.sourceGraphAnchorOffset, size.height * 0.035)
    centreBase = CGPoint(x: size.width / 2, y: size.height / 2 + verticalOffset)
    sourceBases = Self.makeSourceBases(
      sources: sources,
      size: size,
      centre: centreBase,
      metrics: layoutMetrics
    )
    contextBases = Self.makeContextBases(sources: sourceBases, centre: centreBase)
    graphEdges = Self.makeGraphEdges(sourceCount: sources.count)

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
          diameter: diameters[index],
          metrics: metrics
        )
      },
      contextNodes: contextBases,
      segments: segments
    )
  }

  private func diameter(for _: RestingSource) -> CGFloat {
    CGFloat(metrics.minimumIconDiameter)
  }

  private static func makeSourceBases(
    sources: [RestingSource],
    size: CGSize,
    centre: CGPoint,
    metrics: ConstellationLayoutMetrics
  ) -> [CGPoint] {
    guard !sources.isEmpty else { return [] }
    let layout = ConstellationOrbitLayout(
      sourceIDs: sources.map(\.id),
      size: ConstellationPoint(x: Double(size.width), y: Double(size.height)),
      centre: ConstellationPoint(x: Double(centre.x), y: Double(centre.y)),
      metrics: metrics
    )
    return layout.placements().map {
      CGPoint(x: CGFloat($0.anchor.x), y: CGFloat($0.anchor.y))
    }
  }

  private static func makeContextBases(sources: [CGPoint], centre: CGPoint) -> [CGPoint] {
    sources.enumerated().map { index, source in
      let radialFraction = 0.43 + CGFloat(index % 3) * 0.035
      let tangentFraction = index.isMultiple(of: 2) ? 0.035 : -0.035
      let radial = CGVector(dx: source.x - centre.x, dy: source.y - centre.y)
      let tangent = CGVector(dx: -radial.dy * tangentFraction, dy: radial.dx * tangentFraction)
      return CGPoint(
        x: centre.x + radial.dx * radialFraction + tangent.dx,
        y: centre.y + radial.dy * radialFraction + tangent.dy
      )
    }
  }

  private static func makeGraphEdges(sourceCount: Int) -> [GraphEdge] {
    guard sourceCount > 0 else { return [] }
    let centreIndex = sourceCount
    let contextStart = sourceCount + 1
    var edges = Set<GraphEdge>()
    for sourceIndex in 0..<sourceCount {
      let contextIndex = contextStart + sourceIndex
      edges.insert(GraphEdge(sourceIndex, contextIndex))
      if sourceCount > 1 {
        edges.insert(GraphEdge(contextIndex, contextStart + (sourceIndex + 1) % sourceCount))
      }
    }
    for index in Set([0, sourceCount / 3, sourceCount * 2 / 3]) {
      edges.insert(GraphEdge(centreIndex, contextStart + index))
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
