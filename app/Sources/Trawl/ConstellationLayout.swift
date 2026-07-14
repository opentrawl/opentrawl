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
  let centreDiameter: CGFloat
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

struct ConstellationLayout {
  private let sources: [RestingSource]
  private let sourceBases: [CGPoint]
  private let metrics: ConstellationLayoutMetrics
  private let contextBases: [CGPoint]
  private let centreBase: CGPoint
  private let centreDiameter: CGFloat
  private let graphEdges: [GraphEdge]

  init(size: CGSize, sources: [RestingSource]) {
    self.sources = sources
    let layoutMetrics = ConstellationLayoutMetrics.forSourceCount(
      sources.count,
      fitting: ConstellationPoint(x: size.width, y: size.height)
    )
    metrics = layoutMetrics
    centreDiameter = max(
      84,
      min(
        TrawlDesign.centreSize,
        CGFloat(layoutMetrics.minimumIconDiameter / 44) * TrawlDesign.centreSize))
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
          trimRadius: centreDiameter / 2 + 2,
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
      centreDiameter: centreDiameter,
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
    if sourceCount > 3 {
      for sourceIndex in stride(from: 0, to: sourceCount, by: 3) {
        let start = contextStart + sourceIndex
        let end = contextStart + (sourceIndex + 2) % sourceCount
        edges.insert(GraphEdge(start, end))
      }
    }
    for index in Set([0, sourceCount / 3, sourceCount * 2 / 3]) {
      edges.insert(GraphEdge(centreIndex, contextStart + index))
    }
    return edges.sorted()
  }

}
