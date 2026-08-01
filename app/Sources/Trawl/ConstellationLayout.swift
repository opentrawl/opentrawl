import CoreGraphics
import Foundation
import TrawlClient
import TrawlCore

struct MovingTrawler: Identifiable {
  let trawler: RestingTrawler
  let anchor: CGPoint
  let diameter: CGFloat
  let metrics: ConstellationLayoutMetrics

  var id: RegisteredTrawlerIdentity { trawler.id }

  var motion: ConstellationMotion {
    ConstellationMotion(registeredTrawler: trawler.id)
  }
}

struct ConstellationSnapshot {
  let centre: CGPoint
  let centreDiameter: CGFloat
  let visualScale: CGFloat
  let trawlers: [MovingTrawler]
  let contextNodes: [CGPoint]
  let segments: [NetworkSegment]
}

struct NetworkEndpoint: Equatable {
  let anchor: CGPoint
  let trimRadius: CGFloat
  let registeredTrawler: RegisteredTrawlerIdentity?

  func point(offset: CGVector = .zero) -> CGPoint {
    CGPoint(x: anchor.x + offset.dx, y: anchor.y + offset.dy)
  }
}

struct NetworkSegment: Equatable {
  enum Kind: Equatable {
    case context
    case trawler
    case centre
  }

  let startEndpoint: NetworkEndpoint
  let endEndpoint: NetworkEndpoint
  let kind: Kind

  var movingRegisteredTrawler: RegisteredTrawlerIdentity? {
    switch (startEndpoint.registeredTrawler, endEndpoint.registeredTrawler) {
    case (.some(let registeredTrawler), nil), (nil, .some(let registeredTrawler)):
      registeredTrawler
    default:
      nil
    }
  }

  func points(trawlerOffset: CGVector = .zero) -> (start: CGPoint, end: CGPoint) {
    let startOffset =
      startEndpoint.registeredTrawler == movingRegisteredTrawler ? trawlerOffset : .zero
    let endOffset =
      endEndpoint.registeredTrawler == movingRegisteredTrawler ? trawlerOffset : .zero
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
  private let trawlers: [RestingTrawler]
  private let trawlerBases: [CGPoint]
  private let metrics: ConstellationLayoutMetrics
  private let contextBases: [CGPoint]
  private let centreBase: CGPoint
  private let centreDiameter: CGFloat
  private let visualScale: CGFloat
  private let graphEdges: [GraphEdge]

  init(size: CGSize, trawlers: [RestingTrawler]) {
    let layoutMetrics = ConstellationLayoutMetrics.forRegisteredTrawlerCount(
      trawlers.count,
      fitting: ConstellationPoint(x: size.width, y: size.height)
    )
    metrics = layoutMetrics
    visualScale = min(1, max(0.8, CGFloat(layoutMetrics.minimumIconDiameter / 44)))
    centreDiameter = TrawlDesign.centreSize
    let verticalOffset = -min(TrawlDesign.trawlerGraphAnchorOffset, size.height * 0.035)
    centreBase = CGPoint(x: size.width / 2, y: size.height / 2 + verticalOffset)
    let bases = Self.makeTrawlerBases(
      trawlers: trawlers,
      size: size,
      centre: centreBase,
      metrics: layoutMetrics
    )
    let supportedTrawlers = bases.count == trawlers.count ? trawlers : []
    self.trawlers = supportedTrawlers
    trawlerBases = supportedTrawlers.isEmpty ? [] : bases
    contextBases =
      supportedTrawlers.isEmpty
      ? []
      : Self.makeContextBases(
        count: max(10, min(18, supportedTrawlers.count + 3)),
        size: size,
        centre: centreBase,
        seed: TrawlDesign.meshSeed
      )
    graphEdges = Self.makeGraphEdges(
      points: trawlerBases + [centreBase] + contextBases,
      registeredTrawlerCount: supportedTrawlers.count
    )
  }

  func snapshot() -> ConstellationSnapshot {
    let diameters = trawlers.map(diameter)
    let points = trawlerBases + [centreBase] + contextBases
    let endpoints = zip(points.indices, points).map { index, point in
      if index < trawlers.count {
        return NetworkEndpoint(
          anchor: point,
          trimRadius: diameters[index] / 2,
          registeredTrawler: trawlers[index].id
        )
      }
      if index == trawlers.count {
        return NetworkEndpoint(
          anchor: point,
          trimRadius: centreDiameter / 2 + 2,
          registeredTrawler: nil
        )
      }
      return NetworkEndpoint(anchor: point, trimRadius: 2, registeredTrawler: nil)
    }

    let centreIndex = trawlers.count
    let segments = graphEdges.map { edge in
      let kind: NetworkSegment.Kind
      if edge.start == centreIndex || edge.end == centreIndex {
        kind = .centre
      } else if edge.start < trawlers.count || edge.end < trawlers.count {
        kind = .trawler
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
      centreDiameter: centreDiameter,
      visualScale: visualScale,
      trawlers: zip(trawlers, zip(trawlerBases, diameters)).map { trawler, placement in
        MovingTrawler(
          trawler: trawler,
          anchor: placement.0,
          diameter: placement.1,
          metrics: metrics
        )
      },
      contextNodes: contextBases,
      segments: segments
    )
  }

  private func diameter(for _: RestingTrawler) -> CGFloat {
    CGFloat(metrics.maximumIconDiameter)
  }

  private static func makeTrawlerBases(
    trawlers: [RestingTrawler],
    size: CGSize,
    centre: CGPoint,
    metrics: ConstellationLayoutMetrics
  ) -> [CGPoint] {
    guard !trawlers.isEmpty else { return [] }
    let layout = ConstellationOrbitLayout(
      registeredTrawlers: trawlers.map(\.id),
      size: ConstellationPoint(x: Double(size.width), y: Double(size.height)),
      centre: ConstellationPoint(x: Double(centre.x), y: Double(centre.y)),
      metrics: metrics
    )
    return layout.placements().map {
      CGPoint(x: CGFloat($0.anchor.x), y: CGFloat($0.anchor.y))
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
      let radius = CGFloat(0.11 + sqrt(fraction) * 0.20)
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

  private static func makeGraphEdges(
    points: [CGPoint],
    registeredTrawlerCount: Int
  ) -> [GraphEdge] {
    guard registeredTrawlerCount > 0 else { return [] }
    let centreIndex = registeredTrawlerCount
    let contextIndices = Array(points.indices.dropFirst(registeredTrawlerCount + 1))
    let contextIndexSet = Set(contextIndices)
    return triangulatedEdges(points: points).filter { edge in
      let startIsTrawler = edge.start < registeredTrawlerCount
      let endIsTrawler = edge.end < registeredTrawlerCount
      if startIsTrawler {
        return contextIndexSet.contains(edge.end)
      }
      if endIsTrawler {
        return contextIndexSet.contains(edge.start)
      }
      return edge.start == centreIndex || edge.end == centreIndex
        || (contextIndexSet.contains(edge.start) && contextIndexSet.contains(edge.end))
    }
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
