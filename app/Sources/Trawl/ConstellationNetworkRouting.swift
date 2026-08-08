import CoreGraphics
import Foundation
import TrawlCore

/// Identifies one node of the constellation mesh with value identity: an app
/// source node, or a mesh anchor point (the centre or a context anchor)
/// numbered by first appearance in the segment list. Both traffic renderers
/// build their topology from the same segment list, so the numbering is
/// stable within a render pass.
enum ConstellationNetworkNodeIdentity: Hashable, Comparable {
  case source(String)
  case meshPoint(Int)

  static func < (lhs: Self, rhs: Self) -> Bool {
    switch (lhs, rhs) {
    case (.source(let lhsSourceID), .source(let rhsSourceID)):
      lhsSourceID < rhsSourceID
    case (.meshPoint(let lhsIndex), .meshPoint(let rhsIndex)):
      lhsIndex < rhsIndex
    case (.source, .meshPoint):
      true
    case (.meshPoint, .source):
      false
    }
  }
}

/// One mesh segment together with the direction a traveller crosses it.
struct ConstellationDirectedNetworkSegment {
  let segment: NetworkSegment
  let travelsFromStartToEnd: Bool

  var reversedTravelDirection: ConstellationDirectedNetworkSegment {
    ConstellationDirectedNetworkSegment(
      segment: segment,
      travelsFromStartToEnd: !travelsFromStartToEnd
    )
  }

  /// Departure and arrival points at a moment of the shared animation
  /// timeline, with the segment's moving-source orbit offset applied.
  func travelPoints(elapsed: TimeInterval) -> (departure: CGPoint, arrival: CGPoint) {
    let orbitOffset =
      segment.movingSourceID.map { movingSourceID in
        let translation = ConstellationMotion(sourceID: movingSourceID)
          .translation(elapsed: elapsed)
        return CGVector(dx: CGFloat(translation.dx), dy: CGFloat(translation.dy))
      } ?? CGVector.zero
    let points = segment.points(sourceOffset: orbitOffset)
    return travelsFromStartToEnd ? (points.start, points.end) : (points.end, points.start)
  }
}

/// The constellation mesh as a walkable graph with stable node identities,
/// shared by the search and archive-update traffic renderers.
struct ConstellationNetworkTopology {
  struct Connection {
    let neighbour: ConstellationNetworkNodeIdentity
    let neighbourAnchor: CGPoint
    let directedSegment: ConstellationDirectedNetworkSegment
  }

  private let connectionsByNode: [ConstellationNetworkNodeIdentity: [Connection]]
  private let anchorByNode: [ConstellationNetworkNodeIdentity: CGPoint]

  init(segments: [NetworkSegment]) {
    var meshPointAnchorsInFirstAppearanceOrder: [CGPoint] = []
    func nodeIdentity(of endpoint: NetworkEndpoint) -> ConstellationNetworkNodeIdentity {
      if let sourceID = endpoint.sourceID {
        return .source(sourceID)
      }
      if let existingIndex = meshPointAnchorsInFirstAppearanceOrder.firstIndex(
        of: endpoint.anchor)
      {
        return .meshPoint(existingIndex)
      }
      meshPointAnchorsInFirstAppearanceOrder.append(endpoint.anchor)
      return .meshPoint(meshPointAnchorsInFirstAppearanceOrder.count - 1)
    }

    var connections: [ConstellationNetworkNodeIdentity: [Connection]] = [:]
    var anchors: [ConstellationNetworkNodeIdentity: CGPoint] = [:]
    for segment in segments {
      let startNode = nodeIdentity(of: segment.startEndpoint)
      let endNode = nodeIdentity(of: segment.endEndpoint)
      anchors[startNode] = segment.startEndpoint.anchor
      anchors[endNode] = segment.endEndpoint.anchor
      connections[startNode, default: []].append(
        Connection(
          neighbour: endNode,
          neighbourAnchor: segment.endEndpoint.anchor,
          directedSegment: ConstellationDirectedNetworkSegment(
            segment: segment,
            travelsFromStartToEnd: true
          )
        )
      )
      connections[endNode, default: []].append(
        Connection(
          neighbour: startNode,
          neighbourAnchor: segment.startEndpoint.anchor,
          directedSegment: ConstellationDirectedNetworkSegment(
            segment: segment,
            travelsFromStartToEnd: false
          )
        )
      )
    }
    connectionsByNode = connections.mapValues { nodeConnections in
      nodeConnections.sorted { $0.neighbour < $1.neighbour }
    }
    anchorByNode = anchors
  }

  /// The node at an exact anchor point, such as the constellation centre.
  /// Nil when the segment list does not reach that anchor.
  func nodeIdentity(atAnchor anchor: CGPoint) -> ConstellationNetworkNodeIdentity? {
    anchorByNode.first(where: { $0.value == anchor })?.key
  }

  /// Deterministically ordered connections. A node the topology has never
  /// seen has no connections.
  func connections(from node: ConstellationNetworkNodeIdentity) -> [Connection] {
    connectionsByNode[node] ?? []
  }

  /// Every node identity this topology produces has an anchor by
  /// construction.
  func anchor(of node: ConstellationNetworkNodeIdentity) -> CGPoint {
    guard let anchor = anchorByNode[node] else {
      preconditionFailure("topology produced a node identity without an anchor")
    }
    return anchor
  }

  /// Breadth-first shortest route. Nil when the destination is unreachable.
  func shortestRoute(
    from start: ConstellationNetworkNodeIdentity,
    to destination: ConstellationNetworkNodeIdentity
  ) -> [ConstellationDirectedNetworkSegment]? {
    var explorationQueue: [(ConstellationNetworkNodeIdentity, [ConstellationDirectedNetworkSegment])] =
      [(start, [])]
    var visitedNodes = Set([start])
    while !explorationQueue.isEmpty {
      let (currentNode, routeSoFar) = explorationQueue.removeFirst()
      for connection in connections(from: currentNode)
      where visitedNodes.insert(connection.neighbour).inserted {
        let candidateRoute = routeSoFar + [connection.directedSegment]
        if connection.neighbour == destination { return candidateRoute }
        explorationQueue.append((connection.neighbour, candidateRoute))
      }
    }
    return nil
  }
}
