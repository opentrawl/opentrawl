import Foundation

public struct OpenRecord: Sendable, Equatable {
  public let sourceID: String
  public let openRef: String
  public let typeURL: String
  public let value: Data
  public let presentation: PresentationDocument
}
public struct OpenResponse: Sendable, Equatable {
  public let outcome: OperationOutcome
  public let requestedRef: String
  public let requestedAnchorID: String
  public let record: OpenRecord?
  public let failure: SourceFailure?
}
public struct PresentationDocument: Sendable, Equatable {
  public let title: String
  public let primaryAnchorID: String
  public let blocks: [PresentationBlock]
  public let actions: [PresentationAction]
  public let facts: [PresentationFact]

  func containsAnchor(_ wanted: String) -> Bool {
    blocks.contains { block in
      switch block {
      case .heading(let anchorID, _), .prose(let anchorID, _):
        anchorID == wanted
      case .fields(let anchorID, let fields):
        anchorID == wanted || fields.contains { $0.anchorID == wanted }
      case .table(let anchorID, _, let rows):
        anchorID == wanted || rows.contains { $0.anchorID == wanted }
      case .resource(let anchorID, let resource):
        anchorID == wanted || resource.anchorID == wanted
          || resource.metadata.contains { $0.anchorID == wanted }
      }
    }
  }
}
public enum PresentationBlock: Sendable, Equatable {
  case heading(anchorID: String, text: String)
  case prose(anchorID: String, text: String)
  case fields(anchorID: String, [PresentationField])
  case table(anchorID: String, columns: [String], rows: [PresentationRow])
  case resource(anchorID: String, PresentationResource)
}
public struct PresentationField: Sendable, Equatable {
  public let label: String
  public let display: String
  public let anchorID: String
}
public enum PresentationRowRole: Sendable, Equatable { case normal, target }
public struct PresentationRow: Sendable, Equatable {
  public let role: PresentationRowRole
  public let cells: [String]
  public let anchorID: String
}
public enum PresentationResourceKind: Sendable, Equatable { case file, image, video, audio }
public struct PresentationResource: Sendable, Equatable {
  public let kind: PresentationResourceKind
  public let label: String
  public let ref: String
  public let metadata: [PresentationField]
  public let anchorID: String
}
public struct PresentationResourceData: Sendable, Equatable {
  public let ref: String
  public let contentType: String
  public let data: Data
}
public enum PresentationActionTarget: Sendable, Equatable {
  case openRef(String)
  case url(URL)
}
public struct PresentationAction: Sendable, Equatable {
  public let label: String
  public let target: PresentationActionTarget
}
public enum PresentationFactKind: Sendable, Equatable {
  case truncation, provenance, warning, error
}
public struct PresentationFact: Sendable, Equatable {
  public let kind: PresentationFactKind
  public let message: String
  public let remedy: String
}
