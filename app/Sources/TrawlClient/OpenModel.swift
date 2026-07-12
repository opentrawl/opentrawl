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
  public let record: OpenRecord?
  public let failure: SourceFailure?
}
public struct PresentationDocument: Sendable, Equatable { public let title: String; public let blocks: [PresentationBlock]; public let actions: [PresentationAction]; public let facts: [PresentationFact] }
public enum PresentationBlock: Sendable, Equatable { case heading(String), prose(String), fields([PresentationField]), table(columns: [String], rows: [PresentationRow]), resource(PresentationResource) }
public struct PresentationField: Sendable, Equatable { public let label: String; public let display: String }
public enum PresentationRowRole: Sendable, Equatable { case normal, target }
public struct PresentationRow: Sendable, Equatable { public let role: PresentationRowRole; public let cells: [String] }
public enum PresentationResourceKind: Sendable, Equatable { case file, image, video, audio }
public struct PresentationResource: Sendable, Equatable { public let kind: PresentationResourceKind; public let label: String; public let ref: String; public let metadata: [PresentationField] }
public enum PresentationActionTarget: Sendable, Equatable { case openRef(String), url(URL) }
public struct PresentationAction: Sendable, Equatable { public let label: String; public let target: PresentationActionTarget }
public enum PresentationFactKind: Sendable, Equatable { case truncation, provenance, warning, error }
public struct PresentationFact: Sendable, Equatable { public let kind: PresentationFactKind; public let message: String; public let remedy: String }
